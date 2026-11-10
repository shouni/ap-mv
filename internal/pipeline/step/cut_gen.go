package step

import (
	"context"
	"fmt"
	"strings"

	"github.com/shouni/ap-mv/internal/domain"
)

// CutKeyframeStep is the pipeline step that generates each cut's keyframe image.
type CutKeyframeStep struct{}

// Name returns the receiver name.
func (CutKeyframeStep) Name() string { return "cut_keyframe_gen" }

// Execute runs the receiver processing step.
func (CutKeyframeStep) Execute(ctx context.Context, sc *Context) error {
	if sc == nil {
		return fmt.Errorf("cut keyframe generation requires recipe")
	}
	if err := ensureVideoRecipe(sc); err != nil {
		return err
	}
	applyTaskAudioURLToVideoRecipe(sc.Task, sc.VideoRecipe)
	applyTaskCharacterIDToVideoRecipe(sc.Task, sc.VideoRecipe)
	// RunAndSave (below) writes video_music_meta.json to GCS itself after generating keyframes
	// (go-veo-orchestrator/runner/keyframe.go). So AspectRatio has to be settled before the call.
	// Setting it afterwards would update the in-memory object but not the file on GCS (it has
	// already been written).
	if strings.TrimSpace(sc.VideoRecipe.AspectRatio) == "" {
		sc.VideoRecipe.AspectRatio = resolvedAspectRatio(sc.Task)
	}
	if sc.Workflows == nil || sc.Workflows.CutKeyframe == nil {
		recipe, err := toDomainRecipe(sc.VideoRecipe)
		sc.Recipe = recipe
		return err
	}
	outputPath := sc.OutputPath
	if outputPath == "" {
		return fmt.Errorf("output path is required")
	}
	// RunAndSave does not re-render cuts that have a keyframe_reference
	// (go-veo-orchestrator v1.10.0 and later). SceneSplitStep keeps the reference of cuts reassigned
	// 1:1, so making video from a stored recipe does not have to remake the images.
	recipe, err := sc.Workflows.CutKeyframe.GenerateAndSave(ctx, sc.VideoRecipe, outputPath)
	if err != nil {
		return err
	}
	applyLyricsToVideoRecipeCuts(recipe)
	sc.VideoRecipe = recipe
	sc.Recipe, err = toDomainRecipe(recipe)
	return err
}

// resolvedAspectRatio returns the aspect ratio a keyframe generation task actually used: the
// task's explicit choice (set once at recipe creation, or inherited from an existing recipe for
// operations on an existing job — see PostVideoRecipeCreate / PostGenerateVideoFromHistory /
// PostRegenerateCutKeyframe), falling back to domain.DefaultAspectRatio when the task has none.
// The kit holds no art-direction default any more, so this app owns the fallback. Recording it
// on the recipe lets later video generation steps read a single source of truth instead of
// choosing independently.
func resolvedAspectRatio(task *domain.Task) string {
	if task != nil {
		if aspectRatio := strings.TrimSpace(task.VeoAspectRatio); aspectRatio != "" {
			return aspectRatio
		}
	}
	return domain.DefaultAspectRatio
}
