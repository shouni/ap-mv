package ports

import "testing"

func TestConfigWithModelsOverridesNonEmptyValues(t *testing.T) {
	cfg := Config{GeminiModel: "gemini-default", ImageModel: "image-default"}

	got := cfg.WithModels(" gemini-selected ", " image-selected ")

	if got.GeminiModel != "gemini-selected" {
		t.Fatalf("GeminiModel = %q, want gemini-selected", got.GeminiModel)
	}
	if got.ImageModel != "image-selected" {
		t.Fatalf("ImageModel = %q, want image-selected", got.ImageModel)
	}
}

func TestConfigWithModelsKeepsCurrentModelsForEmptyValues(t *testing.T) {
	cfg := Config{GeminiModel: "gemini-default", ImageModel: "image-default"}

	got := cfg.WithModels("", " ")

	if got.GeminiModel != "gemini-default" {
		t.Fatalf("GeminiModel = %q, want gemini-default", got.GeminiModel)
	}
	if got.ImageModel != "image-default" {
		t.Fatalf("ImageModel = %q, want image-default", got.ImageModel)
	}
}

func TestConfigUsesModels(t *testing.T) {
	cfg := Config{GeminiModel: "gemini-default", ImageModel: "image-default"}

	if !cfg.UsesModels("gemini-default", "image-default") {
		t.Fatal("UsesModels() = false, want true for same models")
	}
	if cfg.UsesModels("gemini-alt", "image-default") {
		t.Fatal("UsesModels() = true, want false for different gemini model")
	}
	if cfg.UsesModels("gemini-default", "image-alt") {
		t.Fatal("UsesModels() = true, want false for different image model")
	}
}
