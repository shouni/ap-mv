package repository

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/shouni/go-remote-io/remoteio"
)

type fakeHistoryReader struct {
	paths []string
	files map[string]string
}

func (r *fakeHistoryReader) Open(_ context.Context, path string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(r.files[path])), nil
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
