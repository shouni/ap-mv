package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-utils/jobid"

	"github.com/shouni/ap-mv/internal/domain"
)

// ErrJobStatusNotFound は、ジョブ状態がまだ記録されていないことを表します。
// 「状態が無い」は正常な状態（記録前の投入や、この機能より前に作られたジョブ）なので、
// 呼び出し側がストレージ障害と区別できるよう独立したエラーにしています。
var ErrJobStatusNotFound = errors.New("job status not found")

const (
	// jobStatusFile はジョブ進行状況を記録するオブジェクト名です。
	// ジョブ出力ディレクトリ配下に置くため、履歴削除（プレフィックス一括削除）で
	// 自動的に片付きます。履歴一覧は video_music_meta.json だけを拾うので混ざりません。
	jobStatusFile = "status.json"
	// jobStatusContentType はジョブ状態 JSON の Content-Type です。
	jobStatusContentType = "application/json; charset=utf-8"
)

// JobStatusRepository は、GCS を裏付けとしたジョブ進行状況の読み書きを行います。
type JobStatusRepository struct {
	baseURI string
	reader  remoteio.InputReader
	writer  remoteio.OutputWriter
	now     func() time.Time
}

// NewJobStatusRepository は、GCS を裏付けとした JobStatusRepository を構築します。
func NewJobStatusRepository(baseURI string, reader remoteio.InputReader, writer remoteio.OutputWriter) *JobStatusRepository {
	return &JobStatusRepository{
		baseURI: strings.TrimRight(strings.TrimSpace(baseURI), "/"),
		reader:  reader,
		writer:  writer,
		now:     func() time.Time { return time.Now().UTC() },
	}
}

// Save はジョブ状態を保存します。
// 状態ファイルは常に最新の 1 世代だけを保持し、上書きで更新します。
func (r *JobStatusRepository) Save(ctx context.Context, status domain.JobStatus) error {
	safeJobID, err := jobid.Sanitize(status.JobID)
	if err != nil {
		return err
	}
	if r.writer == nil || r.baseURI == "" {
		return fmt.Errorf("job status writer is not configured")
	}

	status.JobID = safeJobID
	status.UpdatedAt = r.now()

	data, err := json.Marshal(status)
	if err != nil {
		return fmt.Errorf("marshal job status: %w", err)
	}

	uri := r.statusURI(safeJobID)
	if err := r.writer.Write(ctx, uri, bytes.NewReader(data),
		remoteio.WithContentType(jobStatusContentType),
		// 状態は頻繁に変わるため、CDN・ブラウザにキャッシュさせない。
		remoteio.WithCacheControl("no-store"),
	); err != nil {
		return fmt.Errorf("write job status (%s): %w", uri, err)
	}

	return nil
}

// Get はジョブ状態を取得します。未記録の場合は ErrJobStatusNotFound を返します。
func (r *JobStatusRepository) Get(ctx context.Context, jobID string) (*domain.JobStatus, error) {
	safeJobID, err := jobid.Sanitize(jobID)
	if err != nil {
		return nil, err
	}
	if r.reader == nil || r.baseURI == "" {
		return nil, fmt.Errorf("job status reader is not configured")
	}

	rc, err := r.reader.Open(ctx, r.statusURI(safeJobID))
	if err != nil {
		// remoteio は「未存在」を型付きで返さないため、読めなかった時点で未記録とみなします。
		// 状態の欠落で処理を止めるより、記録が無いものとして先へ進めるほうが安全です。
		return nil, fmt.Errorf("%w: %s", ErrJobStatusNotFound, safeJobID)
	}
	defer rc.Close()

	var status domain.JobStatus
	if err := json.NewDecoder(rc).Decode(&status); err != nil {
		return nil, fmt.Errorf("decode job status (%s): %w", safeJobID, err)
	}
	if status.JobID == "" {
		status.JobID = safeJobID
	}

	return &status, nil
}

func (r *JobStatusRepository) statusURI(jobID string) string {
	return r.baseURI + "/" + jobID + "/" + jobStatusFile
}
