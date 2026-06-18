package ports

import (
	"context"

	"ap-mv/internal/domain"
)

// Notifier sends asynchronous pipeline completion and error notifications.
type Notifier interface {
	NotifyTaskComplete(ctx context.Context, req domain.NotificationRequest) error
	NotifyTaskError(ctx context.Context, errDetail error, req domain.NotificationRequest) error
}
