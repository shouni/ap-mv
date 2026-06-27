package ports

import (
	"context"
	"io"

	"ap-mv/internal/domain"
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
}
