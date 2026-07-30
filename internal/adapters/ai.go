// Package adapters は、Vertex AI クライアントの初期化と、Veo動画生成APIの
// リクエスト/レスポンス変換を行うアダプター実装を提供します。
package adapters

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/shouni/go-gemini-client/gemini"

	"github.com/shouni/ap-mv/internal/config"
)

const (
	// defaultVertexLocationID はVertex AI のデフォルトロケーション
	defaultVertexLocationID = "global"
	// defaultVertexInitialDelay はリトライ遅延
	defaultVertexInitialDelay = 60 * time.Second
	// defaultVertexHTTPTimeout は Vertex AI への1回の HTTP 呼び出しに許す時間です。
	//
	// このクライアントは動画・テキスト・画像の全生成で共有されるため、最も遅い正常な
	// 呼び出し（画像生成は数十秒かかることがあり、http.Client.Timeout はレスポンス本文の
	// 読み切りまで含む）より十分長く取る必要があります。一方で上限が無いと、応答を
	// 返さない接続を Cloud Run のリクエスト上限（3600s）まで掴み続けることになります。
	// その間に取った値で、正常系には数倍の余裕があります。
	defaultVertexHTTPTimeout = 5 * time.Minute
)

// NewVertexAIAdapter は GCP Vertex AI クライアントを初期化します。
func NewVertexAIAdapter(ctx context.Context, ai *config.Config) (*gemini.Client, error) {
	if ai.GCP.ProjectID == "" {
		return nil, fmt.Errorf("GCP_PROJECT_ID が設定されていません")
	}

	clientConfig := gemini.Config{
		ProjectID:    ai.GCP.ProjectID,
		LocationID:   vertexLocationID(ai),
		InitialDelay: defaultVertexInitialDelay,
		HTTPClient:   &http.Client{Timeout: defaultVertexHTTPTimeout},
	}

	aiClient, err := gemini.NewClient(ctx, clientConfig)
	if err != nil {
		return nil, fmt.Errorf("vertex AI クライアントの初期化に失敗しました: %w", err)
	}

	return aiClient, nil
}

// vertexLocationID は、動画生成機能のロケーション解決ロジックと同じ優先順位で
// ロケーションを解決します（VEO_LOCATION_ID → GCP_LOCATION_ID）。どちらも未設定の
// 場合のみ defaultVertexLocationID にフォールバックします。テキスト/画像生成用の
// Vertex AI クライアントを、実際に動画生成で使うリージョンとなるべく揃えるためです。
func vertexLocationID(ai *config.Config) string {
	if locationID := strings.TrimSpace(ai.AI.VeoLocationID); locationID != "" {
		return locationID
	}
	if locationID := strings.TrimSpace(ai.GCP.LocationID); locationID != "" {
		return locationID
	}
	return defaultVertexLocationID
}
