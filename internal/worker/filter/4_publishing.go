package filter

import (
	"context"
	"fmt"
)

type PublishingFilter struct{}

func (PublishingFilter) Name() string { return "publishing" }

func (PublishingFilter) Execute(ctx context.Context, fc *Context) error {
	if fc == nil {
		return fmt.Errorf("publishing requires recipe")
	}
	if fc.VideoRecipe == nil {
		if err := applyTaskAudioURL(fc.Task, fc.Recipe); err != nil {
			return err
		}
		recipe, err := toVideoRecipe(fc.Recipe)
		if err != nil {
			return err
		}
		fc.VideoRecipe = recipe
	}
	applyTaskAudioURLToVideoRecipe(fc.Task, fc.VideoRecipe)
	if fc.VideoRecipe == nil {
		return fmt.Errorf("publishing requires recipe")
	}
	if fc.Workflows != nil && fc.Workflows.Publish != nil {
		if fc.OutputPath == "" {
			return fmt.Errorf("output path is required")
		}
		if _, err := fc.Workflows.Publish.Run(ctx, fc.VideoRecipe, fc.OutputPath); err != nil {
			return err
		}
	}
	var err error
	fc.Recipe, err = toDomainRecipe(fc.VideoRecipe)
	if err != nil {
		return err
	}
	return fc.Recipe.Normalize()
}
