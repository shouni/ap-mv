package ports

import (
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
