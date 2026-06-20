package builder

import (
	"context"
	"fmt"

	"github.com/shouni/go-http-kit/httpkit"
	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"ap-mv/internal/app"
	"ap-mv/internal/config"
	"ap-mv/internal/domain"
	"ap-mv/internal/ports"
	"ap-mv/internal/worker/pipeline"
)

// buildPipeline は、パイプラインの実行に必要な境界実装を注入して返します。
func buildPipeline(
	ctx context.Context,
	cfg *config.Config,
	rio *app.RemoteIO,
	httpClient httpkit.HTTPClient,
	videoRunner ports.VideoRunner,
) (*pipeline.Runner, error) {
	runner := pipeline.New(videoRunner, buildOrchestratorConfig(cfg))
	workflows, err := buildWorkflow(ctx, cfg, rio, httpClient, videoRunner)
	if err != nil {
		return nil, err
	}
	runner.Workflows = workflows
	characters, err := buildCharacters()
	if err != nil {
		return nil, fmt.Errorf("load characters for pipeline: %w", err)
	}
	runner.Characters = characters
	runner.WorkflowFactory = func(ctx context.Context, task *domain.Task) (*orchestrator.Workflows, error) {
		orchCfg := runner.OrchestratorConfig
		if task != nil {
			orchCfg = orchCfg.WithModels(task.TextModel, task.ImageModel)
		}
		return buildWorkflowWithConfig(ctx, cfg, orchCfg, rio, httpClient, videoRunner, taskVisualMode(task))
	}
	if rio != nil {
		runner.Reader = workflowReader{delegate: rio.Reader}
	}
	runner.OutputBaseURI = workflowOutputBaseURI(cfg)
	return runner, nil
}

func taskVisualMode(task *domain.Task) string {
	if task == nil {
		return ""
	}
	return task.VisualMode
}
