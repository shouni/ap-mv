package builder

import (
	"context"
	"fmt"
	"io"

	"github.com/shouni/go-http-kit/httpkit"
	"github.com/shouni/go-remote-io/remoteio/gcs"

	"ap-mv/internal/app"
	"ap-mv/internal/config"
	"ap-mv/internal/ports"
)

// BuildContainer は外部サービスとの接続を確立し、依存関係を組み立てた app.Container を返します。
func BuildContainer(ctx context.Context, cfg *config.Config, videoRunner ports.VideoRunner) (container *app.Container, err error) {
	var resources []io.Closer
	defer func() {
		if err != nil {
			for _, r := range resources {
				if r != nil {
					_ = r.Close()
				}
			}
		}
	}()

	storage, err := gcs.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCS factory: %w", err)
	}
	resources = append(resources, storage)

	rio, err := buildRemoteIO(storage)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize IO components: %w", err)
	}

	enqueuer, err := buildTaskEnqueuer(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize task enqueuer: %w", err)
	}
	resources = append(resources, enqueuer)

	httpClient := httpkit.New(httpkit.DefaultHTTPTimeout)
	pipe := buildPipeline(ctx, cfg, rio, httpClient, videoRunner)
	queue := taskQueueAdapter{enqueuer: enqueuer}

	return &app.Container{
		Config:       cfg,
		RemoteIO:     rio,
		HTTPClient:   httpClient,
		TaskEnqueuer: enqueuer,
		TaskQueue:    queue,
		Pipeline:     pipe,
	}, nil
}
