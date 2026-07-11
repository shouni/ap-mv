package filter

import (
	"context"
	"fmt"
	"strings"

	"github.com/shouni/go-veo-orchestrator/keyframe"

	"github.com/shouni/ap-mv/internal/domain"
)

// CutKeyframeFilter は、各カットのキーフレーム画像を生成するパイプラインステップです。
type CutKeyframeFilter struct{}

// Name returns the receiver name.
func (CutKeyframeFilter) Name() string { return "cut_keyframe_gen" }

// Execute runs the receiver processing step.
func (CutKeyframeFilter) Execute(ctx context.Context, fc *Context) error {
	if fc == nil {
		return fmt.Errorf("cut keyframe generation requires recipe")
	}
	if err := ensureVideoRecipe(fc); err != nil {
		return err
	}
	applyTaskAudioURLToVideoRecipe(fc.Task, fc.VideoRecipe)
	applyTaskCharacterIDToVideoRecipe(fc.Task, fc.VideoRecipe)
	if fc.Workflows == nil || fc.Workflows.CutKeyframe == nil {
		recipe, err := toDomainRecipe(fc.VideoRecipe)
		fc.Recipe = recipe
		return err
	}
	outputPath := fc.OutputPath
	if outputPath == "" {
		return fmt.Errorf("output path is required")
	}
	recipe, err := fc.Workflows.CutKeyframe.RunAndSave(ctx, fc.VideoRecipe, outputPath)
	if err != nil {
		return err
	}
	applyLyricsToVideoRecipeCuts(recipe)
	if strings.TrimSpace(recipe.AspectRatio) == "" {
		recipe.AspectRatio = resolvedAspectRatio(fc.Task)
	}
	fc.VideoRecipe = recipe
	fc.Recipe, err = toDomainRecipe(recipe)
	return err
}

// resolvedAspectRatio returns the aspect ratio a keyframe generation task actually used: the
// task's explicit choice (set once at recipe creation, or inherited from an existing recipe for
// operations on an existing job — see PostVideoRecipeCreate / PostGenerateVideoFromHistory /
// PostRegenerateCutKeyframe), falling back to keyframe.CutAspectRatio (the same default the
// Generator itself applies) when the task has none. Recording this on the recipe lets later
// video generation steps read a single source of truth instead of choosing independently.
func resolvedAspectRatio(task *domain.Task) string {
	if task != nil {
		if aspectRatio := strings.TrimSpace(task.VeoAspectRatio); aspectRatio != "" {
			return aspectRatio
		}
	}
	return keyframe.CutAspectRatio
}
