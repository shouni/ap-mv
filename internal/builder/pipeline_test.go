package builder

import (
	"testing"

	"github.com/shouni/ap-mv/internal/config"
)

// TestBuildPipelinePassesOrchestratorConfig verifies that pipeline construction forwards orchestrator config.
func TestBuildPipelinePassesOrchestratorConfig(t *testing.T) {
	cfg := &config.Config{
		GeminiModel: "gemini-text",
		ImageModel:  "gemini-image",
	}

	runner, err := buildPipeline(t.Context(), cfg, nil, nil, nil)
	if err != nil {
		t.Fatalf("buildPipeline() error = %v", err)
	}

	if runner.OrchestratorConfig.GeminiModel != "gemini-text" {
		t.Fatalf("GeminiModel = %q", runner.OrchestratorConfig.GeminiModel)
	}
	if runner.OrchestratorConfig.ImageModel != "gemini-image" {
		t.Fatalf("ImageModel = %q", runner.OrchestratorConfig.ImageModel)
	}
}
