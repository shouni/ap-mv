package builder

import (
	"context"
	"fmt"
	"net/url"

	"github.com/shouni/gcp-kit/tasks"

	"ap-mv/internal/config"
	"ap-mv/internal/domain"
)

// buildTaskEnqueuer は、Cloud Tasks エンキューアを初期化します。
func buildTaskEnqueuer(ctx context.Context, cfg *config.Config) (*tasks.Enqueuer[domain.Task], error) {
	workerURL, err := url.JoinPath(cfg.ServiceURL, "/tasks/generate")
	if err != nil {
		return nil, fmt.Errorf("failed to build worker URL: %w", err)
	}

	taskCfg := tasks.Config{
		ProjectID:           cfg.ProjectID,
		LocationID:          cfg.LocationID,
		QueueID:             cfg.QueueID,
		WorkerURL:           workerURL,
		ServiceAccountEmail: cfg.ServiceAccountEmail,
		Audience:            cfg.TaskAudienceURL,
	}
	return tasks.NewEnqueuer[domain.Task](ctx, taskCfg)
}

type taskQueueAdapter struct {
	enqueuer *tasks.Enqueuer[domain.Task]
}

func (q taskQueueAdapter) Enqueue(ctx context.Context, task *domain.Task) error {
	if task == nil {
		return fmt.Errorf("task is nil")
	}
	if q.enqueuer == nil {
		return fmt.Errorf("cloud tasks enqueuer is not configured")
	}
	return q.enqueuer.Enqueue(ctx, *task)
}
