package ports

import (
	"context"

	"github.com/shouni/ap-mv/internal/domain"
)

// JobStatusStore は、ジョブの進行状況（queued/running/succeeded/failed）を永続化します。
// Web プロセスが投入時の状態を、Worker プロセスが実行結果を書き込みます。
//
// Save / Get のシグネチャは go-job-kit の jobstatus.StatusStore に揃えてあります。
// これにより *jobstatus.Store[domain.JobStatus] がそのまま実装となり、jobstatus.Recorder へ
// 渡すためのアダプタが要りません（以前はジョブ ID を状態に含める形だったため、
// 形を合わせるだけのアダプタを 3 サービスがそれぞれ持っていました）。
type JobStatusStore interface {
	// Save はジョブ状態を保存します。
	Save(ctx context.Context, jobID string, status domain.JobStatus) error
	// Get はジョブ状態を取得します。未記録の場合はエラーを返します。
	Get(ctx context.Context, jobID string) (domain.JobStatus, error)
}
