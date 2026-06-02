package config

import (
	"time"
)

const (
	DefaultPort          = "8080"
	DefaultGeminiModel   = "gemini-3.5-flash"
	DefaultVeoModel      = "veo-3.1-generate-001"
	DefaultVeoAspect     = "16:9"
	DefaultVeoOutputRoot = "ap-mv/veo"
	DefaultShutdownGrace = 15 * time.Second
)

// Config はアプリ設定です。
type Config struct {
	ServiceURL          string
	Port                string
	ProjectID           string
	LocationID          string
	QueueID             string
	TaskAudienceURL     string
	ServiceAccountEmail string
	GCSBucket           string
	SlackWebhookURL     string
	GeminiAPIKey        string
	GeminiModel         string
	VeoModel            string
	VeoOutputPrefix     string
	VeoAspectRatio      string
	VeoGenerateAudio    bool
	VeoPollInterval     time.Duration
	VeoOperationTimeout time.Duration
	ShutdownTimeout     time.Duration

	// OAuth & Session Settings
	GoogleClientID     string
	GoogleClientSecret string
	// SessionSecret はセッションデータのHMAC署名用シークレットキーです。
	SessionSecret string
	// SessionEncryptKey はセッションデータのAES暗号化用シークレットキーです。 16, 24, 32 バイトのいずれかである必要があります。
	SessionEncryptKey string

	// Authz Settings
	AllowedEmails  []string
	AllowedDomains []string
}

// LoadConfig は環境変数から設定を読み込みます。
func LoadConfig() *Config {
	serviceURL := getEnv("SERVICE_URL", "http://localhost:8080")
	allowedEmails := getEnv("ALLOWED_EMAILS", "")
	allowedDomains := getEnv("ALLOWED_DOMAINS", "")

	cfg := Config{
		ServiceURL:          serviceURL,
		Port:                getEnv("PORT", DefaultPort),
		ProjectID:           getEnv("GCP_PROJECT_ID", ""),
		LocationID:          getEnv("GCP_LOCATION_ID", ""),
		QueueID:             getEnv("CLOUD_TASKS_QUEUE_ID", ""),
		TaskAudienceURL:     getEnv("TASK_AUDIENCE_URL", serviceURL),
		ServiceAccountEmail: getEnv("SERVICE_ACCOUNT_EMAIL", ""),
		GCSBucket:           getEnv("GCS_MUSIC_BUCKET", ""),
		SlackWebhookURL:     getEnv("SLACK_WEBHOOK_URL", ""),
		GeminiAPIKey:        getEnv("GEMINI_API_KEY", ""),
		GeminiModel:         getEnv("GEMINI_MODEL", DefaultGeminiModel),
		VeoModel:            getEnv("VEO_MODEL", DefaultVeoModel),
		VeoOutputPrefix:     getEnv("VEO_OUTPUT_PREFIX", DefaultVeoOutputRoot),
		VeoAspectRatio:      getEnv("VEO_ASPECT_RATIO", DefaultVeoAspect),
		VeoGenerateAudio:    getEnvAsBool("VEO_GENERATE_AUDIO", false),
		VeoPollInterval:     time.Duration(getEnvAsInt("VEO_POLL_INTERVAL_SECONDS", 10)) * time.Second,
		VeoOperationTimeout: time.Duration(getEnvAsInt("VEO_OPERATION_TIMEOUT_SECONDS", 20*60)) * time.Second,
		ShutdownTimeout:     DefaultShutdownGrace,

		// OAuth & Session
		GoogleClientID:     getEnv("GOOGLE_CLIENT_ID", ""),
		GoogleClientSecret: getEnv("GOOGLE_CLIENT_SECRET", ""),
		SessionSecret:      getEnv("SESSION_SECRET", ""),
		SessionEncryptKey:  getEnv("SESSION_ENCRYPT_KEY", ""),

		AllowedEmails:  parseCommaSeparatedList(allowedEmails),
		AllowedDomains: parseCommaSeparatedList(allowedDomains),
	}

	return &cfg
}
