package filter

import (
	"context"
	"fmt"

	"github.com/shouni/ap-mv/internal/domain"
)

// PublishingFilter は、生成済み動画を公開先へアップロードするパイプラインステップです。
type PublishingFilter struct{}

// Name returns the receiver name.
func (PublishingFilter) Name() string { return "publishing" }

// Execute runs the receiver processing step.
func (PublishingFilter) Execute(ctx context.Context, fc *Context) error {
	if fc == nil {
		return fmt.Errorf("publishing requires recipe")
	}
	if fc.VideoRecipe == nil {
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
	return domain.NormalizeMusicRecipe(fc.Recipe)
}
