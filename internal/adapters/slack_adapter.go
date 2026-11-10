package adapters

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/shouni/go-http-kit/httpkit"
	"github.com/shouni/go-notify/notify"
	"github.com/shouni/go-notify/slack"

	"github.com/shouni/ap-mv/internal/domain"
)

// slackTitles are the headings per pipeline result.
// Skip notices are never sent, so Skipped is not set.
var slackTitles = notify.Titles{
	Success: "✅ AP MV 処理が完了しました",
	Failure: "❌ AP MV 処理中にエラーが発生しました",
}

// SlackAdapter posts asynchronous pipeline notifications through Slack Incoming Webhook.
type SlackAdapter struct {
	pipeline   *notify.Pipeline
	serviceURL string
}

// NewSlackAdapter creates a Slack notification adapter. Empty webhook URL disables notifications.
func NewSlackAdapter(httpClient httpkit.Poster, webhookURL, serviceURL string) (*SlackAdapter, error) {
	notifier, err := slack.NewNotifier(httpClient, webhookURL)
	if err != nil {
		return nil, fmt.Errorf("initialize Slack client: %w", err)
	}

	return &SlackAdapter{
		pipeline:   notify.NewPipeline(notifier, slackTitles),
		serviceURL: serviceURL,
	}, nil
}

// NotifyTaskComplete posts a completion notification.
func (s *SlackAdapter) NotifyTaskComplete(ctx context.Context, req domain.NotificationRequest) error {
	if !s.pipeline.Enabled() {
		slog.InfoContext(ctx, "Slack notification skipped", "job_id", req.JobID)
		return nil
	}

	if err := s.pipeline.Success(ctx, s.buildCompleteContent(req)); err != nil {
		return fmt.Errorf("post Slack completion notification: %w", err)
	}

	slog.InfoContext(ctx, "Slack completion notification sent", "job_id", req.JobID)
	return nil
}

// NotifyTaskError posts an error notification.
func (s *SlackAdapter) NotifyTaskError(ctx context.Context, errDetail error, req domain.NotificationRequest) error {
	if !s.pipeline.Enabled() {
		slog.InfoContext(ctx, "Slack error notification skipped", "job_id", req.JobID, "error", errDetail)
		return nil
	}

	body := notify.NewBody()
	writeSlackRequestSummary(body, req)
	writeSlackRequestGenerationMetadata(body, req)
	writeSlackRequestSource(body, req)

	if err := s.pipeline.Failure(ctx, body, errDetail); err != nil {
		return fmt.Errorf("post Slack error notification: %w", err)
	}

	slog.InfoContext(ctx, "Slack error notification sent", "job_id", req.JobID, "error", errDetail)
	return nil
}

// buildCompleteContent builds the body of the completion notice.
func (s *SlackAdapter) buildCompleteContent(req domain.NotificationRequest) *notify.Body {
	body := notify.NewBody()

	writeSlackRequestSummary(body, req)

	// Script-only jobs (video_recipe_draft) write the same video_music_meta.json as finished jobs, so
	// they go to the same history detail. They used to go to a dedicated /drafts listing, but that
	// route disappeared when drafts stopped using a separate prefix and became ordinary jobs, and only
	// script-only jobs' completion notices were left with a broken link.
	historyJobID := req.HistoryJobID
	if historyJobID == "" {
		historyJobID = req.JobID
	}
	body.Link("History Detail", s.historyDetailURL(historyJobID), historyJobID)

	writeSlackRequestGenerationMetadata(body, req)

	if req.CutCount > 0 {
		body.Code("Cuts", strconv.Itoa(req.CutCount))
	}
	// gs:// is linked to the Cloud Console as displayed (notify.Body.URIField).
	body.URIField("Output", req.OutputURI)

	writeSlackRequestSource(body, req)

	return body
}

// writeSlackRequestSummary appends the job's identifying information.
func writeSlackRequestSummary(body *notify.Body, req domain.NotificationRequest) {
	body.Code("Job ID", req.JobID).
		Code("Command", req.Command).
		Field("Title", req.Title)
}

// writeSlackRequestGenerationMetadata appends the generation conditions.
func writeSlackRequestGenerationMetadata(body *notify.Body, req domain.NotificationRequest) {
	body.Code("Text Model", req.TextModel).
		Code("Image Model", req.ImageModel).
		Code("Visual Mode", req.VisualMode).
		Code("Character", req.CharacterID)
}

// writeSlackRequestSource appends the input source.
func writeSlackRequestSource(body *notify.Body, req domain.NotificationRequest) {
	body.URIField("Source", req.SourceURL).
		URIField("Recipe", req.RecipeURL).
		URIField("Audio", req.AudioURL)
}

// historyDetailURL returns the URL of the history detail page.
func (s *SlackAdapter) historyDetailURL(jobID string) string {
	// If serviceURL or jobID is empty it returns empty, and the notifier omits the whole line (JoinURL's contract).
	return notify.JoinURL(s.serviceURL, "/jobs", jobID)
}
