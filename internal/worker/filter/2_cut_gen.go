package filter

import (
	"context"
	"fmt"
)

type CutKeyframeFilter struct{}

func (CutKeyframeFilter) Name() string { return "cut_keyframe_gen" }

func (CutKeyframeFilter) Execute(ctx context.Context, fc *Context) error {
	if fc == nil {
		return fmt.Errorf("cut keyframe generation requires recipe")
	}
	if fc.VideoRecipe == nil {
		recipe, err := toVideoRecipe(fc.Recipe)
		if err != nil {
			return err
		}
		fc.VideoRecipe = recipe
	}
	if fc.VideoRecipe == nil {
		return fmt.Errorf("cut keyframe generation requires recipe")
	}
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
	fc.VideoRecipe = recipe
	fc.Recipe, err = toDomainRecipe(recipe)
	return err
}
