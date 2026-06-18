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
	if fc.VideoRecipe == nil {
		fc.VideoRecipe = fc.Task.VideoRecipe
	}
	if fc.VideoRecipe != nil {
		applyTaskAudioURLToVideoRecipe(fc.Task, fc.VideoRecipe)
		applyTaskCharacterIDToVideoRecipe(fc.Task, fc.VideoRecipe)
		if fc.Recipe == nil {
			recipe, err := toDomainRecipe(fc.VideoRecipe)
			if err != nil {
				return err
			}
			fc.Recipe = recipe
		}
		return nil
	}
	if fc.Recipe == nil {
		if strings.TrimSpace(fc.Task.RecipeURL) == "" {
			return fmt.Errorf("recipe or recipe_url is required")
		}
		if fc.Reader == nil {
			return fmt.Errorf("recipe reader is not configured")
		}
		recipe, videoRecipe, err := readRecipeInput(ctx, fc.Reader, fc.Task.RecipeURL)
		if err != nil {
			return err
		}
		if videoRecipe != nil {
			fc.VideoRecipe = videoRecipe
			applyTaskAudioURLToVideoRecipe(fc.Task, fc.VideoRecipe)
			applyTaskCharacterIDToVideoRecipe(fc.Task, fc.VideoRecipe)
			if recipe == nil {
				recipe, err = toDomainRecipe(fc.VideoRecipe)
				if err != nil {
					return err
				}
			}
			fc.Recipe = recipe
			return nil
		}
		fc.Recipe = recipe
	}
	if err := domain.NormalizeMusicRecipe(fc.Recipe); err != nil {
		return err
	}
	recipe, err := toVideoRecipe(fc.Recipe)
	if err != nil {
		return err
	}
	applyTaskAudioURLToVideoRecipe(fc.Task, recipe)
	applyTaskCharacterIDToVideoRecipe(fc.Task, recipe)
	fc.VideoRecipe = recipe
	return nil
}

// readRecipeInput reads and validates a music recipe or keyframe video recipe from remote storage.
func readRecipeInput(ctx context.Context, reader interface {
	Open(context.Context, string) (io.ReadCloser, error)
}, uri string) (*domain.MusicRecipe, *domain.VideoRecipe, error) {
	rc, err := reader.Open(ctx, strings.TrimSpace(uri))
	if err != nil {
		return nil, nil, fmt.Errorf("read recipe url: %w", err)
	}
	defer rc.Close()

	raw, err := io.ReadAll(io.LimitReader(rc, maxRecipeJSONSize))
	if err != nil {
		return nil, nil, fmt.Errorf("read recipe json: %w", err)
	}
	if looksLikeVideoRecipeJSON(raw) {
		var recipe domain.VideoRecipe
		if err := json.Unmarshal(raw, &recipe); err != nil {
			return nil, nil, fmt.Errorf("decode video recipe json: %w", err)
		}
		recipe.Normalize()
		if strings.TrimSpace(recipe.Title) == "" {
			return nil, nil, fmt.Errorf("video recipe title is required")
		}
		if len(recipe.Cuts) == 0 {
			return nil, nil, fmt.Errorf("video recipe requires cuts")
		}
		return nil, &recipe, nil
	}
	var recipe domain.MusicRecipe
	if err := json.Unmarshal(raw, &recipe); err != nil {
		return nil, nil, fmt.Errorf("decode recipe json: %w", err)
	}
	if err := domain.NormalizeMusicRecipe(&recipe); err != nil {
		return nil, nil, err
	}
	return &recipe, nil, nil
}

func looksLikeVideoRecipeJSON(raw []byte) bool {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return false
	}
	for _, key := range []string{"cuts", "project_title", "music_recipe"} {
		if _, ok := fields[key]; ok {
			return true
		}
	}
	return false
}
