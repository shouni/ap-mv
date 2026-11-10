package ports

import (
	"context"

	"github.com/shouni/ap-mv/internal/domain"
)

// TaskQueue is the boundary separating web intake from the asynchronous execution platform.
type TaskQueue interface {
	Enqueue(ctx context.Context, task *domain.Task) error
	// EnqueueWithName enqueues a task under a deterministic name derived from taskID.
	// Calling it several times with the same taskID creates only one task (Cloud Tasks rejects the
	// second and later with ALREADY_EXISTS, and the implementation treats that as success).
	// Use it when the same logical task must not be created twice, e.g. on caller retries.
	EnqueueWithName(ctx context.Context, taskID string, task *domain.Task) error
}
