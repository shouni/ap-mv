package filter

import (
	"context"
	"fmt"
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
	fc.VideoRecipe = recipe
	fc.Recipe, err = toDomainRecipe(recipe)
	return err
}
