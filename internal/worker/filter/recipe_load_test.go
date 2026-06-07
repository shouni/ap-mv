package filter

import (
	"context"
	"io"
	"strings"
	"testing"

	"ap-mv/internal/domain"
)

func TestRecipeLoadFilterLoadsRecipeURLAndAppliesAudioURL(t *testing.T) {
	reader := staticReader{content: `{
		"title": "recipe from gcs",
		"sections": [
			{"name": "intro", "duration_seconds": 8, "prompt": "blue light"}
		]
	}`}
	fc := &Context{
		Task: &domain.Task{
			JobID:     "job-1",
			Command:   domain.CommandGenerateFromRecipe,
			RecipeURL: "gs://bucket/recipe.json",
			AudioURL:  "gs://bucket/music.mp3",
		},
		Reader: reader,
	}

	if err := (RecipeLoadFilter{}).Execute(context.Background(), fc); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if fc.Recipe == nil {
		t.Fatal("recipe was not loaded")
	}
	if got := fc.Recipe.Cuts[0].AudioURI; got != "gs://bucket/music.mp3" {
		t.Fatalf("AudioURI = %q, want audio URL", got)
	}
}

type staticReader struct {
	content string
}

func (r staticReader) Open(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(r.content)), nil
}
