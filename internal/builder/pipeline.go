package builder

import (
	"context"
	"fmt"
	"strings"

	"github.com/shouni/genai-kit/gemini"
	"github.com/shouni/go-http-kit/httpkit"
	"github.com/shouni/go-remote-io/remoteio"
	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"github.com/shouni/ap-mv/internal/adapters"
	"github.com/shouni/ap-mv/internal/config"
	"github.com/shouni/ap-mv/internal/domain"
	"github.com/shouni/ap-mv/internal/pipeline"
	"github.com/shouni/ap-mv/internal/ports"
)

// pipelineExternals are the pipeline's external dependencies, built first on the BuildContainer
// side and passed to buildPipeline.
type pipelineExternals struct {
	notifier          ports.Notifier
	taskQueue         ports.TaskQueue
	historyRepository ports.HistoryRepository
	jobStatus         ports.JobStatusStore
}

// buildPipeline injects and returns the boundary implementations needed to run the pipeline.
// pipeline.New validates required dependencies, so no fields are assigned afterwards.
func buildPipeline(
	ctx context.Context,
	cfg *config.Config,
	store remoteio.Store,
	httpClient httpkit.HTTPClient,
	videoRunner ports.VideoRunner,
	aiClient gemini.Generator,
	externals pipelineExternals,
) (*pipeline.Runner, error) {
	workflows, err := buildWorkflow(ctx, cfg, store, httpClient, videoRunner, aiClient)
	if err != nil {
		return nil, err
	}
	characters, err := buildCharacters()
	if err != nil {
		return nil, fmt.Errorf("load characters for pipeline: %w", err)
	}
	deps := pipeline.Dependencies{
		VideoRunner:       videoRunner,
		TaskQueue:         externals.taskQueue,
		Characters:        characters,
		HistoryRepository: externals.historyRepository,
		WorkflowResolver:  newWorkflowResolver(cfg, store, httpClient, videoRunner, aiClient, workflows),
		Notifier:          externals.notifier,
		OutputBaseURI:     workflowOutputBaseURI(cfg),
		Timeout:           cfg.Tasks.PipelineTimeout,
		JobStatus:         externals.jobStatus,
	}
	planner := &pipeline.DefaultPlanner{UsePreviousVideo: cfg.AI.VeoUsePreviousVideo}
	deps.Planner = planner
	if store != nil {
		deps.Reader = workflowReader{delegate: store}
		deps.Writer = store
		planner.VideoProcessor = adapters.NewFFmpegVideoProcessor(deps.Reader, deps.Writer)
	}
	return pipeline.New(deps)
}

// newWorkflowResolver assembles the production workflowResolver bundling the shared Workflows and
// the task-specific build.
func newWorkflowResolver(
	cfg *config.Config,
	store remoteio.Store,
	httpClient httpkit.HTTPClient,
	videoRunner ports.VideoRunner,
	aiClient gemini.Generator,
	shared *orchestrator.Workflows,
) *workflowResolver {
	orchCfg := buildOrchestratorConfig(cfg)
	return &workflowResolver{
		orchCfg: orchCfg,
		shared:  shared,
		build: func(ctx context.Context, task *domain.Task) (*orchestrator.Workflows, error) {
			taskOrchCfg := orchCfg
			taskVideoRunner := videoRunner
			if task != nil {
				taskOrchCfg = taskOrchCfg.WithModels(task.TextModel, task.ImageModel).WithAspectRatio(task.VeoAspectRatio)
				taskVideoRunner = ports.DeriveVideoRunner(videoRunner, task.VeoModel, task.VeoAspectRatio)
			}
			return buildWorkflowWithConfig(ctx, workflowBuildParams{
				cfg:          cfg,
				orchCfg:      taskOrchCfg,
				store:        store,
				httpClient:   httpClient,
				videoRunner:  taskVideoRunner,
				aiClient:     aiClient,
				visualMode:   taskVisualMode(task),
				seedOverride: taskSeedOverride(task),
			})
		},
	}
}

// workflowResolver is the production implementation of pipeline.WorkflowResolver. When a task
// keeps the default model, seed and Veo settings it reuses the shared Workflows, and only when
// task-specific options are given does build construct task-specific Workflows.
// Deciding "is a task-specific build needed" and the build itself are gathered in this type, so
// adding a task-specific option only needs a change here.
type workflowResolver struct {
	orchCfg orchestrator.Config
	shared  *orchestrator.Workflows
	build   func(context.Context, *domain.Task) (*orchestrator.Workflows, error)
}

// Resolve returns the Workflows for the task.
func (r *workflowResolver) Resolve(ctx context.Context, task *domain.Task) (*orchestrator.Workflows, error) {
	// The shared instance is reused across the process, so it is not closed.
	if r.build == nil || (!r.usesCustomModels(task) && !usesSeedOverride(task) && !usesVeoOptions(task)) {
		return r.shared, nil
	}
	workflows, err := r.build(ctx, task)
	if err != nil {
		return nil, fmt.Errorf("build workflow for selected models: %w", err)
	}
	return workflows, nil
}

// usesCustomModels reports whether the task names a model other than the default.
func (r *workflowResolver) usesCustomModels(task *domain.Task) bool {
	if task == nil {
		return false
	}
	return !r.orchCfg.UsesModels(task.TextModel, task.ImageModel)
}

// usesSeedOverride reports whether the task asks for a temporary character seed override.
func usesSeedOverride(task *domain.Task) bool {
	return task != nil && task.SeedOverride != nil && task.SeedOverrideCharacterID != ""
}

// usesVeoOptions reports whether the task asks to replace the Veo model or the aspect ratio.
// When it does, task-specific Workflows with the replaced VideoRunner must be built instead of the
// shared Workflows.
func usesVeoOptions(task *domain.Task) bool {
	if task == nil {
		return false
	}
	return strings.TrimSpace(task.VeoModel) != "" || strings.TrimSpace(task.VeoAspectRatio) != ""
}

func taskVisualMode(task *domain.Task) string {
	if task == nil {
		return ""
	}
	return task.VisualMode
}

// taskSeedOverride converts the task's character seed override into a characterSeedOverride.
// With none it returns nil, and buildWorkflowWithConfig uses the default character definitions as-is.
func taskSeedOverride(task *domain.Task) *characterSeedOverride {
	if task == nil || task.SeedOverride == nil || task.SeedOverrideCharacterID == "" {
		return nil
	}
	return &characterSeedOverride{
		characterID: task.SeedOverrideCharacterID,
		seed:        *task.SeedOverride,
	}
}
