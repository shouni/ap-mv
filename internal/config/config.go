// Package config は、環境変数からアプリケーション設定を読み込み・正規化します。
package config

import (
	"strings"
	"time"

	"github.com/caarlos0/env/v11"

	"github.com/shouni/ap-mv/internal/domain"
)

const (
	defaultGeminiModel = "gemini-3.5-flash"
	defaultImageModel  = "gemini-3-pro-image-preview"
	defaultVeoModel    = "veo-3.1-generate-001"
	taskGeneratePath   = "/tasks/generate"
)

// Config はアプリ設定です。
type Config struct {
	ServiceURL          string `env:"SERVICE_URL" envDefault:"http://localhost:8080"`
	Port                string `env:"PORT" envDefault:"8080"`
	ProjectID           string `env:"GCP_PROJECT_ID"`
	LocationID          string `env:"GCP_LOCATION_ID"`
	QueueID             string `env:"CLOUD_TASKS_QUEUE_ID"`
	WorkerURL           string `env:"WORKER_URL"`
	TaskAudienceURL     string `env:"TASK_AUDIENCE_URL"`
	ServiceAccountEmail string `env:"SERVICE_ACCOUNT_EMAIL"`
	GCSBucket           string `env:"AP_MV_BUCKET"`
	// MusicBucket は、Video Recipe Create の Music Job ID からレシピJSON
	// （gs://<MusicBucket>/<jobID>.json、ap-comp/lyric-videoと同じ規則）を解決するために使う。
	MusicBucket            string        `env:"AP_MUSIC_BUCKET" envDefault:"ap-music"`
	SlackWebhookURL        string        `env:"SLACK_WEBHOOK_URL"`
	GeminiModel            string        `env:"GEMINI_MODEL"`
	ImageModel             string        `env:"IMAGE_MODEL"`
	GeminiModels           []string      `env:"GEMINI_MODELS" envDefault:"gemini-3.5-flash"`
	ImageModels            []string      `env:"IMAGE_MODELS" envDefault:"gemini-3.1-flash-image"`
	VeoModel               string        `env:"VEO_MODEL" envDefault:"veo-3.1-generate-001"`
	VeoModels              []string      `env:"VEO_MODELS" envDefault:"veo-3.1-generate-001"`
	VeoLocationID          string        `env:"VEO_LOCATION_ID"`
	VeoOutputPrefix        string        `env:"VEO_OUTPUT_PREFIX" envDefault:"github.com/shouni/ap-mv/veo"`
	VeoAspectRatio         string        `env:"VEO_ASPECT_RATIO" envDefault:"16:9"`
	VeoGenerateAudio       bool          `env:"VEO_GENERATE_AUDIO" envDefault:"false"`
	VeoPollInterval        time.Duration `env:"VEO_POLL_INTERVAL" envDefault:"10s"`
	VeoOperationTimeout    time.Duration `env:"VEO_OPERATION_TIMEOUT" envDefault:"20m"`
	VeoPollMaxErrors       int           `env:"VEO_POLL_MAX_ERRORS" envDefault:"10"`
	VeoUsePreviousVideo    bool          `env:"VEO_USE_PREVIOUS_VIDEO" envDefault:"false"`
	KeyframeMaxConcurrency int           `env:"KEYFRAME_MAX_CONCURRENCY" envDefault:"1"`
	KeyframeRateInterval   time.Duration `env:"KEYFRAME_RATE_INTERVAL" envDefault:"60s"`
	ShutdownTimeout        time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"15s"`

	// OAuth & Session Settings
	GoogleClientID     string `env:"GOOGLE_CLIENT_ID"`
	GoogleClientSecret string `env:"GOOGLE_CLIENT_SECRET"`
	// SessionSecret はセッションデータのHMAC署名用シークレットキーです。
	SessionSecret string `env:"SESSION_SECRET"`
	// SessionEncryptKey はセッションデータのAES暗号化用シークレットキーです。 16, 24, 32 バイトのいずれかである必要があります。
	SessionEncryptKey string `env:"SESSION_ENCRYPT_KEY"`

	// Authz Settings
	AllowedEmails  []string `env:"ALLOWED_EMAILS"`
	AllowedDomains []string `env:"ALLOWED_DOMAINS"`
	// AllowedM2MServiceAccounts は、Web APIをサーバー間通信（OIDC Bearerトークン）で
	// 呼び出せるサービスアカウントのメールアドレスです。空ならM2M認証は無効です。
	AllowedM2MServiceAccounts []string `env:"ALLOWED_M2M_SERVICE_ACCOUNTS"`
}

// normalize normalizes the provided values.
func (c *Config) normalize() error {
	workerURL, err := normalizeWorkerURL(c.ServiceURL, c.WorkerURL)
	if err != nil {
		return err
	}
	c.WorkerURL = workerURL
	if c.TaskAudienceURL == "" {
		c.TaskAudienceURL = c.ServiceURL
	}
	c.GCSBucket = normalizeGCSBucket(c.GCSBucket)
	c.MusicBucket = normalizeGCSBucket(c.MusicBucket)
	c.AllowedEmails = normalizeStringSlice(c.AllowedEmails)
	c.AllowedDomains = normalizeStringSlice(c.AllowedDomains)
	c.AllowedM2MServiceAccounts = normalizeStringSlice(c.AllowedM2MServiceAccounts)
	// Veo は提供リージョンが限られる（例: us-central1）ため、Cloud Tasks 等と共有する
	// GCP_LOCATION_ID とは別に VEO_LOCATION_ID で上書きできる。未設定なら共通値を使う。
	if strings.TrimSpace(c.VeoLocationID) == "" {
		c.VeoLocationID = c.LocationID
	}
	c.NormalizeModels()
	return nil
}

// NormalizeModels normalizes configured model lists and defaults.
func (c *Config) NormalizeModels() {
	c.GeminiModels = domain.NormalizeModelList(c.GeminiModels, c.GeminiModel, defaultGeminiModel)
	c.ImageModels = domain.NormalizeModelList(c.ImageModels, c.ImageModel, defaultImageModel)
	c.VeoModels = domain.NormalizeModelList(c.VeoModels, c.VeoModel, defaultVeoModel)
	c.GeminiModel = domain.NormalizeDefaultModel(c.GeminiModel, c.GeminiModels, defaultGeminiModel)
	c.ImageModel = domain.NormalizeDefaultModel(c.ImageModel, c.ImageModels, defaultImageModel)
	c.VeoModel = domain.NormalizeDefaultModel(c.VeoModel, c.VeoModels, defaultVeoModel)
}

// LoadConfig は環境変数から設定を読み込みます。
func LoadConfig() (*Config, error) {
	return LoadConfigFromEnv()
}

// LoadConfigFromEnv は環境変数から設定を読み込み、変換エラーを返します。
func LoadConfigFromEnv() (*Config, error) {
	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return nil, err
	}
	if err := cfg.normalize(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// normalizeStringSlice trims strings and removes empty values.
func normalizeStringSlice(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			normalized = append(normalized, value)
		}
	}
	return normalized
}
