// Package adapters は、Gemini/Vertex AI クライアントの初期化と、Veo動画生成APIの
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
	// defaultInitialDelay はリトライ時の初期待ち時間です。
	defaultInitialDelay = 60 * time.Second
	// defaultVertexLocationID はVertex AI のデフォルトロケーション
	defaultVertexLocationID = "global"
	// defaultVertexInitialDelay はリトライ遅延
	defaultVertexInitialDelay = 60 * time.Second
)

// NewGeminiAIAdapter は Google AI (Gemini API) クライアントを初期化します。
func NewGeminiAIAdapter(ctx context.Context, ai *config.Config) (*gemini.Client, error) {
	if ai.GeminiAPIKey == "" {
		return nil, fmt.Errorf("GEMINI_API_KEY が設定されていません")
	}

	clientConfig := gemini.Config{
		APIKey:       ai.GeminiAPIKey,
		InitialDelay: defaultInitialDelay,
	}

	aiClient, err := gemini.NewClient(ctx, clientConfig)
	if err != nil {
		return nil, fmt.Errorf("gemini API クライアントの初期化に失敗しました: %w", err)
	}

	return aiClient, nil
}

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
