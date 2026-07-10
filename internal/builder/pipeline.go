package builder

import (
	"context"
	"fmt"

	"github.com/shouni/go-http-kit/httpkit"
	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"github.com/shouni/ap-mv/internal/app"
	"github.com/shouni/ap-mv/internal/config"
	"github.com/shouni/ap-mv/internal/domain"
	"github.com/shouni/ap-mv/internal/ports"
	"github.com/shouni/ap-mv/internal/worker/pipeline"
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
		taskVideoRunner := videoRunner
		if task != nil {
			orchCfg = orchCfg.WithModels(task.TextModel, task.ImageModel)
			taskVideoRunner = ports.DeriveVideoRunner(videoRunner, task.VeoModel, task.VeoAspectRatio)
		}
		return buildWorkflowWithConfig(ctx, cfg, orchCfg, rio, httpClient, taskVideoRunner, taskVisualMode(task), taskSeedOverride(task))
	}
	if rio != nil {
		runner.Reader = workflowReader{delegate: rio.Reader}
		runner.Writer = rio.Writer
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

// taskSeedOverride は、タスクに指定されたキャラクターシード上書きを characterSeedOverride に変換します。
// 指定がなければ nil を返し、buildWorkflowWithConfig は既定のキャラクター定義をそのまま使います。
func taskSeedOverride(task *domain.Task) *characterSeedOverride {
	if task == nil || task.SeedOverride == nil || task.SeedOverrideCharacterID == "" {
		return nil
	}
	return &characterSeedOverride{
		characterID: task.SeedOverrideCharacterID,
		seed:        *task.SeedOverride,
	}
}
