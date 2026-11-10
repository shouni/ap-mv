// Package builder provides the factory functions that assemble each service client, handler and
// the pipeline's dependencies from the configuration.
package builder

import (
	"context"
	"fmt"
	"io"

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

// BuildContainer establishes connections to external services and returns an app.Container with the dependencies assembled.
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
	// resources is for rolling back during assembly; nobody looks at it after a successful return.
	// Releasing at run time is the job of closers (app.Container.Close), so resources that live on
	// after success go into both. The storage's lifetime belongs to the factory.
	closers := []io.Closer{storage}

	enqueuer, err := buildTaskEnqueuer(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize task enqueuer: %w", err)
	}
	resources = append(resources, enqueuer)
	closers = append(closers, enqueuer)

	// Sessions live in a different database from job status (SessionDatabase).
	// It does not branch on role because this file does not for other resources either.
	// The store owns the client's lifetime, so the store is registered in Closers.
	sessionStore, err := session.OpenFirestoreStore(ctx, cfg.GCP.ProjectID, cfg.Auth.SessionDatabase, cfg.Auth.SessionCollection)
	if err != nil {
		return nil, fmt.Errorf("failed to build the session store: %w", err)
	}
	resources = append(resources, sessionStore)
	closers = append(closers, sessionStore)

	httpClient := httpkit.New()
	queue := taskQueueAdapter{enqueuer: enqueuer}
	// The web process writes queued at submission and the worker process writes the run's result.
	// Unlike the artifacts it lives in Firestore, so the history prefix delete does not remove it
	// (removing it is the handler's job; see ports.JobStatusStore.Delete).
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

	// The listing is a job status query; the detail reads the metadata stored alongside the artifacts.
	historyRepository := repository.NewVideoHistoryRepository(repository.VideoHistoryRepositoryConfig{
		BaseURI:   workflowOutputBaseURI(cfg),
		Store:     store,
		JobStatus: jobStatus,
	})

	// Only the worker face builds the generation side (Vertex AI, Veo, Slack notifications, the pipeline).
	// Not building it on the web face matches the infrastructure's premise that ap-mv-web-runner has
	// neither aiplatform.user nor access to SLACK_WEBHOOK_URL (there is no reason to grant them, and
	// granting them would weaken the point of the split).
	//
	// While pipe stays nil, neither Container.Close nor BuildHandlers looks at it.
	// It is assigned only when built, so as not to create a non-nil interface holding nil.
	var pipe ports.Pipeline
	if cfg.Server.Role.ServesWorker() {
		// Build the Vertex AI client once here and pass it to both video generation and text/image
		// generation. The two used to create their own, leaving room for the region or retry settings to
		// change on one side only and disagree.
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
		// videoRunner is closed by Pipeline.Close, so it is not put in closers
		// (that would close it twice).
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
