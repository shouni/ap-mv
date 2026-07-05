package builder

import (
	"testing"
	"time"

	"github.com/shouni/ap-mv/internal/config"
)

// TestBuildOrchestratorConfigMapsModels verifies that configured model names map into orchestrator config.
func TestBuildOrchestratorConfigMapsModels(t *testing.T) {
	cfg := &config.Config{
		GeminiModel:            "gemini-text",
		ImageModel:             "gemini-image",
		KeyframeMaxConcurrency: 4,
		KeyframeRateInterval:   10 * time.Second,
	}

	got := buildOrchestratorConfig(cfg)

	if got.GeminiModel != "gemini-text" {
		t.Fatalf("GeminiModel = %q", got.GeminiModel)
	}
	if got.ImageModel != "gemini-image" {
		t.Fatalf("ImageModel = %q", got.ImageModel)
	}
	if got.MaxConcurrency != 4 {
		t.Fatalf("MaxConcurrency = %d, want 4", got.MaxConcurrency)
	}
	if got.RateInterval != 10*time.Second {
		t.Fatalf("RateInterval = %s, want 10s", got.RateInterval)
	}
}

// TestBuildCharactersUsesBundledCharactersByDefault verifies that bundled characters load by default.
func TestBuildCharactersUsesBundledCharactersByDefault(t *testing.T) {
	chars, err := buildCharacters()
	if err != nil {
		t.Fatalf("buildCharacters() error = %v", err)
	}

	if chars.GetCharacter("zundamon") == nil {
		t.Fatal("bundled zundamon character was not loaded")
	}
	if got := chars.GetDefault(); got == nil || got.ID != "tsumugi" {
		if got == nil {
			t.Fatal("default character is nil, want tsumugi")
		}
		t.Fatalf("default character = %q, want tsumugi", got.ID)
	}
}
