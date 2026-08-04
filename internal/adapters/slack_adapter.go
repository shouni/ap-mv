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

	"github.com/shouni/ap-mv/internal/domain"
)

const (
	slackCompleteTitle      = "✅ AP MV 処理が完了しました"
	slackErrorTitle         = "❌ AP MV 処理中にエラーが発生しました"
	slackErrorContentHeader = "*エラー内容:*\n"
	slackNotAvailable       = "N/A"
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
	if s.webhookURL == "" || s.slackClient == nil {
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
	if s.webhookURL == "" || s.slackClient == nil {
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
		sb.WriteString(slackNotAvailable)
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
	// 下書き（video_recipe_draft）は video_music_meta.json を書かないため履歴に現れません。
	// 履歴詳細のリンクを出すと開いた先が 404/500 になるので、成果物が実際に並ぶ一覧へ誘導します。
	if req.Command == string(domain.CommandVideoRecipeDraft) {
		if draftsURL := s.draftsURL(); draftsURL != "" {
			fmt.Fprintf(&sb, "*Drafts:* <%s|%s>\n", draftsURL, req.JobID)
		}
	} else {
		historyJobID := req.HistoryJobID
		if historyJobID == "" {
			historyJobID = req.JobID
		}
		if historyURL := s.historyDetailURL(historyJobID); historyURL != "" {
			fmt.Fprintf(&sb, "*History Detail:* <%s|%s>\n", historyURL, historyJobID)
		}
	}
	writeSlackRequestGenerationMetadata(&sb, req)
	if req.CutCount > 0 {
		fmt.Fprintf(&sb, "*Cuts:* `%d`\n", req.CutCount)
	}
	if req.OutputURI != "" {
		fmt.Fprintf(&sb, "*Output:* %s\n", gcsConsoleLink(req.OutputURI))
	}
	writeSlackRequestSource(&sb, req)
	if sb.Len() == 0 {
		sb.WriteString(slackNotAvailable)
	}
	return sb.String()
}

// gcsConsoleLink は gs:// URI を Slack で開けるリンクへ変換します。
//
// Slack が自動リンク化するのは http/https だけで、gs:// はただの文字列として表示されます
// （押しても何も起きません）。ブラウザで開ける形は Cloud Console の URL なので、表示は
// gs:// のまま（コピーして gsutil などにそのまま渡せるように）、リンク先だけ Console に
// します。末尾がスラッシュのものはディレクトリ扱いでバケットブラウザへ、単体オブジェクトは
// 詳細ページへ飛ばします。
//
// gs:// 以外（http(s) や空文字）はそのまま返し、呼び出し側がそのまま出力します。
func gcsConsoleLink(uri string) string {
	uri = strings.TrimSpace(uri)
	const scheme = "gs://"
	if !strings.HasPrefix(uri, scheme) {
		return uri
	}
	objectPath := strings.TrimPrefix(uri, scheme)
	if objectPath == "" {
		return uri
	}
	consoleURL := "https://console.cloud.google.com/storage/browser/" + objectPath
	if !strings.HasSuffix(objectPath, "/") {
		consoleURL = "https://console.cloud.google.com/storage/browser/_details/" + objectPath
	}
	return fmt.Sprintf("<%s|%s>", consoleURL, uri)
}

func writeSlackRequestSummary(sb *strings.Builder, req domain.NotificationRequest) {
	if req.JobID != "" {
		fmt.Fprintf(sb, "*Job ID:* `%s`\n", req.JobID)
	}
	if req.Command != "" {
		fmt.Fprintf(sb, "*Command:* `%s`\n", req.Command)
	}
	if req.Title != "" {
		fmt.Fprintf(sb, "*Title:* %s\n", req.Title)
	}
}

func writeSlackRequestGenerationMetadata(sb *strings.Builder, req domain.NotificationRequest) {
	if req.TextModel != "" {
		fmt.Fprintf(sb, "*Text Model:* `%s`\n", req.TextModel)
	}
	if req.ImageModel != "" {
		fmt.Fprintf(sb, "*Image Model:* `%s`\n", req.ImageModel)
	}
	if req.VisualMode != "" {
		fmt.Fprintf(sb, "*Visual Mode:* `%s`\n", req.VisualMode)
	}
	if req.CharacterID != "" {
		fmt.Fprintf(sb, "*Character:* `%s`\n", req.CharacterID)
	}
}

func writeSlackRequestSource(sb *strings.Builder, req domain.NotificationRequest) {
	if req.SourceURL != "" {
		fmt.Fprintf(sb, "*Source:* %s\n", gcsConsoleLink(req.SourceURL))
	}
	if req.RecipeURL != "" {
		fmt.Fprintf(sb, "*Recipe:* %s\n", gcsConsoleLink(req.RecipeURL))
	}
	if req.AudioURL != "" {
		fmt.Fprintf(sb, "*Audio:* %s\n", gcsConsoleLink(req.AudioURL))
	}
}

// draftsURL は下書き一覧のURLを返します。下書きには専用の詳細画面が無いため
// （JSON は ap-mcp 用、ブラウザは一覧へリダイレクト）、リンク先は一覧そのものです。
func (s *SlackAdapter) draftsURL() string {
	if s.serviceURL == "" {
		return ""
	}
	draftsURL, err := url.JoinPath(s.serviceURL, "/web/drafts")
	if err != nil {
		return ""
	}
	return draftsURL
}

func (s *SlackAdapter) historyDetailURL(jobID string) string {
	if s.serviceURL == "" || jobID == "" {
		return ""
	}
	historyURL, err := url.JoinPath(s.serviceURL, "/web/history", jobID)
	if err != nil {
		return ""
	}
	return historyURL
}
