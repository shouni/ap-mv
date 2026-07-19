package config

import (
	"os"
	"testing"
	"time"
)

var configEnvKeys = []string{
	"SERVICE_URL",
	"PORT",
	"GCP_PROJECT_ID",
	"GCP_LOCATION_ID",
	"CLOUD_TASKS_QUEUE_ID",
	"WORKER_URL",
	"TASK_AUDIENCE_URL",
	"SERVICE_ACCOUNT_EMAIL",
	"AP_MV_BUCKET",
	"AP_MUSIC_BUCKET",
	"SLACK_WEBHOOK_URL",
	"GEMINI_MODEL",
	"GEMINI_MODELS",
	"IMAGE_MODEL",
	"IMAGE_MODELS",
	"VEO_MODEL",
	"VEO_MODELS",
	"VEO_LOCATION_ID",
	"VEO_OUTPUT_PREFIX",
	"VEO_ASPECT_RATIO",
	"VEO_GENERATE_AUDIO",
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
	if cfg.AI.ImageModel != "gemini-3.1-flash-image" {
		t.Fatalf("ImageModel = %q", cfg.AI.ImageModel)
	}
	if len(cfg.AI.GeminiModels) != 1 || cfg.AI.GeminiModels[0] != "gemini-3.5-flash" {
		t.Fatalf("GeminiModels = %#v", cfg.AI.GeminiModels)
	}
	if len(cfg.AI.ImageModels) != 1 || cfg.AI.ImageModels[0] != "gemini-3.1-flash-image" {
		t.Fatalf("ImageModels = %#v", cfg.AI.ImageModels)
	}
}

// TestLoadConfigFromEnvOverrides verifies environment variable overrides.
func TestLoadConfigFromEnvOverrides(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("SERVICE_URL", "https://example.com")
	t.Setenv("WORKER_URL", "https://worker.example.com/tasks/generate")
	t.Setenv("TASK_AUDIENCE_URL", "https://tasks.example.com")
	t.Setenv("AP_MV_BUCKET", "gs://music-bucket/output/")
	t.Setenv("GEMINI_MODEL", "gemini-selected")
	t.Setenv("GEMINI_MODELS", "gemini-a, gemini-b")
	t.Setenv("IMAGE_MODEL", "image-standard")
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
	if cfg.AI.ImageModel != "image-standard" {
		t.Fatalf("ImageModel = %q", cfg.AI.ImageModel)
	}
	if len(cfg.AI.GeminiModels) != 3 || cfg.AI.GeminiModels[0] != "gemini-selected" || cfg.AI.GeminiModels[1] != "gemini-a" {
		t.Fatalf("GeminiModels = %#v", cfg.AI.GeminiModels)
	}
	if len(cfg.AI.ImageModels) != 3 || cfg.AI.ImageModels[0] != "image-standard" || cfg.AI.ImageModels[1] != "image-a" {
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
	t.Setenv("VEO_MODEL", "veo-selected")
	t.Setenv("VEO_MODELS", "veo-a, veo-b")

	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("LoadConfigFromEnv() error = %v", err)
	}

	if cfg.AI.VeoModel != "veo-selected" {
		t.Fatalf("VeoModel = %q", cfg.AI.VeoModel)
	}
	if len(cfg.AI.VeoModels) != 3 || cfg.AI.VeoModels[0] != "veo-selected" || cfg.AI.VeoModels[1] != "veo-a" || cfg.AI.VeoModels[2] != "veo-b" {
		t.Fatalf("VeoModels = %v, want selected model prepended", cfg.AI.VeoModels)
	}
	if cfg.AI.VeoLocationID != "asia-northeast1" {
		t.Fatalf("VeoLocationID = %q, want fallback to GCP_LOCATION_ID", cfg.AI.VeoLocationID)
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
