package builder

import (
	"testing"
	"time"

	"github.com/shouni/go-character-kit/character"

	"github.com/shouni/ap-mv/internal/config"
)

// TestBuildOrchestratorConfigMapsModels verifies that configured model names map into orchestrator config.
func TestBuildOrchestratorConfigMapsModels(t *testing.T) {
	cfg := &config.Config{
		AI: config.AIConfig{
			GeminiModel:            "gemini-text",
			ImageModel:             "gemini-image",
			KeyframeMaxConcurrency: 4,
			KeyframeRateInterval:   10 * time.Second,
		},
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

// TestBuildOrchestratorConfigSatisfiesLibraryValidation verifies that a configured ap-mv config
// meets go-veo-orchestrator's required fields. Neither side keeps default model names, so this
// guards that buildOrchestratorConfig actually carries the configured ones across.
func TestBuildOrchestratorConfigSatisfiesLibraryValidation(t *testing.T) {
	cfg := &config.Config{
		AI: config.AIConfig{
			GeminiModels: []string{"gemini-text"},
			ImageModels:  []string{"gemini-image"},
		},
	}
	cfg.NormalizeModels()

	got := buildOrchestratorConfig(cfg)

	if err := got.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

// TestWithCharacterSeedOverrideReplacesOnlyTargetCharacter verifies the seed override is scoped
// to the requested character and leaves the base Characters (and other entries) untouched.
func TestWithCharacterSeedOverrideReplacesOnlyTargetCharacter(t *testing.T) {
	base, err := buildCharacters()
	if err != nil {
		t.Fatalf("buildCharacters() error = %v", err)
	}
	original := requireCharacter(t, base, "zundamon")
	var originalSeed *int64
	if original.Seed != nil {
		s := *original.Seed
		originalSeed = &s
	}

	overridden := withCharacterSeedOverride(base, "zundamon", 999999)

	got := requireCharacter(t, overridden, "zundamon")
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

	requireCharacter(t, chars, "zundamon")

	if def := requireDefaultCharacter(t, chars); def.ID != "tsumugi" {
		t.Fatalf("default character = %q, want tsumugi", def.ID)
	}
}

// requireCharacter は指定 ID のキャラクターを取り出し、無ければテストを打ち切ります。
// 呼び出し側で「nil チェック → デリファレンス」を書かずに済ませるための小さなヘルパーです。
// staticcheck の SA5011 は t.Fatal が戻らないことを解析結果（facts）から判断しますが、
// golangci-lint の増分キャッシュ経由ではこれを取りこぼして偽陽性を出すことがあるため、
// パターン自体を呼び出し側に残さないようにしています。
func requireCharacter(t *testing.T, chars *character.Characters, id string) *character.Character {
	t.Helper()
	c := chars.GetCharacter(id)
	if c == nil {
		t.Fatalf("character %q was not loaded", id)
	}
	return c
}

// requireDefaultCharacter は既定キャラクターを取り出し、無ければテストを打ち切ります。
// requireCharacter と同じく、呼び出し側に「nil チェック → デリファレンス」を残さないための
// ヘルパーです。
func requireDefaultCharacter(t *testing.T, chars *character.Characters) *character.Character {
	t.Helper()
	c := chars.GetDefault()
	if c == nil {
		t.Fatal("default character was not loaded")
	}
	return c
}
