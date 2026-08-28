package repository

import (
	"github.com/shouni/go-job-kit/jobstatus"
	"github.com/shouni/go-remote-io/remoteio"

	"github.com/shouni/ap-mv/internal/domain"
)

// NewJobStatusRepository は、GCS を裏付けとしたジョブ進行状況の読み書きを構築します。
//
// 保存形式・ジョブ ID の正規化・キャッシュ抑止（no-store）は go-job-kit の jobstatus が
// 担います。ここが与えるのは「ジョブ出力ディレクトリ配下に置く」という配置だけです。
// 履歴削除（プレフィックス一括削除）で状態ファイルも自動的に片付き、履歴一覧は
// video_music_meta.json だけを拾うため一覧には混ざりません。
//
// ports.JobStatusStore は jobstatus.Store と同じシグネチャなので、包む型は要りません。
// 状態ファイルは常に最新の 1 世代だけを保持し、上書きで更新します。
func NewJobStatusRepository(baseURI string, store remoteio.Store) *jobstatus.Store[domain.JobStatus] {
	return jobstatus.NewStore[domain.JobStatus](store, jobstatus.UnderJobDir(baseURI))
}
