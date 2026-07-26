// Package config は、環境変数からアプリケーション設定を読み込み・正規化します。
package config

import (
	"strings"
	"time"

	"github.com/caarlos0/env/v11"

	"github.com/shouni/ap-mv/internal/domain"
)

const taskGeneratePath = "/tasks/generate"

// ServerConfig はHTTPサーバーの起動・シャットダウンに関する設定です。
type ServerConfig struct {
	ServiceURL      string        `env:"SERVICE_URL" envDefault:"http://localhost:8080"`
	Port            string        `env:"PORT" envDefault:"8080"`
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"15s"`
}

// GCPConfig は Vertex AI / Cloud Tasks 呼び出しに使う GCP プロジェクト情報です。
type GCPConfig struct {
	ProjectID           string `env:"GCP_PROJECT_ID"`
	LocationID          string `env:"GCP_LOCATION_ID"`
	ServiceAccountEmail string `env:"SERVICE_ACCOUNT_EMAIL"`
}

// TasksConfig は Cloud Tasks キューへのエンキュー設定です。
type TasksConfig struct {
	QueueID         string `env:"CLOUD_TASKS_QUEUE_ID"`
	WorkerURL       string `env:"WORKER_URL"`
	TaskAudienceURL string `env:"TASK_AUDIENCE_URL"`
}

// StorageConfig は GCS バケットの設定です。
type StorageConfig struct {
	GCSBucket string `env:"AP_MV_BUCKET"`
	// MusicBucket は、Video Recipe Create の Music Job ID からレシピJSON
	// （gs://<MusicBucket>/music/<jobID>/recipe.json、ap-comp と同じ規則）を解決するために使う。
	MusicBucket string `env:"AP_MUSIC_BUCKET" envDefault:"ap-music"`
}

// AIConfig は Gemini / Image / Veo のモデルと生成パラメータです。
type AIConfig struct {
	GeminiModel         string        `env:"GEMINI_MODEL"`
	ImageModel          string        `env:"IMAGE_MODEL"`
	GeminiModels        []string      `env:"GEMINI_MODELS" envDefault:"gemini-3.6-flash"`
	ImageModels         []string      `env:"IMAGE_MODELS" envDefault:"gemini-3.1-flash-image"`
	VeoModel            string        `env:"VEO_MODEL" envDefault:"veo-3.1-generate-001"`
	VeoModels           []string      `env:"VEO_MODELS" envDefault:"veo-3.1-generate-001"`
	VeoLocationID       string        `env:"VEO_LOCATION_ID"`
	VeoOutputPrefix     string        `env:"VEO_OUTPUT_PREFIX" envDefault:"github.com/shouni/ap-mv/veo"`
	VeoAspectRatio      string        `env:"VEO_ASPECT_RATIO" envDefault:"16:9"`
	VeoGenerateAudio    bool          `env:"VEO_GENERATE_AUDIO" envDefault:"false"`
	VeoPollInterval     time.Duration `env:"VEO_POLL_INTERVAL" envDefault:"10s"`
	VeoOperationTimeout time.Duration `env:"VEO_OPERATION_TIMEOUT" envDefault:"20m"`
	// PipelineTimeout はワーカータスク 1 件の実行時間の上限です。0 以下は無制限を意味します。
	// VEO_OPERATION_TIMEOUT が Veo の 1 オペレーション単位の上限であるのに対し、
	// こちらはフィルター列全体（レシピ生成・キーフレーム・動画生成・公開）を包む上限です。
	PipelineTimeout        time.Duration `env:"PIPELINE_TIMEOUT" envDefault:"45m"`
	VeoPollMaxErrors       int           `env:"VEO_POLL_MAX_ERRORS" envDefault:"10"`
	VeoUsePreviousVideo    bool          `env:"VEO_USE_PREVIOUS_VIDEO" envDefault:"false"`
	KeyframeMaxConcurrency int           `env:"KEYFRAME_MAX_CONCURRENCY" envDefault:"1"`
	KeyframeRateInterval   time.Duration `env:"KEYFRAME_RATE_INTERVAL" envDefault:"60s"`
}

// NotificationConfig は外部通知先の設定です。
type NotificationConfig struct {
	SlackWebhookURL string `env:"SLACK_WEBHOOK_URL"`
}

// AuthConfig は OAuth・セッション・認可の設定です。
type AuthConfig struct {
	GoogleClientID     string `env:"GOOGLE_CLIENT_ID"`
	GoogleClientSecret string `env:"GOOGLE_CLIENT_SECRET"`
	// SessionSecret はセッションデータのHMAC署名用シークレットキーです。
	SessionSecret string `env:"SESSION_SECRET"`
	// SessionEncryptKey はセッションデータのAES暗号化用シークレットキーです。 16, 24, 32 バイトのいずれかである必要があります。
	SessionEncryptKey string   `env:"SESSION_ENCRYPT_KEY"`
	AllowedEmails     []string `env:"ALLOWED_EMAILS"`
	AllowedDomains    []string `env:"ALLOWED_DOMAINS"`
	// AllowedM2MServiceAccounts は、Web APIをサーバー間通信（OIDC Bearerトークン）で
	// 呼び出せるサービスアカウントのメールアドレスです。空ならM2M認証は無効です。
	AllowedM2MServiceAccounts []string `env:"ALLOWED_M2M_SERVICE_ACCOUNTS"`
}

// Config はアプリ設定です。
type Config struct {
	Server       ServerConfig
	GCP          GCPConfig
	Tasks        TasksConfig
	Storage      StorageConfig
	AI           AIConfig
	Notification NotificationConfig
	Auth         AuthConfig
}

// normalize normalizes the provided values.
func (c *Config) normalize() error {
	workerURL, err := normalizeWorkerURL(c.Server.ServiceURL, c.Tasks.WorkerURL)
	if err != nil {
		return err
	}
	c.Tasks.WorkerURL = workerURL
	if c.Tasks.TaskAudienceURL == "" {
		c.Tasks.TaskAudienceURL = c.Server.ServiceURL
	}
	c.Storage.GCSBucket = normalizeGCSBucket(c.Storage.GCSBucket)
	c.Storage.MusicBucket = normalizeGCSBucket(c.Storage.MusicBucket)
	c.Auth.AllowedEmails = normalizeStringSlice(c.Auth.AllowedEmails)
	c.Auth.AllowedDomains = normalizeStringSlice(c.Auth.AllowedDomains)
	c.Auth.AllowedM2MServiceAccounts = normalizeStringSlice(c.Auth.AllowedM2MServiceAccounts)
	// Veo は提供リージョンが限られる（例: us-central1）ため、Cloud Tasks 等と共有する
	// GCP_LOCATION_ID とは別に VEO_LOCATION_ID で上書きできる。未設定なら共通値を使う。
	if strings.TrimSpace(c.AI.VeoLocationID) == "" {
		c.AI.VeoLocationID = c.GCP.LocationID
	}
	c.NormalizeModels()
	return nil
}

// NormalizeModels normalizes configured model lists and defaults.
func (c *Config) NormalizeModels() {
	c.AI.GeminiModels = domain.NormalizeModelList(c.AI.GeminiModels, c.AI.GeminiModel, domain.DefaultGeminiModel)
	c.AI.ImageModels = domain.NormalizeModelList(c.AI.ImageModels, c.AI.ImageModel, domain.DefaultImageModel)
	c.AI.VeoModels = domain.NormalizeModelList(c.AI.VeoModels, c.AI.VeoModel, domain.DefaultVeoModel)
	c.AI.GeminiModel = domain.NormalizeDefaultModel(c.AI.GeminiModel, c.AI.GeminiModels, domain.DefaultGeminiModel)
	c.AI.ImageModel = domain.NormalizeDefaultModel(c.AI.ImageModel, c.AI.ImageModels, domain.DefaultImageModel)
	c.AI.VeoModel = domain.NormalizeDefaultModel(c.AI.VeoModel, c.AI.VeoModels, domain.DefaultVeoModel)
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
