package ports

import (
	"context"

	"github.com/shouni/ap-mv/internal/domain"
)

// DraftRepository loads and deletes VideoRecipe drafts.
//
// HistoryRepository と分けているのは、下書きが履歴の一種ではないからです。下書きは
// キーフレームも動画も課金実績も持たず、HistoryRepository の大半のメソッド
// （DownloadKeyframes / KeyframeZipSignedURL / GetVeoUsage）に意味のある返り値がありません。
// 実装は同じ *repository.VideoHistoryRepository が両方を満たします。
type DraftRepository interface {
	ListDraftPage(ctx context.Context, page int, perPage int) (domain.VideoDraftPage, error)
	// GetDraftRecipe は下書きの VideoRecipe を返します。下書きは作られた後に
	// 書き換わらないため、履歴詳細と違って読み出しごとの鮮度を気にする必要はありません。
	GetDraftRecipe(ctx context.Context, jobID string) (*domain.VideoRecipe, error)
	DeleteDraft(ctx context.Context, jobID string) error
}
