package config

import (
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
)

const (
	defaultGeminiModel = "gemini-3.5-flash"
	defaultImageModel  = "gemini-3-pro-image-preview"
	taskGeneratePath   = "/tasks/generate"
)

// Config はアプリ設定です。
type Config struct {
	ServiceURL             string        `env:"SERVICE_URL" envDefault:"http://localhost:8080"`
	Port                   string        `env:"PORT" envDefault:"8080"`
	ProjectID              string        `env:"GCP_PROJECT_ID"`
	LocationID             string        `env:"GCP_LOCATION_ID"`
	QueueID                string        `env:"CLOUD_TASKS_QUEUE_ID"`
	WorkerURL              string        `env:"WORKER_URL"`
	TaskAudienceURL        string        `env:"TASK_AUDIENCE_URL"`
	ServiceAccountEmail    string        `env:"SERVICE_ACCOUNT_EMAIL"`
	GCSBucket              string        `env:"GCS_MUSIC_BUCKET"`
	SlackWebhookURL        string        `env:"SLACK_WEBHOOK_URL"`
	GeminiAPIKey           string        `env:"GEMINI_API_KEY"`
	GeminiModel            string        `env:"GEMINI_MODEL"`
	ImageModel             string        `env:"IMAGE_MODEL"`
	GeminiModels           []string      `env:"GEMINI_MODELS" envDefault:"gemini-3.5-flash,gemini-3.1-pro-preview"`
	ImageModels            []string      `env:"IMAGE_MODELS" envDefault:"gemini-3.1-flash-image,gemini-3-pro-image"`
	VeoModel               string        `env:"VEO_MODEL" envDefault:"veo-3.1-generate-001"`
	VeoOutputPrefix        string        `env:"VEO_OUTPUT_PREFIX" envDefault:"ap-mv/veo"`
	VeoAspectRatio         string        `env:"VEO_ASPECT_RATIO" envDefault:"16:9"`
	VeoGenerateAudio       bool          `env:"VEO_GENERATE_AUDIO" envDefault:"false"`
	VeoPollInterval        time.Duration `env:"VEO_POLL_INTERVAL" envDefault:"10s"`
	VeoOperationTimeout    time.Duration `env:"VEO_OPERATION_TIMEOUT" envDefault:"20m"`
	VeoPollMaxErrors       int           `env:"VEO_POLL_MAX_ERRORS" envDefault:"10"`
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
	c.AllowedEmails = normalizeStringSlice(c.AllowedEmails)
	c.AllowedDomains = normalizeStringSlice(c.AllowedDomains)
	c.NormalizeModels()
	return nil
}

// NormalizeModels normalizes configured model lists and defaults.
func (c *Config) NormalizeModels() {
	c.GeminiModels = normalizeModelList(c.GeminiModels, c.GeminiModel, defaultGeminiModel)
	c.ImageModels = normalizeModelList(c.ImageModels, c.ImageModel, defaultImageModel)
	c.GeminiModel = normalizeDefaultModel(c.GeminiModel, c.GeminiModels, defaultGeminiModel)
	c.ImageModel = normalizeDefaultModel(c.ImageModel, c.ImageModels, defaultImageModel)
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

// normalizeModelList normalizes available model names with preferred and fallback values.
func normalizeModelList(values []string, preferred, fallback string) []string {
	normalized := normalizeStringSlice(values)
	preferred = strings.TrimSpace(preferred)
	if preferred != "" {
		normalized = prependUnique(normalized, preferred)
	}
	if len(normalized) == 0 {
		normalized = []string{fallback}
	}
	return normalized
}

// normalizeDefaultModel selects a valid default model.
func normalizeDefaultModel(value string, models []string, fallback string) string {
	value = strings.TrimSpace(value)
	if value != "" {
		return value
	}
	if len(models) > 0 {
		return models[0]
	}
	return fallback
}

// prependUnique prepends a value while preserving uniqueness.
func prependUnique(values []string, preferred string) []string {
	result := []string{preferred}
	for _, value := range values {
		if value != preferred {
			result = append(result, value)
		}
	}
	return result
}
