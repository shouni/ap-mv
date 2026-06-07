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
	"TASK_AUDIENCE_URL",
	"SERVICE_ACCOUNT_EMAIL",
	"GCS_MUSIC_BUCKET",
	"SLACK_WEBHOOK_URL",
	"GEMINI_API_KEY",
	"GEMINI_MODEL",
	"IMAGE_MODEL",
	"VEO_MODEL",
	"VEO_OUTPUT_PREFIX",
	"VEO_ASPECT_RATIO",
	"VEO_GENERATE_AUDIO",
	"VEO_POLL_INTERVAL",
	"VEO_OPERATION_TIMEOUT",
	"SHUTDOWN_TIMEOUT",
	"GOOGLE_CLIENT_ID",
	"GOOGLE_CLIENT_SECRET",
	"SESSION_SECRET",
	"SESSION_ENCRYPT_KEY",
	"ALLOWED_EMAILS",
	"ALLOWED_DOMAINS",
}

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

func TestLoadConfigFromEnvDefaults(t *testing.T) {
	clearConfigEnv(t)

	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("LoadConfigFromEnv() error = %v", err)
	}

	if cfg.ServiceURL != "http://localhost:8080" {
		t.Fatalf("ServiceURL = %q, want localhost default", cfg.ServiceURL)
	}
	if cfg.Port != "8080" {
		t.Fatalf("Port = %q, want 8080", cfg.Port)
	}
	if cfg.TaskAudienceURL != cfg.ServiceURL {
		t.Fatalf("TaskAudienceURL = %q, want ServiceURL %q", cfg.TaskAudienceURL, cfg.ServiceURL)
	}
	if cfg.VeoPollInterval != 10*time.Second {
		t.Fatalf("VeoPollInterval = %s, want 10s", cfg.VeoPollInterval)
	}
	if cfg.VeoOperationTimeout != 20*time.Minute {
		t.Fatalf("VeoOperationTimeout = %s, want 20m", cfg.VeoOperationTimeout)
	}
	if cfg.ImageModel != "gemini-3-pro-image-preview" {
		t.Fatalf("ImageModel = %q", cfg.ImageModel)
	}
}

func TestLoadConfigFromEnvOverrides(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("SERVICE_URL", "https://example.com")
	t.Setenv("TASK_AUDIENCE_URL", "https://tasks.example.com")
	t.Setenv("GCS_MUSIC_BUCKET", "gs://music-bucket/output/")
	t.Setenv("IMAGE_MODEL", "image-standard")
	t.Setenv("VEO_GENERATE_AUDIO", "true")
	t.Setenv("VEO_POLL_INTERVAL", "7s")
	t.Setenv("VEO_OPERATION_TIMEOUT", "30s")
	t.Setenv("ALLOWED_EMAILS", "a@example.com, b@example.com")
	t.Setenv("ALLOWED_DOMAINS", "example.com, example.jp")

	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("LoadConfigFromEnv() error = %v", err)
	}

	if cfg.TaskAudienceURL != "https://tasks.example.com" {
		t.Fatalf("TaskAudienceURL = %q", cfg.TaskAudienceURL)
	}
	if cfg.GCSBucket != "music-bucket/output" {
		t.Fatalf("GCSBucket = %q", cfg.GCSBucket)
	}
	if !cfg.VeoGenerateAudio {
		t.Fatal("VeoGenerateAudio = false, want true")
	}
	if cfg.ImageModel != "image-standard" {
		t.Fatalf("ImageModel = %q", cfg.ImageModel)
	}
	if cfg.VeoPollInterval != 7*time.Second {
		t.Fatalf("VeoPollInterval = %s, want 7s", cfg.VeoPollInterval)
	}
	if cfg.VeoOperationTimeout != 30*time.Second {
		t.Fatalf("VeoOperationTimeout = %s, want 30s", cfg.VeoOperationTimeout)
	}
	if len(cfg.AllowedEmails) != 2 || cfg.AllowedEmails[1] != "b@example.com" {
		t.Fatalf("AllowedEmails = %#v", cfg.AllowedEmails)
	}
	if len(cfg.AllowedDomains) != 2 || cfg.AllowedDomains[1] != "example.jp" {
		t.Fatalf("AllowedDomains = %#v", cfg.AllowedDomains)
	}
}

func TestLoadConfigFromEnvRejectsBareDurationNumber(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("VEO_POLL_INTERVAL", "7")

	if _, err := LoadConfigFromEnv(); err == nil {
		t.Fatal("LoadConfigFromEnv() error = nil, want duration parse error")
	}
}
