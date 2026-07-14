package ports

import (
	"context"

	"github.com/shouni/ap-mv/internal/domain"
)

// TaskQueue はWeb受付と非同期実行基盤を分離する境界です。
type TaskQueue interface {
	Enqueue(ctx context.Context, task *domain.Task) error
	// EnqueueWithName は taskID から導出した決定的な名前でタスクを投入します。
	// 同じ taskID で複数回呼び出しても、実際に作られるタスクは1つだけです
	// （Cloud Tasks が2回目以降を ALREADY_EXISTS で拒否し、実装側はそれを成功として扱います）。
	// 呼び出し元の再試行等で同じ論理的なタスクが重複して作られてはいけない場合に使います。
	EnqueueWithName(ctx context.Context, taskID string, task *domain.Task) error
}
