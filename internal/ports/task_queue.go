package ports

import (
	"context"

	"github.com/shouni/ap-mv/internal/domain"
)

// TaskQueue はWeb受付と非同期実行基盤を分離する境界です。
type TaskQueue interface {
	Enqueue(ctx context.Context, task *domain.Task) error
}
