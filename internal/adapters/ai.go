// Package adapters は、Vertex AI クライアントの初期化と、Veo動画生成APIの
// リクエスト/レスポンス変換を行うアダプター実装を提供します。
package adapters

import (
	"context"
	"fmt"
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
)

// NewVertexAIAdapter は GCP Vertex AI クライアントを初期化します。
func NewVertexAIAdapter(ctx context.Context, ai *config.Config) (*gemini.Client, error) {
	if ai.ProjectID == "" {
		return nil, fmt.Errorf("GCP_PROJECT_ID が設定されていません")
	}

	clientConfig := gemini.Config{
		ProjectID:    ai.ProjectID,
		LocationID:   vertexLocationID(ai),
		InitialDelay: defaultVertexInitialDelay,
	}

	aiClient, err := gemini.NewClient(ctx, clientConfig)
	if err != nil {
		return nil, fmt.Errorf("vertex AI クライアントの初期化に失敗しました: %w", err)
	}

	return aiClient, nil
}

// vertexLocationID は、Veo（vertex_veo.go の NewVertexVeoRunner）と同じ優先順位で
// ロケーションを解決します（VEO_LOCATION_ID → GCP_LOCATION_ID）。どちらも未設定の
// 場合のみ defaultVertexLocationID にフォールバックします。テキスト/画像生成用の
// Vertex AI クライアントを、実際に動画生成で使うリージョンとなるべく揃えるためです。
func vertexLocationID(ai *config.Config) string {
	if locationID := strings.TrimSpace(ai.VeoLocationID); locationID != "" {
		return locationID
	}
	if locationID := strings.TrimSpace(ai.LocationID); locationID != "" {
		return locationID
	}
	return defaultVertexLocationID
}
