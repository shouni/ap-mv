package ports

import (
	"context"

	"github.com/shouni/ap-mv/internal/domain"
)

// JobStatusStore は、ジョブの進行状況（queued/running/succeeded/failed）を永続化します。
// Web プロセスが投入時の状態を、Worker プロセスが実行結果を書き込みます。
type JobStatusStore interface {
	// Save はジョブ状態を保存します。
	Save(ctx context.Context, status domain.JobStatus) error
	// Get はジョブ状態を取得します。未記録の場合はエラーを返します。
	Get(ctx context.Context, jobID string) (*domain.JobStatus, error)
}
