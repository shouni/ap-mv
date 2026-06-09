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
	chars, err := buildCharacters(&config.Config{})
	if err != nil {
		t.Fatalf("buildCharacters() error = %v", err)
	}

	if chars.GetCharacter("default") == nil {
		t.Fatal("default character was not loaded")
	}
	if chars.GetCharacter("zundamon") == nil {
		t.Fatal("bundled zundamon character was not loaded")
	}
	if got := chars.GetDefault(); got == nil || got.ID != "default" {
		if got == nil {
			t.Fatal("default character is nil, want default")
		}
		t.Fatalf("default character = %q, want default", got.ID)
	}
}

func TestBuildCharactersUsesConfiguredReferenceURL(t *testing.T) {
	chars, err := buildCharacters(&config.Config{CharacterReferenceURL: "gs://bucket/custom.png"})
	if err != nil {
		t.Fatalf("buildCharacters() error = %v", err)
	}

	char := chars.GetCharacter("default")
	if char == nil {
		t.Fatal("default character was not loaded")
	}
	if char.ReferenceURL != "gs://bucket/custom.png" {
		t.Fatalf("ReferenceURL = %q, want configured URL", char.ReferenceURL)
	}
}
