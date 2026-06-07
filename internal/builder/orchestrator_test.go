package builder

import (
	"testing"

	"ap-mv/internal/config"
)

func TestBuildOrchestratorConfigMapsModels(t *testing.T) {
	cfg := &config.Config{
		GeminiModel: "gemini-text",
		ImageModel:  "gemini-image",
	}

	got := buildOrchestratorConfig(cfg)

	if got.GeminiModel != "gemini-text" {
		t.Fatalf("GeminiModel = %q", got.GeminiModel)
	}
	if got.ImageModel != "gemini-image" {
		t.Fatalf("ImageModel = %q", got.ImageModel)
	}
}
