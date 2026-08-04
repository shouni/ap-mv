package adapters

import (
	"strings"
	"testing"

	"github.com/shouni/ap-mv/internal/domain"
)

// TestBuildCompleteContentLinksDraftsForDraftJobs pins that a draft job's notification points at
// the drafts list. Drafts are stored under their own prefix and write no video_music_meta.json,
// so the History Detail page has nothing to load for them — linking there hands the user a dead
// link at the exact moment they want to look at the result.
func TestBuildCompleteContentLinksDraftsForDraftJobs(t *testing.T) {
	adapter, err := NewSlackAdapter(nil, "", "https://ap-mv.example.com")
	if err != nil {
		t.Fatalf("NewSlackAdapter() error = %v", err)
	}

	got := adapter.buildCompleteContent(domain.NotificationRequest{
		JobID:   "video-draft-20260804-101112-abc",
		Command: string(domain.CommandVideoRecipeDraft),
		Title:   "draft test",
	})

	if !strings.Contains(got, "https://ap-mv.example.com/web/drafts") {
		t.Errorf("content = %q, want a link to the drafts list", got)
	}
	if strings.Contains(got, "/web/history/") {
		t.Errorf("content = %q, want no History Detail link for a draft job", got)
	}
}

// TestBuildCompleteContentLinksHistoryForNormalJobs pins the unchanged behaviour for every other
// command, including the regenerate case where the result lands in the original job.
func TestBuildCompleteContentLinksHistoryForNormalJobs(t *testing.T) {
	adapter, err := NewSlackAdapter(nil, "", "https://ap-mv.example.com")
	if err != nil {
		t.Fatalf("NewSlackAdapter() error = %v", err)
	}

	t.Run("uses the job ID", func(t *testing.T) {
		got := adapter.buildCompleteContent(domain.NotificationRequest{
			JobID:   "video-recipe-20260804-101112-abc",
			Command: string(domain.CommandVideoRecipeCreate),
		})
		if !strings.Contains(got, "/web/history/video-recipe-20260804-101112-abc") {
			t.Errorf("content = %q, want the history detail link", got)
		}
	})

	t.Run("prefers HistoryJobID when the result was written back to another job", func(t *testing.T) {
		got := adapter.buildCompleteContent(domain.NotificationRequest{
			JobID:        "regen-keyframe-20260804-101112-abc",
			HistoryJobID: "video-recipe-20260618-081931-abc",
			Command:      string(domain.CommandRegenerateCutKeyframe),
		})
		if !strings.Contains(got, "/web/history/video-recipe-20260618-081931-abc") {
			t.Errorf("content = %q, want the original job's history detail link", got)
		}
	})
}
