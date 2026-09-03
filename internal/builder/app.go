// Package builder は、設定値から各サービスクライアント・ハンドラー・パイプラインの
// 依存関係を組み立てるファクトリ関数を提供します。
package builder

import (
	"context"
	"fmt"
	"io"

	"cloud.google.com/go/firestore"
	"github.com/shouni/gcp-kit/auth/session"
	"github.com/shouni/gcp-kit/jobstatus"
	"github.com/shouni/go-http-kit/httpkit"
	"github.com/shouni/go-remote-io/remoteio/gcs"

	"github.com/shouni/ap-mv/internal/adapters"
	"github.com/shouni/ap-mv/internal/app"
	"github.com/shouni/ap-mv/internal/config"
	"github.com/shouni/ap-mv/internal/ports"
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

	storage, err := gcs.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCS factory: %w", err)
	}
	resources = append(resources, storage)

	store, err := storage.Store()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize IO components: %w", err)
	}
	// resources は組み立て中の巻き戻し用で、成功して返ったあとは誰も見ません。
	// 実行中の解放は closers（app.Container.Close）が受け持つため、成功後も
	// 生き続ける資源は両方へ入れます。ストレージの寿命はファクトリが持ちます。
	closers := []io.Closer{storage}

	enqueuer, err := buildTaskEnqueuer(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize task enqueuer: %w", err)
	}
	resources = append(resources, enqueuer)
	closers = append(closers, enqueuer)

	// セッションはジョブ状態とは別のデータベースに置きます（SessionDatabase）。
	// 役割で分岐しないのは、このファイルが他の資源もそうしているためです。
	sessionFirestore, err := firestore.NewClientWithDatabase(ctx, cfg.GCP.ProjectID, cfg.Auth.SessionDatabase)
	if err != nil {
		return nil, fmt.Errorf("セッション用 Firestore の初期化に失敗しました: %w", err)
	}
	resources = append(resources, sessionFirestore)
	closers = append(closers, sessionFirestore)

	sessionStore, err := session.NewFirestoreStore(session.FirestoreConfig{
		Client:     sessionFirestore,
		Collection: cfg.Auth.SessionCollection,
	})
	if err != nil {
		return nil, fmt.Errorf("セッションストアの構築に失敗しました: %w", err)
	}

	httpClient := httpkit.New()
	queue := taskQueueAdapter{enqueuer: enqueuer}
	// Web プロセスは投入時の queued を、Worker プロセスは実行結果を書き込みます。
	// 成果物と違って Firestore に置くため、履歴のプレフィックス削除では消えません
	// （消すのはハンドラーの仕事です。ports.JobStatusStore.Delete を参照）。
	firestoreFactory, err := jobstatus.New(ctx,
		jobstatus.WithProjectID(cfg.GCP.ProjectID),
		jobstatus.WithDatabase(cfg.Storage.FirestoreDatabase),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize Firestore: %w", err)
	}
	resources = append(resources, firestoreFactory)
	closers = append(closers, firestoreFactory)

	firestoreClient, err := firestoreFactory.Client()
	if err != nil {
		return nil, fmt.Errorf("failed to obtain Firestore client: %w", err)
	}
	jobStatus := repository.NewJobStatusRepository(firestoreClient)

	// 一覧はジョブ状態のクエリ、詳細は成果物と同じ場所のメタデータ読み込みです。
	historyRepository := repository.NewVideoHistoryRepository(repository.VideoHistoryRepositoryConfig{
		BaseURI:   workflowOutputBaseURI(cfg),
		Store:     store,
		JobStatus: jobStatus,
	})

	// 生成系（Vertex AI・Veo・Slack 通知・パイプライン）を組み立てるのは Worker 面だけです。
	// Web 面で組み立てないことで、ap-mv-web-runner が aiplatform.user も
	// SLACK_WEBHOOK_URL へのアクセス権も持たないという ap-infra 側の前提と一致します
	// （持たせる理由が無く、持たせれば分離した意味が薄れます）。
	//
	// pipe は nil のままなら Container.Close も BuildHandlers も参照しません。
	// 非 nil のインターフェースが nil を保持する状態を作らないよう、組み立てたときにだけ代入します。
	var pipe ports.Pipeline
	if cfg.Server.Role.ServesWorker() {
		// Vertex AI クライアントはここで一度だけ組み、動画生成とテキスト/画像生成の
		// 双方へ渡す。以前は両者が別々に生成しており、リージョンやリトライ設定が
		// 片方だけ変更されて食い違う余地があった。
		aiClient, aiErr := adapters.NewVertexAIAdapter(ctx, cfg)
		if aiErr != nil {
			return nil, fmt.Errorf("failed to initialize Vertex AI client: %w", aiErr)
		}

		videoRunner, runnerErr := adapters.NewVertexVeoRunner(cfg, aiClient, store)
		if runnerErr != nil {
			return nil, fmt.Errorf("failed to initialize video runner: %w", runnerErr)
		}
		// resources is only for cleanup while BuildContainer is still assembling dependencies.
		// On success, app.Container.Close owns the runtime lifecycle (Pipeline.Close closes it).
		resources = append(resources, videoRunner)

		notifier, notifierErr := adapters.NewSlackAdapter(httpClient.WithoutRetry(), cfg.Notification.SlackWebhookURL, cfg.Server.ServiceURL)
		if notifierErr != nil {
			return nil, fmt.Errorf("failed to initialize slack notifier: %w", notifierErr)
		}

		builtPipe, pipeErr := buildPipeline(ctx, cfg, store, httpClient, videoRunner, aiClient, pipelineExternals{
			notifier:          notifier,
			taskQueue:         queue,
			historyRepository: historyRepository,
			jobStatus:         jobStatus,
		})
		if pipeErr != nil {
			return nil, fmt.Errorf("failed to initialize worker pipeline: %w", pipeErr)
		}
		pipe = builtPipe
		// videoRunner は Pipeline.Close が閉じるため、closers へは入れません
		// （入れると二重に閉じます）。
		closers = append(closers, builtPipe)
	}

	return &app.Container{
		Config:            cfg,
		Storage:           storage,
		Store:             store,
		HTTPClient:        httpClient,
		TaskEnqueuer:      enqueuer,
		TaskQueue:         queue,
		SessionStore:      sessionStore,
		Pipeline:          pipe,
		HistoryRepository: historyRepository,
		JobStatus:         jobStatus,
		Closers:           closers,
	}, nil
}
