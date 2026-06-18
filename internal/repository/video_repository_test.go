package repository

import (
	"context"
	"io"
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

func (r *fakeHistoryReader) List(_ context.Context, _ string, callback func(path string) error) error {
	for _, p := range r.paths {
		if err := callback(p); err != nil {
			return err
		}
	}
	return nil
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
	repo := NewVideoHistoryRepository("gs://bucket/ap-mv/veo/jobs", reader, nil, nil, NewHistoryCache())

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
	repo := NewVideoHistoryRepository("gs://bucket/ap-mv/veo/jobs", reader, nil, fakeHistorySigner{}, NewHistoryCache())

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

func TestGetHistoryReusesRecipeLoadedByHistoryList(t *testing.T) {
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
	repo := NewVideoHistoryRepository("gs://bucket/ap-mv/veo/jobs", reader, nil, fakeHistorySigner{}, NewHistoryCache())

	if _, err := repo.ListHistoryPage(context.Background(), 1, 20); err != nil {
		t.Fatalf("ListHistoryPage() error = %v", err)
	}
	if _, err := repo.GetHistory(context.Background(), jobID); err != nil {
		t.Fatalf("GetHistory() error = %v", err)
	}
	if got := reader.OpenCount(metadataURI); got != 1 {
		t.Fatalf("metadata open count = %d, want 1", got)
	}
}

func TestGetHistoryReturnsErrorWhenRepositoryMisconfigured(t *testing.T) {
	t.Parallel()

	repo := NewVideoHistoryRepository("", nil, nil, nil, NewHistoryCache())

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
	repo := NewVideoHistoryRepository("gs://bucket/ap-mv/veo/jobs", reader, writer, nil, NewHistoryCache())

	if err := repo.DeleteHistory(context.Background(), "job-1"); err != nil {
		t.Fatalf("DeleteHistory() error = %v", err)
	}
	if len(writer.deleted) != 2 {
		t.Fatalf("deleted count = %d, want 2: %#v", len(writer.deleted), writer.deleted)
	}
}
