package builder

import (
	"context"
	"fmt"

	"github.com/shouni/gcp-kit/tasks"

	"github.com/shouni/ap-mv/internal/config"
	"github.com/shouni/ap-mv/internal/domain"
)

// buildTaskEnqueuer initialises the Cloud Tasks enqueuer.
func buildTaskEnqueuer(ctx context.Context, cfg *config.Config) (*tasks.Enqueuer[domain.Task], error) {

	taskCfg := tasks.Config{
		ProjectID:  cfg.GCP.ProjectID,
		LocationID: cfg.GCP.LocationID,
		QueueID:    cfg.Tasks.QueueID,
		WorkerURL:  cfg.Tasks.WorkerURL,
		WorkerPath: domain.WorkerTaskPath,
		// The caller SA set on tasks. Cloud Tasks generates and attaches the token; this process does
		// not sign anything. It is distinct from the receiver's allowlist
		// (Tasks.AllowedServiceAccounts), so do not mix them up.
		ServiceAccountEmail: cfg.TaskCallerServiceAccount(),
		Audience:            cfg.Tasks.TaskAudienceURL,
		// The effective limit on the worker's run time. Unset, Cloud Tasks' default of 10 minutes applies,
		// and the request is cut there however long the worker's Cloud Run timeout is.
		// Self-enqueued continuation cuts go through this enqueuer too, so each cut gets the same limit.
		// The check keeping PIPELINE_TIMEOUT shorter than this is in config.ValidateEssentialConfig.
		DispatchDeadline: cfg.Tasks.DispatchDeadline,
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
