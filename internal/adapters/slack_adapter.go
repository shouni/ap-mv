package adapters

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"

	"github.com/shouni/go-http-kit/httpkit"
	"github.com/shouni/go-notifier/pkg/slack"

	"ap-mv/internal/domain"
)

const (
	slackCompleteTitle      = "✅ AP MV 処理が完了しました"
	slackErrorTitle         = "❌ AP MV 処理中にエラーが発生しました"
	slackErrorContentHeader = "*エラー内容:*\n"
)

// SlackAdapter posts asynchronous pipeline notifications through Slack Incoming Webhook.
type SlackAdapter struct {
	webhookURL  string
	serviceURL  string
	slackClient *slack.Client
}

// NewSlackAdapter creates a Slack notification adapter. Empty webhook URL disables notifications.
func NewSlackAdapter(httpClient httpkit.Requester, webhookURL, serviceURL string) (*SlackAdapter, error) {
	if strings.TrimSpace(webhookURL) == "" {
		return &SlackAdapter{serviceURL: serviceURL}, nil
	}
	if httpClient == nil {
		return nil, errors.New("HTTP client is nil")
	}
	client, err := slack.NewClient(httpClient, webhookURL)
	if err != nil {
		return nil, fmt.Errorf("initialize Slack client: %w", err)
	}
	return &SlackAdapter{
		webhookURL:  webhookURL,
		serviceURL:  serviceURL,
		slackClient: client,
	}, nil
}

// NotifyTaskComplete posts a completion notification.
func (s *SlackAdapter) NotifyTaskComplete(ctx context.Context, req domain.NotificationRequest) error {
	if s == nil || s.webhookURL == "" || s.slackClient == nil {
		slog.InfoContext(ctx, "Slack notification skipped", "job_id", req.JobID)
		return nil
	}
	if err := s.slackClient.SendTextWithHeader(ctx, slackCompleteTitle, s.buildCompleteContent(req)); err != nil {
		return fmt.Errorf("post Slack completion notification: %w", err)
	}
	slog.InfoContext(ctx, "Slack completion notification sent", "job_id", req.JobID)
	return nil
}

// NotifyTaskError posts an error notification.
func (s *SlackAdapter) NotifyTaskError(ctx context.Context, errDetail error, req domain.NotificationRequest) error {
	if s == nil || s.webhookURL == "" || s.slackClient == nil {
		slog.InfoContext(ctx, "Slack error notification skipped", "job_id", req.JobID, "error", errDetail)
		return nil
	}
	var sb strings.Builder
	writeSlackRequestSummary(&sb, req)
	writeSlackRequestGenerationMetadata(&sb, req)
	writeSlackRequestSource(&sb, req)
	if sb.Len() > 0 {
		sb.WriteString("\n")
	}
	sb.WriteString(slackErrorContentHeader)
	if errDetail != nil {
		sb.WriteString(errDetail.Error())
	} else {
		sb.WriteString(domain.NotAvailable)
	}
	if err := s.slackClient.SendTextWithHeader(ctx, slackErrorTitle, sb.String()); err != nil {
		return fmt.Errorf("post Slack error notification: %w", err)
	}
	slog.InfoContext(ctx, "Slack error notification sent", "job_id", req.JobID, "error", errDetail)
	return nil
}

func (s *SlackAdapter) buildCompleteContent(req domain.NotificationRequest) string {
	var sb strings.Builder
	writeSlackRequestSummary(&sb, req)
	if historyURL := s.historyDetailURL(req.JobID); historyURL != "" {
		sb.WriteString(fmt.Sprintf("*History Detail:* <%s|%s>\n", historyURL, req.JobID))
	}
	writeSlackRequestGenerationMetadata(&sb, req)
	if req.CutCount > 0 {
		sb.WriteString(fmt.Sprintf("*Cuts:* `%d`\n", req.CutCount))
	}
	if req.OutputURI != "" {
		sb.WriteString(fmt.Sprintf("*Output:* %s\n", req.OutputURI))
	}
	writeSlackRequestSource(&sb, req)
	if sb.Len() == 0 {
		sb.WriteString(domain.NotAvailable)
	}
	return sb.String()
}

func writeSlackRequestSummary(sb *strings.Builder, req domain.NotificationRequest) {
	if req.JobID != "" {
		sb.WriteString(fmt.Sprintf("*Job ID:* `%s`\n", req.JobID))
	}
	if req.Command != "" {
		sb.WriteString(fmt.Sprintf("*Command:* `%s`\n", req.Command))
	}
	if req.Title != "" {
		sb.WriteString(fmt.Sprintf("*Title:* %s\n", req.Title))
	}
}

func writeSlackRequestGenerationMetadata(sb *strings.Builder, req domain.NotificationRequest) {
	if req.TextModel != "" {
		sb.WriteString(fmt.Sprintf("*Text Model:* `%s`\n", req.TextModel))
	}
	if req.ImageModel != "" {
		sb.WriteString(fmt.Sprintf("*Image Model:* `%s`\n", req.ImageModel))
	}
	if req.VisualMode != "" {
		sb.WriteString(fmt.Sprintf("*Visual Mode:* `%s`\n", req.VisualMode))
	}
	if req.CharacterID != "" {
		sb.WriteString(fmt.Sprintf("*Character:* `%s`\n", req.CharacterID))
	}
}

func writeSlackRequestSource(sb *strings.Builder, req domain.NotificationRequest) {
	if req.SourceURL != "" {
		sb.WriteString(fmt.Sprintf("*Source:* %s\n", req.SourceURL))
	}
	if req.RecipeURL != "" {
		sb.WriteString(fmt.Sprintf("*Recipe:* %s\n", req.RecipeURL))
	}
	if req.AudioURL != "" {
		sb.WriteString(fmt.Sprintf("*Audio:* %s\n", req.AudioURL))
	}
}

func (s *SlackAdapter) historyDetailURL(jobID string) string {
	if s == nil || s.serviceURL == "" || jobID == "" {
		return ""
	}
	historyURL, err := url.JoinPath(s.serviceURL, "/web/history", jobID)
	if err != nil {
		return ""
	}
	return historyURL
}
