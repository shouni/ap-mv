package builder

import (
	"context"
	"fmt"

	"github.com/shouni/gcp-kit/tasks"

	"ap-mv/internal/config"
	"ap-mv/internal/domain"
)

// buildTaskEnqueuer は、Cloud Tasks エンキューアを初期化します。
func buildTaskEnqueuer(ctx context.Context, cfg *config.Config) (*tasks.Enqueuer[domain.Task], error) {
	taskCfg := tasks.Config{
		ProjectID:           cfg.ProjectID,
		LocationID:          cfg.LocationID,
		QueueID:             cfg.QueueID,
		WorkerURL:           cfg.WorkerURL,
		ServiceAccountEmail: cfg.ServiceAccountEmail,
		Audience:            cfg.TaskAudienceURL,
	}
	return tasks.NewEnqueuer[domain.Task](ctx, taskCfg)
}

type taskQueueAdapter struct {
	enqueuer *tasks.Enqueuer[domain.Task]
}

// Enqueue adds a task to the queue.
func (q taskQueueAdapter) Enqueue(ctx context.Context, task *domain.Task) error {
	if task == nil {
		return fmt.Errorf("task is nil")
	}
	if q.enqueuer == nil {
		return fmt.Errorf("cloud tasks enqueuer is not configured")
	}
	return q.enqueuer.Enqueue(ctx, *task)
}
