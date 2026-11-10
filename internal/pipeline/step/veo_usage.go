package step

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-veo-orchestrator/video"

	"github.com/shouni/ap-mv/internal/domain"
)

// recordVeoUsage adds the usage of one successfully generated cut to veo_usage.json.
//
// Unlike the finished length (computable from the VideoRecipe), how many times Veo was actually
// called is known only at the moment of generation. Re-rendering the same cut on a Cloud Tasks
// redelivery leaves the finished piece unchanged while only the billing grows; making that gap
// visible is the purpose of this record.
//
// A failed record is not returned. Stopping generation for the sake of accounting would put the
// cart before the horse (the caller has already been billed by Veo, and returning a failure here
// would make Cloud Tasks come back to re-render the same cut). Failures are logged, accepting
// that usage is undercounted.
func recordVeoUsage(ctx context.Context, sc *Context, cut *video.Cut) {
	if sc == nil || cut == nil || sc.Writer == nil {
		return
	}
	uri := veoUsageURI(sc.OutputPath)
	if uri == "" {
		return
	}

	usage := loadVeoUsage(ctx, sc, uri)
	if sc.Task != nil {
		usage.JobID = sc.Task.JobID
	}
	usage.Record(cut.CutIndex, cut.DurationSec, taskVeoModel(sc.Task), time.Now().UTC())

	if err := writeVeoUsage(ctx, sc.Writer, uri, usage); err != nil {
		slog.WarnContext(ctx, "failed to record veo usage; generation continues",
			"uri", uri,
			"cut_index", cut.CutIndex,
			"error", err,
		)
	}
}

// veoUsageURI builds the usage file URI under a job's output directory.
func veoUsageURI(outputPath string) string {
	outputPath = strings.TrimRight(strings.TrimSpace(outputPath), "/")
	if outputPath == "" {
		return ""
	}
	return outputPath + "/" + domain.VeoUsageFileName
}

// taskVeoModel returns the Veo model the task pinned, or "" when it left the choice to the
// configured default. Recording "" rather than guessing keeps the reader honest: it falls back
// to the model configured at display time and says so.
func taskVeoModel(task *domain.Task) string {
	if task == nil {
		return ""
	}
	return strings.TrimSpace(task.VeoModel)
}

// loadVeoUsage reads the existing record, returning an empty one for the first cut of a job
// (or whenever the file can't be read — a broken record must not stop generation, so this
// starts a fresh count rather than failing).
func loadVeoUsage(ctx context.Context, sc *Context, uri string) *domain.VeoUsage {
	usage := &domain.VeoUsage{SchemaVersion: domain.VeoUsageSchemaVersion}
	if sc.Reader == nil {
		return usage
	}
	rc, err := sc.Reader.Open(ctx, uri)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			slog.WarnContext(ctx, "failed to read veo usage; starting a fresh record", "uri", uri, "error", err)
		}
		return usage
	}
	defer func() { _ = rc.Close() }()

	raw, err := io.ReadAll(rc)
	if err != nil {
		slog.WarnContext(ctx, "failed to read veo usage; starting a fresh record", "uri", uri, "error", err)
		return usage
	}
	if err := json.Unmarshal(raw, usage); err != nil {
		slog.WarnContext(ctx, "failed to parse veo usage; starting a fresh record", "uri", uri, "error", err)
		return &domain.VeoUsage{SchemaVersion: domain.VeoUsageSchemaVersion}
	}
	return usage
}

// writeVeoUsage persists the record.
func writeVeoUsage(ctx context.Context, writer remoteio.Writer, uri string, usage *domain.VeoUsage) error {
	raw, err := json.MarshalIndent(usage, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal veo usage: %w", err)
	}
	if err := writer.Write(ctx, uri, bytes.NewReader(raw), remoteio.WithContentType("application/json")); err != nil {
		return fmt.Errorf("write veo usage: %w", err)
	}
	return nil
}
