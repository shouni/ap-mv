package builder

import (
	"context"
	"fmt"
	"io"

	"github.com/shouni/go-http-kit/httpkit"
	"github.com/shouni/go-remote-io/remoteio/gcs"

	"ap-mv/internal/adapters"
	"ap-mv/internal/app"
	"ap-mv/internal/config"
	"ap-mv/internal/repository"
)

// BuildContainer は外部サービスとの接続を確立し、依存関係を組み立てた app.Container を返します。
func BuildContainer(ctx context.Context, cfg *config.Config) (container *app.Container, err error) {
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

	videoRunner, err := adapters.NewVertexVeoRunner(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize video runner: %w", err)
	}
	// resources is only for cleanup while BuildContainer is still assembling dependencies.
	// On success, app.Container.Close owns the runtime lifecycle.
	resources = append(resources, videoRunner)

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
	pipe, err := buildPipeline(ctx, cfg, rio, httpClient, videoRunner)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize worker pipeline: %w", err)
	}
	notifier, err := adapters.NewSlackAdapter(httpClient, cfg.SlackWebhookURL, cfg.ServiceURL)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize slack notifier: %w", err)
	}
	pipe.Notifier = notifier
	queue := taskQueueAdapter{enqueuer: enqueuer}
	pipe.TaskQueue = queue
	historyRepository := repository.NewVideoHistoryRepository(
		workflowOutputBaseURI(cfg),
		rio.Reader,
		rio.Writer,
		rio.Signer,
		nil,
	)

	return &app.Container{
		Config:            cfg,
		RemoteIO:          rio,
		HTTPClient:        httpClient,
		TaskEnqueuer:      enqueuer,
		TaskQueue:         queue,
		Pipeline:          pipe,
		HistoryRepository: historyRepository,
	}, nil
}
