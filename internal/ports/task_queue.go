package ports

import (
	"context"

	"ap-mv/internal/domain"
)

// TaskQueue はWeb受付と非同期実行基盤を分離する境界です。
type TaskQueue interface {
	Enqueue(ctx context.Context, task *domain.Task) error
}

// InlineTaskQueue は開発用の同期実行キューです。
type InlineTaskQueue struct {
	Handler func(context.Context, *domain.Task) error
}

// Enqueue adds a task to the queue.
func (q InlineTaskQueue) Enqueue(ctx context.Context, task *domain.Task) error {
	if q.Handler == nil {
		return nil
	}
	return q.Handler(ctx, task)
}
