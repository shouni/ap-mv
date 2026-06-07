package pipeline

import (
	"context"
	"errors"
	"fmt"

	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"ap-mv/internal/domain"
	"ap-mv/internal/ports"
	"ap-mv/internal/worker/filter"
)

type Runner struct {
	VideoRunner        ports.VideoRunner
	TaskQueue          ports.TaskQueue
	Filters            []filter.Filter
	OrchestratorConfig orchestrator.Config
}

func New(videoRunner ports.VideoRunner, cfg ...orchestrator.Config) *Runner {
	r := &Runner{VideoRunner: videoRunner}
	if len(cfg) > 0 {
		r.OrchestratorConfig = cfg[0]
	}
	return r
}

func (r *Runner) Run(ctx context.Context, task *domain.Task) (*domain.MusicRecipe, error) {
	if err := task.Validate(); err != nil {
		return nil, err
	}
	fc := &filter.Context{Task: task, Recipe: task.Recipe, VideoRunner: r.VideoRunner, TaskQueue: r.TaskQueue}
	filters := r.Filters
	if len(filters) == 0 {
		filters = defaultFilters(task.Command, r.VideoRunner)
	}
	for _, flt := range filters {
		if err := flt.Execute(ctx, fc); err != nil {
			if errors.Is(err, filter.ErrPipelineDeferred) {
				return fc.Recipe, nil
			}
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
