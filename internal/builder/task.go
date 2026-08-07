package builder

import (
	"context"
	"fmt"

	"github.com/shouni/gcp-kit/tasks"

	"github.com/shouni/ap-mv/internal/config"
	"github.com/shouni/ap-mv/internal/domain"
)

// buildTaskEnqueuer は、Cloud Tasks エンキューアを初期化します。
func buildTaskEnqueuer(ctx context.Context, cfg *config.Config) (*tasks.Enqueuer[domain.Task], error) {
	taskCfg := tasks.Config{
		ProjectID:  cfg.GCP.ProjectID,
		LocationID: cfg.GCP.LocationID,
		QueueID:    cfg.Tasks.QueueID,
		WorkerURL:  cfg.Tasks.WorkerURL,
		// タスクに指定する caller SA。トークンを生成して付与するのは Cloud Tasks で、
		// このプロセスが署名するわけではありません。受信側の許可リスト
		// （Tasks.AllowedServiceAccounts）とは別物なので取り違えないこと。
		ServiceAccountEmail: cfg.TaskCallerServiceAccount(),
		Audience:            cfg.Tasks.TaskAudienceURL,
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

// EnqueueWithName adds a task to the queue under a deterministic name, so retried calls with
// the same taskID do not create duplicate tasks.
func (q taskQueueAdapter) EnqueueWithName(ctx context.Context, taskID string, task *domain.Task) error {
	if task == nil {
		return fmt.Errorf("task is nil")
	}
	if q.enqueuer == nil {
		return fmt.Errorf("cloud tasks enqueuer is not configured")
	}
	return q.enqueuer.EnqueueWithName(ctx, taskID, *task)
}
