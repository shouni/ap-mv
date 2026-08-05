package repository

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shouni/go-remote-io/remoteio"
)

type fakeHistoryReader struct {
	mu        sync.Mutex
	paths     []string
	files     map[string]string
	openCount map[string]int
	listCount map[string]int
}

func (r *fakeHistoryReader) Open(_ context.Context, p string) (io.ReadCloser, error) {
	r.mu.Lock()
	if r.openCount == nil {
		r.openCount = map[string]int{}
	}
	r.openCount[p]++
	r.mu.Unlock()
	return io.NopCloser(strings.NewReader(r.files[p])), nil
}

func (r *fakeHistoryReader) List(_ context.Context, prefix string, callback func(path string) error, _ ...remoteio.ListOption) error {
	r.mu.Lock()
	if r.listCount == nil {
		r.listCount = map[string]int{}
	}
	r.listCount[prefix]++
	r.mu.Unlock()

	for _, p := range r.paths {
		if err := callback(p); err != nil {
			return err
		}
	}
	return nil
}

// ListCount は指定プレフィックスに対する List 呼び出し回数を返します。
func (r *fakeHistoryReader) ListCount(prefix string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.listCount[prefix]
}

func (r *fakeHistoryReader) Exists(context.Context, string) (bool, error) {
	return true, nil
}

func (r *fakeHistoryReader) OpenCount(p string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.openCount[p]
}

type fakeHistoryWriter struct {
	mu      sync.Mutex
	deleted []string
}

func (w *fakeHistoryWriter) Write(context.Context, string, io.Reader, ...remoteio.WriteOption) error {
	return nil
}

func (w *fakeHistoryWriter) Delete(_ context.Context, path string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.deleted = append(w.deleted, path)
	return nil
}

type fakeHistorySigner struct{}

func (s fakeHistorySigner) GenerateSignedURL(_ context.Context, uri string, _ string, _ time.Duration) (string, error) {
	return "https://signed.example/" + strings.TrimPrefix(uri, "gs://"), nil
}

type countingHistorySigner struct {
	mu    sync.Mutex
	count map[string]int
}

func (s *countingHistorySigner) GenerateSignedURL(_ context.Context, uri string, _ string, _ time.Duration) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.count == nil {
		s.count = map[string]int{}
	}
	s.count[uri]++
	return "https://signed.example/" + strings.TrimPrefix(uri, "gs://"), nil
}

func (s *countingHistorySigner) Count(uri string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.count[uri]
}

func TestListHistoryPageLoadsVideoMetadata(t *testing.T) {
	t.Parallel()

	const metadataURI = "gs://bucket/ap-mv/veo/jobs/video-recipe-20260618-081931-abc/video_music_meta.json"
	reader := &fakeHistoryReader{
		paths: []string{
			metadataURI,
			"gs://bucket/ap-mv/veo/jobs/video-recipe-20260618-081931-abc/images/keyframe_001.png",
		},
		files: map[string]string{
			metadataURI: `{
				"title": "軌跡のアーキテクト",
				"mood": "Sparkling",
				"tempo": 168,
				"compose_mode": "sparkle_rock",
				"cuts": [
					{"cut_index": 1, "duration_sec": 8, "visual_anchor": "stage", "status": "generated"},
					{"cut_index": 2, "duration_sec": 8, "visual_anchor": "sky", "status": "generated"}
				]
			}`,
		},
	}
	repo := NewVideoHistoryRepository(VideoHistoryRepositoryConfig{BaseURI: "gs://bucket/ap-mv/veo/jobs", Reader: reader, HistoryCache: NewHistoryCache()})

	page, err := repo.ListHistoryPage(context.Background(), 1, 20)
	if err != nil {
		t.Fatalf("ListHistoryPage() error = %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("len(page.Items) = %d, want 1", len(page.Items))
	}
	got := page.Items[0]
	if got.JobID != "video-recipe-20260618-081931-abc" {
		t.Fatalf("JobID = %q", got.JobID)
	}
	if got.Title != "軌跡のアーキテクト" {
		t.Fatalf("Title = %q", got.Title)
	}
	if got.CutCount != 2 {
		t.Fatalf("CutCount = %d, want 2", got.CutCount)
	}
	if !got.Generated {
		t.Fatal("Generated = false, want true")
	}
}

func TestGetHistoryLoadsCutKeyframeURLs(t *testing.T) {
	t.Parallel()

	const metadataURI = "gs://bucket/ap-mv/veo/jobs/video-recipe-20260618-081931-abc/video_music_meta.json"
	reader := &fakeHistoryReader{
		files: map[string]string{
			metadataURI: `{
				"title": "軌跡のアーキテクト",
				"cuts": [
					{"cut_index": 1, "duration_sec": 8, "visual_anchor": "stage", "keyframe_reference": "images/keyframe_001.png", "status": "pending"},
					{"cut_index": 2, "duration_sec": 7.5, "visual_anchor": "sky", "keyframe_reference": "gs://bucket/ap-mv/veo/jobs/video-recipe-20260618-081931-abc/images/keyframe_002.png", "status": "generated"}
				]
			}`,
		},
	}
	repo := NewVideoHistoryRepository(VideoHistoryRepositoryConfig{BaseURI: "gs://bucket/ap-mv/veo/jobs", Reader: reader, Signer: fakeHistorySigner{}, HistoryCache: NewHistoryCache()})

	history, err := repo.GetHistory(context.Background(), "video-recipe-20260618-081931-abc")
	if err != nil {
		t.Fatalf("GetHistory() error = %v", err)
	}
	if history.Title != "軌跡のアーキテクト" {
		t.Fatalf("Title = %q", history.Title)
	}
	if len(history.Cuts) != 2 {
		t.Fatalf("len(Cuts) = %d, want 2", len(history.Cuts))
	}
	if history.Cuts[0].KeyframeReference != "gs://bucket/ap-mv/veo/jobs/video-recipe-20260618-081931-abc/images/keyframe_001.png" {
		t.Fatalf("first keyframe reference = %q", history.Cuts[0].KeyframeReference)
	}
	if history.Cuts[0].KeyframeURL == "" || !strings.HasPrefix(history.Cuts[0].KeyframeURL, "https://signed.example/") {
		t.Fatalf("first keyframe URL = %q", history.Cuts[0].KeyframeURL)
	}
	if history.Cuts[1].DurationSec != 7.5 {
		t.Fatalf("second duration = %v", history.Cuts[1].DurationSec)
	}
}

// TestGetHistoryAlwaysReadsFreshEvenAfterHistoryListCachedIt verifies GetHistory never reuses a
// recipe cached by ListHistoryPage's bulk read: a single-job detail read always goes straight to
// storage, so it can't serve a stale pre-regenerate/edit snapshot (this matters most under
// multiple running instances, where a worker instance's cache invalidation can't reach every
// other instance's in-memory cache).
func TestGetHistoryAlwaysReadsFreshEvenAfterHistoryListCachedIt(t *testing.T) {
	t.Parallel()

	const (
		jobID       = "video-recipe-20260618-081931-abc"
		metadataURI = "gs://bucket/ap-mv/veo/jobs/video-recipe-20260618-081931-abc/video_music_meta.json"
	)
	reader := &fakeHistoryReader{
		paths: []string{metadataURI},
		files: map[string]string{
			metadataURI: `{
				"title": "軌跡のアーキテクト",
				"cuts": [
					{"cut_index": 1, "duration_sec": 8, "visual_anchor": "stage", "keyframe_reference": "images/keyframe_001.png"}
				]
			}`,
		},
	}
	repo := NewVideoHistoryRepository(VideoHistoryRepositoryConfig{BaseURI: "gs://bucket/ap-mv/veo/jobs", Reader: reader, Signer: fakeHistorySigner{}, HistoryCache: NewHistoryCache()})

	if _, err := repo.ListHistoryPage(context.Background(), 1, 20); err != nil {
		t.Fatalf("ListHistoryPage() error = %v", err)
	}
	if _, err := repo.GetHistory(context.Background(), jobID); err != nil {
		t.Fatalf("GetHistory() error = %v", err)
	}
	if got := reader.OpenCount(metadataURI); got != 2 {
		t.Fatalf("metadata open count = %d, want 2 (ListHistoryPage's cached read must not be reused by GetHistory)", got)
	}
}

// TestDownloadKeyframesAlwaysReadsFreshEvenAfterHistoryListCachedIt mirrors the GetHistory case:
// downloading keyframes is a deliberate, low-frequency action where a user expects the current
// state, so it must not silently serve a recipe cached by an earlier ListHistoryPage call.
func TestDownloadKeyframesAlwaysReadsFreshEvenAfterHistoryListCachedIt(t *testing.T) {
	t.Parallel()

	const (
		jobID       = "video-recipe-20260618-081931-abc"
		metadataURI = "gs://bucket/ap-mv/veo/jobs/video-recipe-20260618-081931-abc/video_music_meta.json"
		keyframeURI = "gs://bucket/ap-mv/veo/jobs/video-recipe-20260618-081931-abc/images/keyframe_001.png"
	)
	reader := &fakeHistoryReader{
		paths: []string{metadataURI, keyframeURI},
		files: map[string]string{
			metadataURI: `{
				"title": "軌跡のアーキテクト",
				"cuts": [
					{"cut_index": 1, "duration_sec": 8, "visual_anchor": "stage", "keyframe_reference": "images/keyframe_001.png"}
				]
			}`,
			keyframeURI: "fake-image-bytes",
		},
	}
	repo := NewVideoHistoryRepository(VideoHistoryRepositoryConfig{BaseURI: "gs://bucket/ap-mv/veo/jobs", Reader: reader, Signer: fakeHistorySigner{}, HistoryCache: NewHistoryCache()})

	if _, err := repo.ListHistoryPage(context.Background(), 1, 20); err != nil {
		t.Fatalf("ListHistoryPage() error = %v", err)
	}
	if err := repo.DownloadKeyframes(context.Background(), jobID, func(string, io.Reader) error { return nil }); err != nil {
		t.Fatalf("DownloadKeyframes() error = %v", err)
	}
	if got := reader.OpenCount(metadataURI); got != 2 {
		t.Fatalf("metadata open count = %d, want 2 (ListHistoryPage's cached read must not be reused by DownloadKeyframes)", got)
	}
}

func TestListHistoryPageRegeneratesSignedURLAfterCacheHit(t *testing.T) {
	t.Parallel()

	const metadataURI = "gs://bucket/ap-mv/veo/jobs/video-recipe-20260618-081931-abc/video_music_meta.json"
	reader := &fakeHistoryReader{
		paths: []string{metadataURI},
		files: map[string]string{
			metadataURI: `{
				"title": "軌跡のアーキテクト",
				"cuts": [
					{"cut_index": 1, "duration_sec": 8, "visual_anchor": "stage"}
				]
			}`,
		},
	}
	signer := &countingHistorySigner{}
	repo := NewVideoHistoryRepository(VideoHistoryRepositoryConfig{BaseURI: "gs://bucket/ap-mv/veo/jobs", Reader: reader, Signer: signer, HistoryCache: NewHistoryCache()})

	if _, err := repo.ListHistoryPage(context.Background(), 1, 20); err != nil {
		t.Fatalf("first ListHistoryPage() error = %v", err)
	}
	if _, err := repo.ListHistoryPage(context.Background(), 1, 20); err != nil {
		t.Fatalf("second ListHistoryPage() error = %v", err)
	}
	if got := signer.Count(metadataURI); got != 2 {
		t.Fatalf("metadata signed URL count = %d, want 2", got)
	}
	if got := reader.OpenCount(metadataURI); got != 1 {
		t.Fatalf("metadata open count = %d, want 1", got)
	}
}

func TestGetHistoryReturnsErrorWhenRepositoryMisconfigured(t *testing.T) {
	t.Parallel()

	repo := NewVideoHistoryRepository(VideoHistoryRepositoryConfig{BaseURI: "", Reader: nil, HistoryCache: NewHistoryCache()})

	if _, err := repo.GetHistory(context.Background(), "video-recipe-20260618-081931-abc"); err == nil {
		t.Fatal("GetHistory() error = nil, want configuration error")
	}
}

func TestDeleteHistoryDeletesJobObjects(t *testing.T) {
	t.Parallel()

	reader := &fakeHistoryReader{
		paths: []string{
			"gs://bucket/ap-mv/veo/jobs/job-1/video_music_meta.json",
			"gs://bucket/ap-mv/veo/jobs/job-1/images/keyframe_001.png",
		},
		files: map[string]string{},
	}
	writer := &fakeHistoryWriter{}
	repo := NewVideoHistoryRepository(VideoHistoryRepositoryConfig{BaseURI: "gs://bucket/ap-mv/veo/jobs", Reader: reader, Writer: writer, HistoryCache: NewHistoryCache()})

	if err := repo.DeleteHistory(context.Background(), "job-1"); err != nil {
		t.Fatalf("DeleteHistory() error = %v", err)
	}
	if len(writer.deleted) != 2 {
		t.Fatalf("deleted count = %d, want 2: %#v", len(writer.deleted), writer.deleted)
	}
}

// notFoundReader serves a fixed set of objects and reports os.ErrNotExist for anything else,
// matching how remoteio's GCS reader signals a missing object.
type notFoundReader struct {
	files map[string]string
}

func (r notFoundReader) Open(_ context.Context, p string) (io.ReadCloser, error) {
	content, ok := r.files[p]
	if !ok {
		return nil, fmt.Errorf("open %s: %w", p, os.ErrNotExist)
	}
	return io.NopCloser(strings.NewReader(content)), nil
}

func (r notFoundReader) List(context.Context, string, func(string) error, ...remoteio.ListOption) error {
	return nil
}

func (r notFoundReader) Exists(_ context.Context, p string) (bool, error) {
	_, ok := r.files[p]
	return ok, nil
}

const testUsageJobID = "video-recipe-20260618-081931-abc"

func TestGetVeoUsageReadsRecordedTally(t *testing.T) {
	t.Parallel()

	usageURI := "gs://bucket/ap-mv/veo/jobs/" + testUsageJobID + "/veo_usage.json"
	repo := NewVideoHistoryRepository(VideoHistoryRepositoryConfig{
		BaseURI: "gs://bucket/ap-mv/veo/jobs",
		Reader: notFoundReader{files: map[string]string{
			usageURI: `{"schema_version":1,"job_id":"` + testUsageJobID + `","model":"veo-test","calls":3,"submitted_seconds":22,"cuts":[{"cut_index":1,"calls":2,"submitted_seconds":16}]}`,
		}},
		HistoryCache: NewHistoryCache(),
	})

	usage, err := repo.GetVeoUsage(context.Background(), testUsageJobID)
	if err != nil {
		t.Fatalf("GetVeoUsage() error = %v", err)
	}
	if usage == nil {
		t.Fatal("GetVeoUsage() = nil, want a record")
	}
	if usage.Calls != 3 || usage.SubmittedSeconds != 22 || usage.Model != "veo-test" {
		t.Fatalf("usage = %+v, want the recorded tally", usage)
	}
	if got := usage.CutCalls(1); got != 2 {
		t.Fatalf("CutCalls(1) = %d, want 2", got)
	}
}

// TestGetVeoUsageTreatsMissingRecordAsNoData verifies jobs that predate the tally (or stopped
// after keyframes) don't surface as an error — the detail page falls back to the recipe-derived
// estimate instead of failing to render.
func TestGetVeoUsageTreatsMissingRecordAsNoData(t *testing.T) {
	t.Parallel()

	repo := NewVideoHistoryRepository(VideoHistoryRepositoryConfig{
		BaseURI:      "gs://bucket/ap-mv/veo/jobs",
		Reader:       notFoundReader{files: map[string]string{}},
		HistoryCache: NewHistoryCache(),
	})

	usage, err := repo.GetVeoUsage(context.Background(), testUsageJobID)
	if err != nil {
		t.Fatalf("GetVeoUsage() error = %v, want nil for a missing record", err)
	}
	if usage != nil {
		t.Fatalf("GetVeoUsage() = %+v, want nil", usage)
	}
}

// TestGetVeoUsageTreatsEmptyObjectAsNoData covers a truncated write leaving a zero-byte object.
func TestGetVeoUsageTreatsEmptyObjectAsNoData(t *testing.T) {
	t.Parallel()

	usageURI := "gs://bucket/ap-mv/veo/jobs/" + testUsageJobID + "/veo_usage.json"
	repo := NewVideoHistoryRepository(VideoHistoryRepositoryConfig{
		BaseURI:      "gs://bucket/ap-mv/veo/jobs",
		Reader:       notFoundReader{files: map[string]string{usageURI: "  \n"}},
		HistoryCache: NewHistoryCache(),
	})

	usage, err := repo.GetVeoUsage(context.Background(), testUsageJobID)
	if err != nil {
		t.Fatalf("GetVeoUsage() error = %v, want nil for an empty object", err)
	}
	if usage != nil {
		t.Fatalf("GetVeoUsage() = %+v, want nil", usage)
	}
}

func TestGetVeoUsageRejectsInvalidJobID(t *testing.T) {
	t.Parallel()

	repo := NewVideoHistoryRepository(VideoHistoryRepositoryConfig{
		BaseURI:      "gs://bucket/ap-mv/veo/jobs",
		Reader:       notFoundReader{files: map[string]string{}},
		HistoryCache: NewHistoryCache(),
	})

	if _, err := repo.GetVeoUsage(context.Background(), "../escape"); err == nil {
		t.Fatal("GetVeoUsage() error = nil, want a validation error for a traversal-style job ID")
	}
}
