package ports

import (
	"context"

	"ap-mv/internal/domain"
)

// HistoryRepository loads and mutates generated MV history.
type HistoryRepository interface {
	ListHistoryPage(ctx context.Context, page int, perPage int) (domain.VideoHistoryPage, error)
	GetHistory(ctx context.Context, jobID string) (domain.VideoHistoryDetail, error)
	DeleteHistory(ctx context.Context, jobID string) error
}
