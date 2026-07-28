// Package ports は、ap-mv の各コンポーネントが依存するインターフェース（ポート）を定義します。
package ports

import (
	"context"
	"io"

	"github.com/shouni/ap-mv/internal/domain"
)

// KeyframeSink はキーフレームダウンロード時に各ファイルごとに呼ばれるコールバックです。
// name はダウンロード時のファイル名、r はファイルの内容を読み取るストリームです。
type KeyframeSink func(name string, r io.Reader) error

// HistoryRepository loads and mutates generated MV history.
type HistoryRepository interface {
	ListHistoryPage(ctx context.Context, page int, perPage int) (domain.VideoHistoryPage, error)
	GetHistory(ctx context.Context, jobID string) (domain.VideoHistoryDetail, error)
	DeleteHistory(ctx context.Context, jobID string) error
	DownloadKeyframes(ctx context.Context, jobID string, sink KeyframeSink) error
	KeyframeZipSignedURL(ctx context.Context, jobID string) (string, error)
	// GetVeoUsage loads the job's recorded Veo generation tally. It returns (nil, nil) for jobs
	// that predate the record or never reached video generation — a missing record is a normal
	// state, not an error, and callers fall back to the recipe-derived estimate.
	GetVeoUsage(ctx context.Context, jobID string) (*domain.VeoUsage, error)
	// InvalidateJob clears cached history/recipe metadata for jobID, so a subsequent GetHistory
	// reads fresh data from storage instead of a stale cached copy (e.g. after a regenerate/edit
	// job writes new metadata back to jobID from a worker running in the same process).
	InvalidateJob(jobID string)
}
