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
	_ context.Context,
	cfg *config.Config,
	_ *app.RemoteIO,
	_ httpkit.HTTPClient,
	videoRunner ports.VideoRunner,
) *pipeline.Runner {
	return pipeline.New(videoRunner, buildOrchestratorConfig(cfg))
}
