package builder

import (
	"testing"

	"ap-mv/internal/config"
)

func TestBuildPipelinePassesOrchestratorConfig(t *testing.T) {
	cfg := &config.Config{
		GeminiModel: "gemini-text",
		ImageModel:  "gemini-image",
	}

	runner := buildPipeline(t.Context(), cfg, nil, nil, nil)

	if runner.OrchestratorConfig.GeminiModel != "gemini-text" {
		t.Fatalf("GeminiModel = %q", runner.OrchestratorConfig.GeminiModel)
	}
	if runner.OrchestratorConfig.ImageModel != "gemini-image" {
		t.Fatalf("ImageModel = %q", runner.OrchestratorConfig.ImageModel)
	}
}
