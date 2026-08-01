package repository

import (
	"context"

	"github.com/shouni/go-job-kit/jobstatus"
	"github.com/shouni/go-remote-io/remoteio"

	"github.com/shouni/ap-mv/internal/domain"
)

// ErrJobStatusNotFound は、ジョブ状態がまだ記録されていないことを表します。
// 「状態が無い」は正常な状態（記録前の投入や、この機能より前に作られたジョブ）なので、
// 呼び出し側がストレージ障害と区別できるよう独立したエラーにしています。
var ErrJobStatusNotFound = jobstatus.ErrNotFound

// JobStatusRepository は、GCS を裏付けとしたジョブ進行状況の読み書きを行います。
//
// 保存形式・ジョブ ID の正規化・キャッシュ抑止（no-store）は go-job-kit の jobstatus が
// 担います。ここが与えるのは「ジョブ出力ディレクトリ配下に置く」という配置だけです。
// 履歴削除（プレフィックス一括削除）で状態ファイルも自動的に片付き、履歴一覧は
// video_music_meta.json だけを拾うため一覧には混ざりません。
type JobStatusRepository struct {
	store *jobstatus.Store[domain.JobStatus]
}

// NewJobStatusRepository は、GCS を裏付けとした JobStatusRepository を構築します。
func NewJobStatusRepository(baseURI string, reader remoteio.InputReader, writer remoteio.OutputWriter) *JobStatusRepository {
	return &JobStatusRepository{
		store: jobstatus.NewStore[domain.JobStatus](reader, writer, jobstatus.UnderJobDir(baseURI)),
	}
}

// Save はジョブ状態を保存します。
// 状態ファイルは常に最新の 1 世代だけを保持し、上書きで更新します。
func (r *JobStatusRepository) Save(ctx context.Context, status domain.JobStatus) error {
	return r.store.Save(ctx, status.JobID, status)
}

// Get はジョブ状態を取得します。未記録の場合は ErrJobStatusNotFound を返します。
func (r *JobStatusRepository) Get(ctx context.Context, jobID string) (*domain.JobStatus, error) {
	status, err := r.store.Get(ctx, jobID)
	if err != nil {
		return nil, err
	}
	return &status, nil
}
