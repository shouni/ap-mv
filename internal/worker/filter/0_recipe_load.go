package filter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"ap-mv/internal/domain"
)

const maxRecipeJSONSize = 5 * 1024 * 1024

type RecipeLoadFilter struct{}

// Name returns the receiver name.
func (RecipeLoadFilter) Name() string { return "recipe_load" }

// Execute runs the receiver processing step.
func (RecipeLoadFilter) Execute(ctx context.Context, fc *Context) error {
	if fc == nil || fc.Task == nil {
		return fmt.Errorf("recipe load requires task")
	}
	if fc.Recipe == nil {
		if strings.TrimSpace(fc.Task.RecipeURL) == "" {
			return fmt.Errorf("recipe or recipe_url is required")
		}
		if fc.Reader == nil {
			return fmt.Errorf("recipe reader is not configured")
		}
		recipe, err := readRecipe(ctx, fc.Reader, fc.Task.RecipeURL)
		if err != nil {
			return err
		}
		fc.Recipe = recipe
	}
	return applyTaskAudioURL(fc.Task, fc.Recipe)
}

// readRecipe reads and validates a music recipe from remote storage.
func readRecipe(ctx context.Context, reader interface {
	Open(context.Context, string) (io.ReadCloser, error)
}, uri string) (*domain.MusicRecipe, error) {
	rc, err := reader.Open(ctx, strings.TrimSpace(uri))
	if err != nil {
		return nil, fmt.Errorf("read recipe url: %w", err)
	}
	defer rc.Close()

	var recipe domain.MusicRecipe
	if err := json.NewDecoder(io.LimitReader(rc, maxRecipeJSONSize)).Decode(&recipe); err != nil {
		return nil, fmt.Errorf("decode recipe json: %w", err)
	}
	if err := recipe.Normalize(); err != nil {
		return nil, err
	}
	return &recipe, nil
}

// applyTaskAudioURL applies a task audio URL to a recipe.
func applyTaskAudioURL(task *domain.Task, recipe *domain.MusicRecipe) error {
	if task == nil || recipe == nil {
		return nil
	}
	audioURL := strings.TrimSpace(task.AudioURL)
	if audioURL == "" {
		return nil
	}
	if err := recipe.Normalize(); err != nil {
		return err
	}
	for i := range recipe.Cuts {
		if strings.TrimSpace(recipe.Cuts[i].AudioURI) == "" {
			recipe.Cuts[i].AudioURI = audioURL
		}
	}
	return nil
}
