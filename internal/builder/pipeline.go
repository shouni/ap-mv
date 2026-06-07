package builder

import (
	"context"

	"github.com/shouni/go-http-kit/httpkit"

	"ap-mv/internal/app"
	"ap-mv/internal/config"
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
	runner.OutputBaseURI = workflowOutputBaseURI(cfg)
	return runner, nil
}
