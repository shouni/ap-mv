package step

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-veo-orchestrator/video"

	"github.com/shouni/ap-mv/internal/domain"
)

// memoryWriter captures what the step would persist to GCS.
type memoryWriter struct {
	mu      sync.Mutex
	objects map[string][]byte
	err     error
}

func newMemoryWriter() *memoryWriter {
	return &memoryWriter{objects: map[string][]byte{}}
}

func (w *memoryWriter) Write(_ context.Context, path string, contentReader io.Reader, _ ...remoteio.WriteOption) error {
	if w.err != nil {
		return w.err
	}
	raw, err := io.ReadAll(contentReader)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.objects[path] = raw
	return nil
}

func (w *memoryWriter) Delete(_ context.Context, path string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.objects, path)
	return nil
}

func (w *memoryWriter) usage(t *testing.T, path string) domain.VeoUsage {
	t.Helper()
	w.mu.Lock()
	raw, ok := w.objects[path]
	w.mu.Unlock()
	if !ok {
		t.Fatalf("no object written at %q (wrote: %v)", path, w.paths())
	}
	var usage domain.VeoUsage
	if err := json.Unmarshal(raw, &usage); err != nil {
		t.Fatalf("unmarshal usage: %v (raw=%s)", err, raw)
	}
	return usage
}

func (w *memoryWriter) paths() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	paths := make([]string, 0, len(w.objects))
	for path := range w.objects {
		paths = append(paths, path)
	}
	return paths
}

// writerBackedReader serves objects a memoryWriter already holds, so a test can exercise the
// read-modify-write cycle across successive cuts the way GCS would.
type writerBackedReader struct {
	writer *memoryWriter
}

func (r writerBackedReader) Open(_ context.Context, path string) (io.ReadCloser, error) {
	r.writer.mu.Lock()
	raw, ok := r.writer.objects[path]
	r.writer.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("open %s: %w", path, os.ErrNotExist)
	}
	return io.NopCloser(bytes.NewReader(raw)), nil
}

const testUsageURI = "gs://bucket/jobs/job-1/" + domain.VeoUsageFileName

func newUsageContext(writer *memoryWriter, task *domain.Task) *Context {
	return &Context{
		Task: task, OutputPath: "gs://bucket/jobs/job-1/",
		Writer: writer,
		Reader: writerBackedReader{writer: writer},
	}
}

// TestRecordVeoUsageAccumulatesAcrossCuts verifies the tally survives the read-modify-write
// cycle: each cut is generated in a separate task execution, so the count only accumulates if
// the previous record is loaded back from storage first.
func TestRecordVeoUsageAccumulatesAcrossCuts(t *testing.T) {
	writer := newMemoryWriter()
	sc := newUsageContext(writer, &domain.Task{JobID: "job-1", VeoModel: "veo-test"})

	recordVeoUsage(context.Background(), sc, &video.Cut{CutIndex: 1, DurationSec: 8})
	recordVeoUsage(context.Background(), sc, &video.Cut{CutIndex: 2, DurationSec: 6})
	// 再配信でカット1が焼き直された、という状況。
	recordVeoUsage(context.Background(), sc, &video.Cut{CutIndex: 1, DurationSec: 8})

	usage := writer.usage(t, testUsageURI)
	if usage.Calls != 3 {
		t.Errorf("Calls = %d, want 3", usage.Calls)
	}
	if usage.SubmittedSeconds != 22 {
		t.Errorf("SubmittedSeconds = %v, want 22", usage.SubmittedSeconds)
	}
	if usage.JobID != "job-1" {
		t.Errorf("JobID = %q, want job-1", usage.JobID)
	}
	if usage.Model != "veo-test" {
		t.Errorf("Model = %q, want veo-test", usage.Model)
	}
	if usage.SchemaVersion != domain.VeoUsageSchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", usage.SchemaVersion, domain.VeoUsageSchemaVersion)
	}
	if got := usage.CutCalls(1); got != 2 {
		t.Errorf("CutCalls(1) = %d, want 2 (the cut was regenerated)", got)
	}
}

// TestRecordVeoUsageSurvivesWriteFailure is the important one: accounting must never break
// generation. The caller has already been billed for the Veo call by this point, so returning
// an error here would make Cloud Tasks retry and pay for the same cut again.
func TestRecordVeoUsageSurvivesWriteFailure(t *testing.T) {
	writer := newMemoryWriter()
	writer.err = errors.New("storage unavailable")
	sc := newUsageContext(writer, &domain.Task{JobID: "job-1"})

	recordVeoUsage(context.Background(), sc, &video.Cut{CutIndex: 1, DurationSec: 8})

	if len(writer.paths()) != 0 {
		t.Fatalf("wrote %v despite the failure", writer.paths())
	}
}

// TestRecordVeoUsageStartsFreshOnCorruptRecord verifies a damaged record is replaced rather than
// blocking further accounting.
func TestRecordVeoUsageStartsFreshOnCorruptRecord(t *testing.T) {
	writer := newMemoryWriter()
	writer.objects[testUsageURI] = []byte("{not json")
	sc := newUsageContext(writer, &domain.Task{JobID: "job-1"})

	recordVeoUsage(context.Background(), sc, &video.Cut{CutIndex: 1, DurationSec: 8})

	usage := writer.usage(t, testUsageURI)
	if usage.Calls != 1 || usage.SubmittedSeconds != 8 {
		t.Fatalf("usage = %+v, want a fresh record with one call", usage)
	}
}

func TestRecordVeoUsageSkipsWithoutOutputPath(t *testing.T) {
	writer := newMemoryWriter()
	sc := newUsageContext(writer, &domain.Task{JobID: "job-1"})
	sc.OutputPath = ""

	recordVeoUsage(context.Background(), sc, &video.Cut{CutIndex: 1, DurationSec: 8})

	if len(writer.paths()) != 0 {
		t.Fatalf("wrote %v, want nothing without an output path", writer.paths())
	}
}

func TestVeoUsageURITrimsTrailingSlash(t *testing.T) {
	want := "gs://bucket/jobs/job-1/" + domain.VeoUsageFileName
	for _, outputPath := range []string{"gs://bucket/jobs/job-1", "gs://bucket/jobs/job-1/", " gs://bucket/jobs/job-1/ "} {
		if got := veoUsageURI(outputPath); got != want {
			t.Errorf("veoUsageURI(%q) = %q, want %q", outputPath, got, want)
		}
	}
	if got := veoUsageURI("   "); got != "" {
		t.Errorf("veoUsageURI(blank) = %q, want empty", got)
	}
}

// TestVideoGenerationStepRecordsUsagePerCut verifies the tally is written as part of normal
// generation, not just when recordVeoUsage is called directly.
func TestVideoGenerationStepRecordsUsagePerCut(t *testing.T) {
	recipe := &video.Recipe{
		MusicRecipe: video.MusicRecipe{Title: "test"},
		Cuts: []video.Cut{
			{CutIndex: 1, VisualAnchor: "first", DurationSec: 8},
		},
	}
	task := &domain.Task{
		JobID:       "job-1",
		Command:     domain.CommandMVFromKeyframeVideoRecipe,
		VideoRecipe: recipe,
		VeoModel:    "veo-test",
	}
	writer := newMemoryWriter()
	sc := newUsageContext(writer, task)
	sc.VideoRecipe = recipe
	st := VideoGenerationStep{Runner: sequenceRunner{}}

	if err := st.Execute(context.Background(), sc); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	usage := writer.usage(t, testUsageURI)
	if usage.Calls != 1 {
		t.Fatalf("Calls = %d, want 1", usage.Calls)
	}
	if usage.SubmittedSeconds != recipe.Cuts[0].DurationSec {
		t.Fatalf("SubmittedSeconds = %v, want the generated cut's duration %v", usage.SubmittedSeconds, recipe.Cuts[0].DurationSec)
	}
	if !strings.Contains(testUsageURI, domain.VeoUsageFileName) {
		t.Fatalf("usage URI %q lost the file name", testUsageURI)
	}
}
