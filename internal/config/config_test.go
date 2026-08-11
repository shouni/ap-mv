package config

import (
	"os"
	"testing"
	"time"

	"github.com/shouni/gcp-kit/serverrole"
)

var configEnvKeys = []string{
	"SERVER_ROLE",
	"SERVICE_URL",
	"PORT",
	"GCP_PROJECT_ID",
	"GCP_LOCATION_ID",
	"CLOUD_TASKS_QUEUE_ID",
	"WORKER_URL",
	"TASK_AUDIENCE_URL",
	"TASK_CALLER_SERVICE_ACCOUNT_EMAIL",
	"AP_MV_BUCKET",
	"AP_MUSIC_BUCKET",
	"SLACK_WEBHOOK_URL",
	"GEMINI_MODELS",
	"IMAGE_MODELS",
	"VEO_MODELS",
	"VEO_LOCATION_ID",
	"VEO_OUTPUT_PREFIX",
	"VEO_ASPECT_RATIO",
	"VEO_GENERATE_AUDIO",
	"VEO_PRICE_USD_PER_SEC",
	"VEO_POLL_INTERVAL",
	"VEO_OPERATION_TIMEOUT",
	"KEYFRAME_MAX_CONCURRENCY",
	"KEYFRAME_RATE_INTERVAL",
	"SHUTDOWN_TIMEOUT",
	"GOOGLE_CLIENT_ID",
	"GOOGLE_CLIENT_SECRET",
	"SESSION_SECRET",
	"SESSION_ENCRYPT_KEY",
	"ALLOWED_EMAILS",
	"ALLOWED_DOMAINS",
}

// clearConfigEnv clears config-related environment variables for a test.
func clearConfigEnv(t *testing.T) {
	t.Helper()

	previous := make(map[string]string, len(configEnvKeys))
	present := make(map[string]bool, len(configEnvKeys))
	for _, key := range configEnvKeys {
		value, ok := os.LookupEnv(key)
		if ok {
			previous[key] = value
			present[key] = true
		}
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unset %s: %v", key, err)
		}
	}

	// SERVER_ROLE は明示が必須。役割に関心のないテストはローカル開発と同じ both で読む。
	// t.Setenv ではなく os.Setenv を使うのは、下の Cleanup が configEnvKeys 全体を
	// 元の値へ戻すため。t.Setenv を混ぜると復元が二重になり、順序に依存する。
	if err := os.Setenv("SERVER_ROLE", string(serverrole.Both)); err != nil {
		t.Fatalf("set SERVER_ROLE: %v", err)
	}

	t.Cleanup(func() {
		for _, key := range configEnvKeys {
			var err error
			if present[key] {
				err = os.Setenv(key, previous[key])
			} else {
				err = os.Unsetenv(key)
			}
			if err != nil {
				t.Fatalf("restore %s: %v", key, err)
			}
		}
	})
}

// TestLoadConfigFromEnvDefaults verifies default config values loaded from the environment.
func TestLoadConfigFromEnvDefaults(t *testing.T) {
	clearConfigEnv(t)

	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("LoadConfigFromEnv() error = %v", err)
	}

	if cfg.Server.ServiceURL != "http://localhost:8080" {
		t.Fatalf("ServiceURL = %q, want localhost default", cfg.Server.ServiceURL)
	}
	if cfg.Server.Port != "8080" {
		t.Fatalf("Port = %q, want 8080", cfg.Server.Port)
	}
	if cfg.Tasks.TaskAudienceURL != cfg.Server.ServiceURL {
		t.Fatalf("TaskAudienceURL = %q, want ServiceURL %q", cfg.Tasks.TaskAudienceURL, cfg.Server.ServiceURL)
	}
	if cfg.Tasks.WorkerURL != "http://localhost:8080/tasks/generate" {
		t.Fatalf("WorkerURL = %q, want localhost worker URL", cfg.Tasks.WorkerURL)
	}
	if cfg.AI.VeoPollInterval != 10*time.Second {
		t.Fatalf("VeoPollInterval = %s, want 10s", cfg.AI.VeoPollInterval)
	}
	if cfg.AI.VeoOperationTimeout != 20*time.Minute {
		t.Fatalf("VeoOperationTimeout = %s, want 20m", cfg.AI.VeoOperationTimeout)
	}
	if cfg.AI.KeyframeMaxConcurrency != 1 {
		t.Fatalf("KeyframeMaxConcurrency = %d, want 1", cfg.AI.KeyframeMaxConcurrency)
	}
	if cfg.AI.KeyframeRateInterval != 60*time.Second {
		t.Fatalf("KeyframeRateInterval = %s, want 60s", cfg.AI.KeyframeRateInterval)
	}
	// モデル名に組み込みの既定値はありません。既定値へ黙って落ちると、
	// 古いモデルを使い続けたまま気付けません。
	if cfg.AI.GeminiModel != "" || cfg.AI.ImageModel != "" || cfg.AI.VeoModel != "" {
		t.Fatalf("既定のモデル名が入っています: gemini=%q image=%q veo=%q",
			cfg.AI.GeminiModel, cfg.AI.ImageModel, cfg.AI.VeoModel)
	}
	if len(cfg.AI.GeminiModels) != 0 || len(cfg.AI.ImageModels) != 0 || len(cfg.AI.VeoModels) != 0 {
		t.Fatalf("既定のモデル一覧が入っています: gemini=%#v image=%#v veo=%#v",
			cfg.AI.GeminiModels, cfg.AI.ImageModels, cfg.AI.VeoModels)
	}
	if err := cfg.ValidateEssentialConfig(); err == nil {
		t.Fatal("モデル未設定が起動時検証を通ってしまいました")
	}
}

// TestLoadConfigFromEnvOverrides verifies environment variable overrides.
func TestLoadConfigFromEnvOverrides(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("SERVICE_URL", "https://example.com")
	t.Setenv("WORKER_URL", "https://worker.example.com/tasks/generate")
	t.Setenv("TASK_AUDIENCE_URL", "https://tasks.example.com")
	t.Setenv("AP_MV_BUCKET", "gs://music-bucket/output/")
	t.Setenv("GEMINI_MODELS", "gemini-a, gemini-b")
	t.Setenv("IMAGE_MODELS", "image-a, image-b")
	t.Setenv("VEO_GENERATE_AUDIO", "true")
	t.Setenv("VEO_POLL_INTERVAL", "7s")
	t.Setenv("VEO_OPERATION_TIMEOUT", "30s")
	t.Setenv("KEYFRAME_MAX_CONCURRENCY", "3")
	t.Setenv("KEYFRAME_RATE_INTERVAL", "10s")
	t.Setenv("ALLOWED_EMAILS", "a@example.com, b@example.com")
	t.Setenv("ALLOWED_DOMAINS", "example.com, example.jp")

	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("LoadConfigFromEnv() error = %v", err)
	}

	if cfg.Tasks.TaskAudienceURL != "https://tasks.example.com" {
		t.Fatalf("TaskAudienceURL = %q", cfg.Tasks.TaskAudienceURL)
	}
	if cfg.Tasks.WorkerURL != "https://worker.example.com/tasks/generate" {
		t.Fatalf("WorkerURL = %q", cfg.Tasks.WorkerURL)
	}
	if cfg.Storage.GCSBucket != "music-bucket/output" {
		t.Fatalf("GCSBucket = %q", cfg.Storage.GCSBucket)
	}
	if !cfg.AI.VeoGenerateAudio {
		t.Fatal("VeoGenerateAudio = false, want true")
	}
	// 単数形は一覧の先頭から埋まります（環境変数からは読みません）。
	if cfg.AI.ImageModel != "image-a" {
		t.Fatalf("ImageModel = %q", cfg.AI.ImageModel)
	}
	if len(cfg.AI.GeminiModels) != 2 || cfg.AI.GeminiModels[0] != "gemini-a" || cfg.AI.GeminiModels[1] != "gemini-b" {
		t.Fatalf("GeminiModels = %#v", cfg.AI.GeminiModels)
	}
	if len(cfg.AI.ImageModels) != 2 || cfg.AI.ImageModels[0] != "image-a" || cfg.AI.ImageModels[1] != "image-b" {
		t.Fatalf("ImageModels = %#v", cfg.AI.ImageModels)
	}
	if cfg.AI.VeoPollInterval != 7*time.Second {
		t.Fatalf("VeoPollInterval = %s, want 7s", cfg.AI.VeoPollInterval)
	}
	if cfg.AI.VeoOperationTimeout != 30*time.Second {
		t.Fatalf("VeoOperationTimeout = %s, want 30s", cfg.AI.VeoOperationTimeout)
	}
	if cfg.AI.KeyframeMaxConcurrency != 3 {
		t.Fatalf("KeyframeMaxConcurrency = %d, want 3", cfg.AI.KeyframeMaxConcurrency)
	}
	if cfg.AI.KeyframeRateInterval != 10*time.Second {
		t.Fatalf("KeyframeRateInterval = %s, want 10s", cfg.AI.KeyframeRateInterval)
	}
	if len(cfg.Auth.AllowedEmails) != 2 || cfg.Auth.AllowedEmails[1] != "b@example.com" {
		t.Fatalf("AllowedEmails = %#v", cfg.Auth.AllowedEmails)
	}
	if len(cfg.Auth.AllowedDomains) != 2 || cfg.Auth.AllowedDomains[1] != "example.jp" {
		t.Fatalf("AllowedDomains = %#v", cfg.Auth.AllowedDomains)
	}
}

// TestLoadConfigFromEnvVeoModelsAndLocation verifies Veo model list normalization and
// the VEO_LOCATION_ID fallback to GCP_LOCATION_ID.
func TestLoadConfigFromEnvVeoModelsAndLocation(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("GCP_LOCATION_ID", "asia-northeast1")
	t.Setenv("VEO_MODELS", "veo-a, veo-b")

	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("LoadConfigFromEnv() error = %v", err)
	}

	if cfg.AI.VeoModel != "veo-a" {
		t.Fatalf("VeoModel = %q", cfg.AI.VeoModel)
	}
	if len(cfg.AI.VeoModels) != 2 || cfg.AI.VeoModels[0] != "veo-a" || cfg.AI.VeoModels[1] != "veo-b" {
		t.Fatalf("VeoModels = %v", cfg.AI.VeoModels)
	}
	if cfg.AI.VeoLocationID != "asia-northeast1" {
		t.Fatalf("VeoLocationID = %q, want fallback to GCP_LOCATION_ID", cfg.AI.VeoLocationID)
	}
}

// TestLoadConfigFromEnvVeoPricingUnset は、単価表に組み込みの既定値が無いことを確認します。
// モデル名も価格も Google 側の都合で変わるため、表そのものはデプロイ設定に置きます。
// 空表が既定単価へ落ちることは domain.VeoPricing 側のテストが見ています。
func TestLoadConfigFromEnvVeoPricingUnset(t *testing.T) {
	clearConfigEnv(t)

	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("LoadConfigFromEnv() error = %v", err)
	}

	if len(cfg.AI.VeoPriceUSDPerSec) != 0 {
		t.Fatalf("VeoPriceUSDPerSec = %v, want empty", cfg.AI.VeoPriceUSDPerSec)
	}
}

// TestLoadConfigFromEnvVeoPricingOverride verifies the price table can be replaced from the
// environment, including the "" fallback key.
func TestLoadConfigFromEnvVeoPricingOverride(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("VEO_PRICE_USD_PER_SEC", "veo-x:1.25,:0.05")

	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("LoadConfigFromEnv() error = %v", err)
	}

	if got := cfg.AI.VeoPriceUSDPerSec["veo-x"]; got != 1.25 {
		t.Fatalf("VeoPriceUSDPerSec[veo-x] = %v, want 1.25", got)
	}
	if got := cfg.AI.VeoPriceUSDPerSec[""]; got != 0.05 {
		t.Fatalf("VeoPriceUSDPerSec[\"\"] = %v, want 0.05 fallback", got)
	}
	if _, ok := cfg.AI.VeoPriceUSDPerSec["veo-3.1-generate-001"]; ok {
		t.Fatal("VeoPriceUSDPerSec kept a default entry, want the env value to replace the table")
	}
}

// TestLoadConfigFromEnvVeoLocationOverride verifies VEO_LOCATION_ID takes precedence over GCP_LOCATION_ID.
func TestLoadConfigFromEnvVeoLocationOverride(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("GCP_LOCATION_ID", "asia-northeast1")
	t.Setenv("VEO_LOCATION_ID", "us-central1")

	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("LoadConfigFromEnv() error = %v", err)
	}

	if cfg.AI.VeoLocationID != "us-central1" {
		t.Fatalf("VeoLocationID = %q, want us-central1", cfg.AI.VeoLocationID)
	}
	if cfg.GCP.LocationID != "asia-northeast1" {
		t.Fatalf("LocationID = %q, want asia-northeast1", cfg.GCP.LocationID)
	}
}

// TestLoadConfigFromEnvRejectsBareDurationNumber verifies bare duration numbers are rejected.
func TestLoadConfigFromEnvRejectsBareDurationNumber(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("VEO_POLL_INTERVAL", "7")

	if _, err := LoadConfigFromEnv(); err == nil {
		t.Fatal("LoadConfigFromEnv() error = nil, want duration parse error")
	}
}

// TestLoadConfigFromEnvRejectsInvalidServiceURLForWorkerDefault verifies invalid service URLs fail worker URL derivation.
func TestLoadConfigFromEnvRejectsInvalidServiceURLForWorkerDefault(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("SERVICE_URL", "http://[::1")

	if _, err := LoadConfigFromEnv(); err == nil {
		t.Fatal("LoadConfigFromEnv() error = nil, want invalid service URL error")
	}
}
