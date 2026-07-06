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

// TestWithCharacterSeedOverrideReplacesOnlyTargetCharacter verifies the seed override is scoped
// to the requested character and leaves the base Characters (and other entries) untouched.
func TestWithCharacterSeedOverrideReplacesOnlyTargetCharacter(t *testing.T) {
	base, err := buildCharacters()
	if err != nil {
		t.Fatalf("buildCharacters() error = %v", err)
	}
	original := base.GetCharacter("zundamon")
	if original == nil {
		t.Fatal("bundled zundamon character was not loaded")
	}
	var originalSeed *int64
	if original.Seed != nil {
		s := *original.Seed
		originalSeed = &s
	}

	overridden := withCharacterSeedOverride(base, "zundamon", 999999)

	got := overridden.GetCharacter("zundamon")
	if got == nil {
		t.Fatal("overridden characters missing zundamon")
	}
	if got.Seed == nil || *got.Seed != 999999 {
		t.Fatalf("overridden seed = %v, want 999999", got.Seed)
	}

	// The base Characters passed in must not be mutated.
	stillOriginal := base.GetCharacter("zundamon")
	if originalSeed == nil {
		if stillOriginal.Seed != nil {
			t.Fatalf("base character seed mutated: got %v, want nil", *stillOriginal.Seed)
		}
	} else if stillOriginal.Seed == nil || *stillOriginal.Seed != *originalSeed {
		t.Fatalf("base character seed mutated: got %v, want %d", stillOriginal.Seed, *originalSeed)
	}
}

// TestWithCharacterSeedOverrideUnknownCharacterReturnsBase verifies an unknown character ID
// is a no-op rather than silently dropping the whole roster.
func TestWithCharacterSeedOverrideUnknownCharacterReturnsBase(t *testing.T) {
	base, err := buildCharacters()
	if err != nil {
		t.Fatalf("buildCharacters() error = %v", err)
	}

	got := withCharacterSeedOverride(base, "no-such-character", 1)

	if got != base {
		t.Fatal("expected unknown character ID to return the base Characters unchanged")
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
