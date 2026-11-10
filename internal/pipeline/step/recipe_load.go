// Package step provides the implementations of the steps (Step) that make up the
// video-generation worker's pipeline.
package step

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/shouni/go-veo-orchestrator/video"

	"github.com/shouni/ap-mv/internal/domain"
)

const maxRecipeJSONSize = 5 * 1024 * 1024

// RecipeLoadStep is the pipeline's first step, loading the recipe (VideoRecipe/MusicRecipe)
type RecipeLoadStep struct{}

// Name returns the receiver name.
func (RecipeLoadStep) Name() string { return "recipe_load" }

// Execute runs the receiver processing step.
func (RecipeLoadStep) Execute(ctx context.Context, sc *Context) error {
	if sc == nil || sc.Task == nil {
		return fmt.Errorf("recipe load requires task")
	}
	if sc.VideoRecipe == nil {
		sc.VideoRecipe = sc.Task.VideoRecipe
	}
	// There are three ways the recipe arrives (an existing VideoRecipe / a VideoRecipe decoded from
	// storage / converted from a MusicRecipe), but each settles sc.VideoRecipe and then applies the
	// task's audio_url / character_id once at the end.
	switch {
	case sc.VideoRecipe != nil:
		// A VideoRecipe is already there. Derive the domain recipe if it is unset.
		if sc.Recipe == nil {
			recipe, err := toDomainRecipe(sc.VideoRecipe)
			if err != nil {
				return err
			}
			sc.Recipe = recipe
		}
	case sc.Recipe == nil:
		// Neither VideoRecipe nor MusicRecipe, so load from RecipeURL.
		if strings.TrimSpace(sc.Task.RecipeURL) == "" {
			return fmt.Errorf("recipe or recipe_url is required")
		}
		if sc.Reader == nil {
			return fmt.Errorf("recipe reader is not configured")
		}
		recipe, videoRecipe, err := readRecipeInput(ctx, sc.Reader, sc.Task.RecipeURL)
		if err != nil {
			return err
		}
		if videoRecipe != nil {
			sc.VideoRecipe = videoRecipe
			if recipe == nil {
				recipe, err = toDomainRecipe(sc.VideoRecipe)
				if err != nil {
					return err
				}
			}
			sc.Recipe = recipe
		} else {
			// Decoded a MusicRecipe. The conversion block below builds the VideoRecipe.
			sc.Recipe = recipe
		}
	}
	// If the VideoRecipe is still unsettled here (the MusicRecipe path), convert to settle it.
	if sc.VideoRecipe == nil {
		if err := domain.NormalizeMusicRecipe(sc.Recipe); err != nil {
			return err
		}
		recipe, err := toVideoRecipe(sc.Recipe)
		if err != nil {
			return err
		}
		sc.VideoRecipe = recipe
	}
	applyTaskAudioURLToVideoRecipe(sc.Task, sc.VideoRecipe)
	applyTaskCharacterIDToVideoRecipe(sc.Task, sc.VideoRecipe)
	absolutizeKeyframeReferences(sc.VideoRecipe, originalJobOutputPath(sc.Task.RecipeURL))
	return nil
}

// absolutizeKeyframeReferences makes keyframe_reference values stored relative to the original
// job into absolute URIs from that job's root.
//
// A stored recipe runs as a new job, so a relative reference would resolve against the new job's
// output path and point at a different object (usually one that does not exist). It is fixed here
// because the downstream CutKeyframeRunner decides "non-empty keyframe_reference = do not
// re-render". Passing it on unresolved would reuse a reference whose target is broken.
//
// An empty base (when recipe JSON is passed directly, for example) and references that already
// have a scheme are left as they are.
func absolutizeKeyframeReferences(recipe *video.Recipe, base string) {
	if recipe == nil || base == "" {
		return
	}
	for i := range recipe.Cuts {
		recipe.Cuts[i].KeyframeReference = resolveRecipeObjectURI(base, recipe.Cuts[i].KeyframeReference)
	}
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
	return domain.UnmarshalRecipeOrVideoRecipe(raw)
}
