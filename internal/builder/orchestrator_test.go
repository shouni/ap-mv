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
