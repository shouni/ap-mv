package adapters

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"

	"github.com/shouni/go-http-kit/httpkit"
	"github.com/shouni/go-notify/notify"
	"github.com/shouni/go-notify/slack"

	"github.com/shouni/ap-mv/internal/domain"
)

// slackTitles はパイプラインの結果ごとの見出しです。
// スキップ通知は行わないため Skipped は設定していません。
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
func NewSlackAdapter(httpClient httpkit.Requester, webhookURL, serviceURL string) (*SlackAdapter, error) {
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

// buildCompleteContent は完了通知の本文を組み立てます。
func (s *SlackAdapter) buildCompleteContent(req domain.NotificationRequest) *notify.Body {
	body := notify.NewBody()

	writeSlackRequestSummary(body, req)

	// 下書き（video_recipe_draft）は video_music_meta.json を書かないため履歴に現れません。
	// 履歴詳細のリンクを出すと開いた先が 404/500 になるので、成果物が実際に並ぶ一覧へ誘導します。
	if req.Command == string(domain.CommandVideoRecipeDraft) {
		body.Link("Drafts", s.draftsURL(), req.JobID)
	} else {
		historyJobID := req.HistoryJobID
		if historyJobID == "" {
			historyJobID = req.JobID
		}
		body.Link("History Detail", s.historyDetailURL(historyJobID), historyJobID)
	}

	writeSlackRequestGenerationMetadata(body, req)

	if req.CutCount > 0 {
		body.Code("Cuts", strconv.Itoa(req.CutCount))
	}
	writeURIField(body, "Output", req.OutputURI)

	writeSlackRequestSource(body, req)

	return body
}

// gcsConsoleURL は gs:// URI に対応する Cloud Console の URL を返します。
// gs:// 以外（http(s) や空文字）の場合は空文字を返します。
//
// 末尾がスラッシュのものはディレクトリ扱いでバケットブラウザへ、
// 単体オブジェクトは詳細ページへ飛ばします。
func gcsConsoleURL(uri string) string {
	const scheme = "gs://"

	objectPath, ok := strings.CutPrefix(strings.TrimSpace(uri), scheme)
	if !ok || objectPath == "" {
		return ""
	}

	if strings.HasSuffix(objectPath, "/") {
		return "https://console.cloud.google.com/storage/browser/" + objectPath
	}
	return "https://console.cloud.google.com/storage/browser/_details/" + objectPath
}

// writeURIField は URI の行を追記します。URI が空の場合は何もしません。
//
// gs:// の場合は Cloud Console へのリンクにし、表示は gs:// のまま残します。
// Slack が自動リンク化するのは http/https だけで、gs:// はただの文字列として
// 表示されます（押しても何も起きません）。一方で表示まで Console の URL に
// してしまうと、コピーして gsutil などにそのまま渡せなくなります。
func writeURIField(body *notify.Body, label, uri string) {
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return
	}

	if consoleURL := gcsConsoleURL(uri); consoleURL != "" {
		body.Link(label, consoleURL, uri)
		return
	}
	body.Field(label, uri)
}

// writeSlackRequestSummary はジョブの識別情報を追記します。
func writeSlackRequestSummary(body *notify.Body, req domain.NotificationRequest) {
	body.Code("Job ID", req.JobID).
		Code("Command", req.Command).
		Field("Title", req.Title)
}

// writeSlackRequestGenerationMetadata は生成条件を追記します。
func writeSlackRequestGenerationMetadata(body *notify.Body, req domain.NotificationRequest) {
	body.Code("Text Model", req.TextModel).
		Code("Image Model", req.ImageModel).
		Code("Visual Mode", req.VisualMode).
		Code("Character", req.CharacterID)
}

// writeSlackRequestSource は入力ソースを追記します。
func writeSlackRequestSource(body *notify.Body, req domain.NotificationRequest) {
	writeURIField(body, "Source", req.SourceURL)
	writeURIField(body, "Recipe", req.RecipeURL)
	writeURIField(body, "Audio", req.AudioURL)
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

// historyDetailURL は履歴詳細ページのURLを返します。
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
