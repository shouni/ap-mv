// Package adapters は、Vertex AI クライアントの初期化と、Veo動画生成APIの
// リクエスト/レスポンス変換を行うアダプター実装を提供します。
package adapters

import (
	"context"
	"fmt"
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
		LocationID:   defaultVertexLocationID,
		InitialDelay: defaultVertexInitialDelay,
	}

	aiClient, err := gemini.NewClient(ctx, clientConfig)
	if err != nil {
		return nil, fmt.Errorf("vertex AI クライアントの初期化に失敗しました: %w", err)
	}

	return aiClient, nil
}
