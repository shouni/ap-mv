package ports

import (
	"context"

	"github.com/shouni/go-job-firestore/jobfirestore"

	"github.com/shouni/ap-mv/internal/domain"
)

// JobStatusStore は、ジョブの進行状況（queued/running/succeeded/failed）を永続化し、
// 履歴一覧のクエリを担います。
// Web プロセスが投入時の状態を、Worker プロセスが実行結果を書き込みます。
//
// 一覧がここにあるのは、履歴の見出しを状態ドキュメントへ写したからです。以前は成果物の
// 置き場を走査して 1 件ずつメタデータを開いていたので、一覧は成果物側の関心でした。
//
// Save / Get / Delete のシグネチャは go-job-firestore の Store に揃えてあります。
// これにより *jobfirestore.Store[domain.JobStatus] がそのまま実装となり、Recorder へ
// 渡すためのアダプタが要りません（以前はジョブ ID を状態に含める形だったため、
// 形を合わせるだけのアダプタを 3 サービスがそれぞれ持っていました）。
type JobStatusStore interface {
	// Save はジョブ状態を保存します。
	Save(ctx context.Context, jobID string, status domain.JobStatus) error
	// Get はジョブ状態を取得します。未記録の場合はエラーを返します。
	Get(ctx context.Context, jobID string) (domain.JobStatus, error)
	// Delete はジョブ状態を削除します。
	//
	// 状態は成果物と別の場所（Firestore）にあるため、履歴のプレフィックス一括削除では
	// 消えません。呼ばないと、成果物の無いジョブの状態だけが残り続けます。
	Delete(ctx context.Context, jobID string) error
	// List はジョブ状態を新しい順に 1 ページ分返します。
	// 総件数は集計クエリで求めるため、ページの外にあるドキュメントは読みません。
	List(ctx context.Context, page, perPage int, opts ...jobfirestore.ListOption) ([]domain.JobStatus, domain.PageMeta, error)
}
