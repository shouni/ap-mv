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

// TestGCSConsoleLink pins that gs:// URIs are turned into something Slack can actually open.
// Slack only auto-links http/https, so a bare gs:// line renders as dead text.
func TestGCSConsoleLink(t *testing.T) {
	cases := []struct {
		name string
		uri  string
		want string
	}{
		{
			name: "directory goes to the bucket browser",
			uri:  "gs://ap-mv/veo/drafts/video-draft-1/",
			want: "<https://console.cloud.google.com/storage/browser/ap-mv/veo/drafts/video-draft-1/|gs://ap-mv/veo/drafts/video-draft-1/>",
		},
		{
			name: "single object goes to its details page",
			uri:  "gs://ap-music/music/comp-1/recipe.json",
			want: "<https://console.cloud.google.com/storage/browser/_details/ap-music/music/comp-1/recipe.json|gs://ap-music/music/comp-1/recipe.json>",
		},
		{
			// http(s) は Slack が自前でリンク化するため触らない。
			name: "http URLs are left alone",
			uri:  "https://example.com/a.json",
			want: "https://example.com/a.json",
		},
		{name: "empty stays empty", uri: "", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := gcsConsoleLink(tc.uri); got != tc.want {
				t.Errorf("gcsConsoleLink(%q) = %q, want %q", tc.uri, got, tc.want)
			}
		})
	}
}

// TestBuildCompleteContentLinksGCSPaths checks the wiring: the notification's Output and Source
// lines carry the clickable form, and the gs:// URI stays visible for copy/paste.
func TestBuildCompleteContentLinksGCSPaths(t *testing.T) {
	adapter, err := NewSlackAdapter(nil, "", "https://ap-mv.example.com")
	if err != nil {
		t.Fatalf("NewSlackAdapter() error = %v", err)
	}

	got := adapter.buildCompleteContent(domain.NotificationRequest{
		JobID:     "video-draft-1",
		Command:   string(domain.CommandVideoRecipeDraft),
		OutputURI: "gs://ap-mv/veo/drafts/video-draft-1/",
		SourceURL: "gs://ap-music/music/comp-1/recipe.json",
	})

	if !strings.Contains(got, "console.cloud.google.com/storage/browser/ap-mv/veo/drafts/video-draft-1/") {
		t.Errorf("content = %q, want a console link for Output", got)
	}
	if !strings.Contains(got, "console.cloud.google.com/storage/browser/_details/ap-music/music/comp-1/recipe.json") {
		t.Errorf("content = %q, want a console details link for Source", got)
	}
	if !strings.Contains(got, "gs://ap-mv/veo/drafts/video-draft-1/") {
		t.Errorf("content = %q, want the gs:// URI kept as the visible label", got)
	}
}
