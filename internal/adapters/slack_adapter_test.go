package adapters

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/shouni/go-notify/notify"

	"github.com/shouni/ap-mv/internal/domain"
)

// recordingNotifier records the notify.Message values it is handed. Rendering to
// Slack mrkdwn belongs to go-notify, so these tests only pin the title and body
// that ap-mv itself assembles.
type recordingNotifier struct {
	got []notify.Message
}

// Notify implements notify.Notifier.
func (r *recordingNotifier) Notify(_ context.Context, msg notify.Message) error {
	r.got = append(r.got, msg)
	return nil
}

// last returns the most recently sent message.
func (r *recordingNotifier) last(t *testing.T) notify.Message {
	t.Helper()
	if len(r.got) == 0 {
		t.Fatal("no notification was sent")
	}
	return r.got[len(r.got)-1]
}

// newTestSlackAdapter returns an adapter wired to a recording notifier.
func newTestSlackAdapter(serviceURL string) (*SlackAdapter, *recordingNotifier) {
	rec := &recordingNotifier{}
	return &SlackAdapter{
		pipeline:   notify.NewPipeline(rec, slackTitles),
		serviceURL: serviceURL,
	}, rec
}

// TestBuildCompleteContentLinksDraftsForDraftJobs pins that a draft job's notification points at
// the drafts list. Drafts are stored under their own prefix and write no video_music_meta.json,
// so the History Detail page has nothing to load for them — linking there hands the user a dead
// link at the exact moment they want to look at the result.
func TestBuildCompleteContentLinksDraftsForDraftJobs(t *testing.T) {
	adapter, _ := newTestSlackAdapter("https://ap-mv.example.com")

	got := adapter.buildCompleteContent(domain.NotificationRequest{
		JobID:   "video-draft-20260804-101112-abc",
		Command: string(domain.CommandVideoRecipeDraft),
		Title:   "draft test",
	}).String()

	if !strings.Contains(got, "https://ap-mv.example.com/drafts") {
		t.Errorf("content = %q, want a link to the drafts list", got)
	}
	if strings.Contains(got, "/history/") {
		t.Errorf("content = %q, want no History Detail link for a draft job", got)
	}
}

// TestBuildCompleteContentLinksHistoryForNormalJobs pins the unchanged behaviour for every other
// command, including the regenerate case where the result lands in the original job.
func TestBuildCompleteContentLinksHistoryForNormalJobs(t *testing.T) {
	adapter, _ := newTestSlackAdapter("https://ap-mv.example.com")

	t.Run("uses the job ID", func(t *testing.T) {
		got := adapter.buildCompleteContent(domain.NotificationRequest{
			JobID:   "video-recipe-20260804-101112-abc",
			Command: string(domain.CommandVideoRecipeCreate),
		}).String()
		if !strings.Contains(got, "/history/video-recipe-20260804-101112-abc") {
			t.Errorf("content = %q, want the history detail link", got)
		}
	})

	t.Run("prefers HistoryJobID when the result was written back to another job", func(t *testing.T) {
		got := adapter.buildCompleteContent(domain.NotificationRequest{
			JobID:        "regen-keyframe-20260804-101112-abc",
			HistoryJobID: "video-recipe-20260618-081931-abc",
			Command:      string(domain.CommandRegenerateCutKeyframe),
		}).String()
		if !strings.Contains(got, "/history/video-recipe-20260618-081931-abc") {
			t.Errorf("content = %q, want the original job's history detail link", got)
		}
	})
}

// The gs:// URI → Cloud Console URL mapping and the URI-line shape moved to
// notify.Body.URIField; go-notify's own tests pin those rules. The wiring test
// below keeps guarding that this adapter routes URIs through it.

// TestBuildCompleteContentLinksGCSPaths checks the wiring: the notification's Output and Source
// lines carry the clickable form, and the gs:// URI stays visible for copy/paste.
func TestBuildCompleteContentLinksGCSPaths(t *testing.T) {
	adapter, _ := newTestSlackAdapter("https://ap-mv.example.com")

	got := adapter.buildCompleteContent(domain.NotificationRequest{
		JobID:     "video-draft-1",
		Command:   string(domain.CommandVideoRecipeDraft),
		OutputURI: "gs://ap-mv/veo/drafts/video-draft-1/",
		SourceURL: "gs://ap-music/music/comp-1/recipe.json",
	}).String()

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

// TestBuildCompleteContentOmitsZeroCutCount pins that a cut count of zero writes no
// line, rather than a misleading "Cuts: 0".
func TestBuildCompleteContentOmitsZeroCutCount(t *testing.T) {
	adapter, _ := newTestSlackAdapter("https://ap-mv.example.com")

	got := adapter.buildCompleteContent(domain.NotificationRequest{
		JobID:   "video-recipe-1",
		Command: string(domain.CommandVideoRecipeCreate),
	}).String()
	if strings.Contains(got, "Cuts") {
		t.Errorf("content = %q, want no Cuts line when the count is zero", got)
	}

	got = adapter.buildCompleteContent(domain.NotificationRequest{
		JobID:    "video-recipe-1",
		Command:  string(domain.CommandVideoRecipeCreate),
		CutCount: 12,
	}).String()
	if !strings.Contains(got, "**Cuts:** `12`") {
		t.Errorf("content = %q, want the cut count", got)
	}
}

// TestNotifyTaskCompleteSendsSuccessTitle pins the completion title.
func TestNotifyTaskCompleteSendsSuccessTitle(t *testing.T) {
	adapter, rec := newTestSlackAdapter("https://ap-mv.example.com")

	req := domain.NotificationRequest{JobID: "video-recipe-1", Command: string(domain.CommandVideoRecipeCreate)}
	if err := adapter.NotifyTaskComplete(context.Background(), req); err != nil {
		t.Fatalf("NotifyTaskComplete() error = %v", err)
	}

	if msg := rec.last(t); msg.Title != slackTitles.Success {
		t.Errorf("Title = %q, want %q", msg.Title, slackTitles.Success)
	}
}

// TestNotifyTaskErrorAppendsCause pins that the failure notification carries the cause
// and the request metadata, but no artifact links (nothing was published).
func TestNotifyTaskErrorAppendsCause(t *testing.T) {
	adapter, rec := newTestSlackAdapter("https://ap-mv.example.com")

	req := domain.NotificationRequest{
		JobID:     "video-recipe-1",
		Command:   string(domain.CommandVideoRecipeCreate),
		SourceURL: "gs://ap-music/music/comp-1/recipe.json",
	}
	if err := adapter.NotifyTaskError(context.Background(), errors.New("Veo timed out"), req); err != nil {
		t.Fatalf("NotifyTaskError() error = %v", err)
	}

	msg := rec.last(t)
	if msg.Title != slackTitles.Failure {
		t.Errorf("Title = %q, want %q", msg.Title, slackTitles.Failure)
	}
	if !strings.Contains(msg.Body, "**エラー内容:**\nVeo timed out") {
		t.Errorf("Body = %q, want the cause", msg.Body)
	}
	if !strings.Contains(msg.Body, "**Job ID:** `video-recipe-1`") {
		t.Errorf("Body = %q, want the request metadata", msg.Body)
	}
	if strings.Contains(msg.Body, "History Detail") {
		t.Errorf("Body = %q, want no result link on failure", msg.Body)
	}
}

// TestNotifyTaskErrorWithNilCause pins that a nil cause does not break the notification.
func TestNotifyTaskErrorWithNilCause(t *testing.T) {
	adapter, rec := newTestSlackAdapter("")

	if err := adapter.NotifyTaskError(context.Background(), nil, domain.NotificationRequest{JobID: "job-1"}); err != nil {
		t.Fatalf("NotifyTaskError() error = %v", err)
	}

	if body := rec.last(t).Body; !strings.Contains(body, "**エラー内容:**\n"+notify.NotAvailable) {
		t.Errorf("Body = %q, want the N/A fallback", body)
	}
}

// TestNewSlackAdapterDisabledWhenWebhookURLEmpty pins that a missing webhook URL disables
// notifications instead of failing construction.
func TestNewSlackAdapterDisabledWhenWebhookURLEmpty(t *testing.T) {
	adapter, err := NewSlackAdapter(nil, "", "https://ap-mv.example.com")
	if err != nil {
		t.Fatalf("NewSlackAdapter() error = %v", err)
	}
	if adapter.pipeline.Enabled() {
		t.Fatal("notifications are enabled despite an empty webhook URL")
	}

	ctx := context.Background()
	req := domain.NotificationRequest{JobID: "job-1"}
	if err := adapter.NotifyTaskComplete(ctx, req); err != nil {
		t.Errorf("NotifyTaskComplete() = %v, want nil", err)
	}
	if err := adapter.NotifyTaskError(ctx, errors.New("boom"), req); err != nil {
		t.Errorf("NotifyTaskError() = %v, want nil", err)
	}
}

// TestNewSlackAdapterRequiresHTTPClientWhenWebhookSet pins that a configured webhook with
// no HTTP client is a configuration error rather than a silent no-op.
func TestNewSlackAdapterRequiresHTTPClientWhenWebhookSet(t *testing.T) {
	if _, err := NewSlackAdapter(nil, "https://hooks.slack.example/webhook", ""); err == nil {
		t.Fatal("expected an error when the HTTP client is nil but a webhook URL is set")
	}
}

// TestNotifySetsLevel pins the outcome level on both notification paths.
// Slack turns it into a coloured attachment bar, so it carries information the
// heading's emoji cannot.
func TestNotifySetsLevel(t *testing.T) {
	req := domain.NotificationRequest{Command: "video_recipe", JobID: "job-1"}

	t.Run("complete is success", func(t *testing.T) {
		adapter, rec := newTestSlackAdapter("https://example.com")

		if err := adapter.NotifyTaskComplete(context.Background(), req); err != nil {
			t.Fatalf("NotifyTaskComplete failed: %v", err)
		}
		if got := rec.last(t).Level; got != notify.LevelSuccess {
			t.Errorf("Level = %v, want %v", got, notify.LevelSuccess)
		}
	})

	t.Run("error is failure", func(t *testing.T) {
		adapter, rec := newTestSlackAdapter("https://example.com")

		if err := adapter.NotifyTaskError(context.Background(), errors.New("boom"), req); err != nil {
			t.Fatalf("NotifyTaskError failed: %v", err)
		}
		if got := rec.last(t).Level; got != notify.LevelFailure {
			t.Errorf("Level = %v, want %v", got, notify.LevelFailure)
		}
	})
}
