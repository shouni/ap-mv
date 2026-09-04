package step

import (
	"context"
	"fmt"

	"github.com/shouni/ap-mv/internal/domain"
)

// PublishingStep は、生成済み動画を公開先へアップロードするパイプラインステップです。
type PublishingStep struct{}

// Name returns the receiver name.
func (PublishingStep) Name() string { return "publishing" }

// Execute runs the receiver processing step.
func (PublishingStep) Execute(ctx context.Context, sc *Context) error {
	if sc == nil {
		return fmt.Errorf("publishing requires recipe")
	}
	if sc.VideoRecipe == nil {
		recipe, err := toVideoRecipe(sc.Recipe)
		if err != nil {
			return err
		}
		sc.VideoRecipe = recipe
	}
	applyTaskAudioURLToVideoRecipe(sc.Task, sc.VideoRecipe)
	if sc.VideoRecipe == nil {
		return fmt.Errorf("publishing requires recipe")
	}
	if sc.Workflows != nil && sc.Workflows.Publish != nil {
		if sc.OutputPath == "" {
			return fmt.Errorf("output path is required")
		}
		if _, err := sc.Workflows.Publish.Run(ctx, sc.VideoRecipe, sc.OutputPath); err != nil {
			return err
		}
	}
	var err error
	sc.Recipe, err = toDomainRecipe(sc.VideoRecipe)
	if err != nil {
		return err
	}
	return domain.NormalizeMusicRecipe(sc.Recipe)
}
