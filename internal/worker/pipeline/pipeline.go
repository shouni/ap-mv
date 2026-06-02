package pipeline

import (
	"context"
	"fmt"

	"ap-mv/internal/domain"
	"ap-mv/internal/ports"
	"ap-mv/internal/worker/filter"
)

type Runner struct {
	VideoRunner ports.VideoRunner
	Filters     []filter.Filter
}

func New(videoRunner ports.VideoRunner) *Runner {
	return &Runner{VideoRunner: videoRunner}
}

func (r *Runner) Run(ctx context.Context, task *domain.Task) (*domain.MusicRecipe, error) {
	if err := task.Validate(); err != nil {
		return nil, err
	}
	fc := &filter.Context{Task: task, Recipe: task.Recipe, VideoRunner: r.VideoRunner}
	filters := r.Filters
	if len(filters) == 0 {
		filters = defaultFilters(task.Command, r.VideoRunner)
	}
	for _, flt := range filters {
		if err := flt.Execute(ctx, fc); err != nil {
			return nil, fmt.Errorf("filter %s: %w", flt.Name(), err)
		}
	}
	return fc.Recipe, nil
}

// Execute は gcp-kit/worker.TaskExecutor に適合するためのエントリーポイントです。
func (r *Runner) Execute(ctx context.Context, task domain.Task) error {
	_, err := r.Run(ctx, &task)
	return err
}

func defaultFilters(command domain.TaskCommand, videoRunner ports.VideoRunner) []filter.Filter {
	filters := []filter.Filter{}
	if command == domain.CommandCompose {
		filters = append(filters, filter.ScriptingFilter{})
	}
	filters = append(filters,
		filter.CutKeyframeFilter{},
		filter.VideoGenerationFilter{Runner: videoRunner},
		filter.PublishingFilter{},
	)
	return filters
}
