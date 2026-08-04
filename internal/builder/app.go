// Package builder は、設定値から各サービスクライアント・ハンドラー・パイプラインの
// 依存関係を組み立てるファクトリ関数を提供します。
package builder

import (
	"context"
	"fmt"
	"io"

	"github.com/shouni/go-http-kit/httpkit"
	"github.com/shouni/go-remote-io/remoteio/gcs"

	"github.com/shouni/ap-mv/internal/adapters"
	"github.com/shouni/ap-mv/internal/app"
	"github.com/shouni/ap-mv/internal/config"
	"github.com/shouni/ap-mv/internal/repository"
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

	// Vertex AI クライアントはここで一度だけ組み、動画生成とテキスト/画像生成の
	// 双方へ渡す。以前は両者が別々に生成しており、リージョンやリトライ設定が
	// 片方だけ変更されて食い違う余地があった。
	aiClient, err := adapters.NewVertexAIAdapter(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize Vertex AI client: %w", err)
	}

	videoRunner, err := adapters.NewVertexVeoRunner(ctx, cfg, aiClient)
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
	notifier, err := adapters.NewSlackAdapter(httpClient.WithoutRetry(), cfg.Notification.SlackWebhookURL, cfg.Server.ServiceURL)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize slack notifier: %w", err)
	}
	queue := taskQueueAdapter{enqueuer: enqueuer}
	historyRepository := repository.NewVideoHistoryRepository(repository.VideoHistoryRepositoryConfig{
		BaseURI:      workflowOutputBaseURI(cfg),
		DraftBaseURI: workflowDraftBaseURI(cfg),
		Reader:       rio.Reader,
		Writer:       rio.Writer,
		Signer:       rio.Signer,
	})
	// Web プロセスは投入時の queued を、Worker プロセスは実行結果を書き込みます。
	jobStatus := repository.NewJobStatusRepository(workflowOutputBaseURI(cfg), rio.Reader, rio.Writer)
	pipe, err := buildPipeline(ctx, cfg, rio, httpClient, videoRunner, aiClient, pipelineExternals{
		notifier:          notifier,
		taskQueue:         queue,
		historyRepository: historyRepository,
		jobStatus:         jobStatus,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to initialize worker pipeline: %w", err)
	}

	return &app.Container{
		Config:            cfg,
		RemoteIO:          rio,
		HTTPClient:        httpClient,
		TaskEnqueuer:      enqueuer,
		TaskQueue:         queue,
		Pipeline:          pipe,
		HistoryRepository: historyRepository,
		DraftRepository:   historyRepository,
		JobStatus:         jobStatus,
	}, nil
}
