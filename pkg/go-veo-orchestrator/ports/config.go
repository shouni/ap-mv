package ports

import (
	"strings"
	"time"
)

// デフォルト値の定義
const (
	DefaultGeminiModel    = "gemini-3-flash-preview"
	DefaultImageModel     = "gemini-3-pro-image-preview"
	DefaultMaxConcurrency = 1
	DefaultStyleSuffix    = "Japanese anime style, official art, cel-shaded, clean line art, expressive eyes, cinematic lighting, consistent character design, high resolution"
)

// Config は Go Veo Orchestrator の各 Runner を動作させるための基本設定です。
type Config struct {
	// --- AI Model Settings (Common) ---
	GeminiModel string
	ImageModel  string

	// --- Generation Settings ---
	MaxConcurrency int
	RateInterval   time.Duration
	StyleSuffix    string

	// --- Timeout & Retries ---
	RequestTimeout time.Duration
}

// ApplyDefaults は未設定（ゼロ値）の項目にデフォルト値を適用します。
func (c *Config) ApplyDefaults() {
	if c.GeminiModel == "" {
		c.GeminiModel = DefaultGeminiModel
	}
	if c.ImageModel == "" {
		c.ImageModel = DefaultImageModel
	}
	if c.MaxConcurrency <= 0 {
		c.MaxConcurrency = DefaultMaxConcurrency
	}
	if c.StyleSuffix == "" {
		c.StyleSuffix = DefaultStyleSuffix
	}
}

// WithModels は、指定されたモデル名で上書きした Config のコピーを返します。
// 空文字は「変更なし」として扱い、返却前にデフォルト値を適用します。
func (c Config) WithModels(geminiModel, imageModel string) Config {
	if geminiModel = strings.TrimSpace(geminiModel); geminiModel != "" {
		c.GeminiModel = geminiModel
	}
	if imageModel = strings.TrimSpace(imageModel); imageModel != "" {
		c.ImageModel = imageModel
	}
	c.ApplyDefaults()
	return c
}

// UsesModels は、指定モデルを適用しても現在の Config と同じモデル構成かを返します。
func (c Config) UsesModels(geminiModel, imageModel string) bool {
	current := c
	current.ApplyDefaults()
	selected := c.WithModels(geminiModel, imageModel)
	return current.GeminiModel == selected.GeminiModel &&
		current.ImageModel == selected.ImageModel
}
