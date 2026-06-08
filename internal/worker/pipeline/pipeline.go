package pipeline

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
	Workflows          *orchestrator.Workflows
	WorkflowFactory    func(context.Context, *domain.Task) (*orchestrator.Workflows, error)
	Reader             orchestrator.ContentReader
	OutputBaseURI      string
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
	workflows, err := r.workflowsForTask(ctx, task)
	if err != nil {
		return nil, err
	}
	fc := &filter.Context{
		Task:        task,
		Recipe:      task.Recipe,
		VideoRunner: r.VideoRunner,
		TaskQueue:   r.TaskQueue,
		Workflows:   workflows,
		Reader:      r.Reader,
		OutputPath:  r.outputPath(task),
	}
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

func (r *Runner) workflowsForTask(ctx context.Context, task *domain.Task) (*orchestrator.Workflows, error) {
	if r == nil {
		return nil, nil
	}
	if r.WorkflowFactory == nil || !r.usesCustomModels(task) {
		return r.Workflows, nil
	}
	workflows, err := r.WorkflowFactory(ctx, task)
	if err != nil {
		return nil, fmt.Errorf("build workflow for selected models: %w", err)
	}
	return workflows, nil
}

func (r *Runner) usesCustomModels(task *domain.Task) bool {
	if task == nil {
		return false
	}
	return !r.OrchestratorConfig.UsesModels(task.TextModel, task.ImageModel)
}

func (r *Runner) outputPath(task *domain.Task) string {
	if task == nil || strings.TrimSpace(r.OutputBaseURI) == "" {
		return ""
	}
	return strings.TrimRight(r.OutputBaseURI, "/") + "/" + task.JobID + "/"
}

// Execute は gcp-kit/worker.TaskExecutor に適合するためのエントリーポイントです。
func (r *Runner) Execute(ctx context.Context, task domain.Task) error {
	_, err := r.Run(ctx, &task)
	return err
}

func defaultFilters(command domain.TaskCommand, videoRunner ports.VideoRunner) []filter.Filter {
	filters := []filter.Filter{}
	switch command {
	case domain.CommandCompose, domain.CommandComposeToKeyframe:
		filters = append(filters, filter.ScriptingFilter{})
	default:
		filters = append(filters, filter.RecipeLoadFilter{})
	}
	filters = append(filters,
		filter.CutKeyframeFilter{},
	)
	if command == domain.CommandComposeToKeyframe {
		return filters
	}
	filters = append(filters,
		filter.VideoGenerationFilter{Runner: videoRunner},
		filter.PublishingFilter{},
	)
	return filters
}
