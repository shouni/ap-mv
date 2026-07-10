package repository

import (
	"sort"

	"github.com/shouni/ap-mv/internal/domain"
)

func selectHistoryPageIDs(jobIDs []string, page int, perPage int) ([]string, domain.PageMeta) {
	if page < 1 {
		page = 1
	}
	// 作成日時（jobID 埋め込みタイムスタンプ）の降順でソートする。jobID の文字列比較だと
	// プレフィックス（video-recipe- / short- / mv- 等）順になってしまうため。
	// タイムスタンプを持たない jobID は末尾に回し、同時刻は jobID 降順で安定させる。
	sort.Slice(jobIDs, func(i, j int) bool {
		ti, tj := historyCreatedAtRaw(jobIDs[i]), historyCreatedAtRaw(jobIDs[j])
		if ti != tj {
			return ti > tj
		}
		return jobIDs[i] > jobIDs[j]
	})

	total := len(jobIDs)
	totalPages := 0
	selectedIDs := jobIDs
	if perPage > 0 {
		totalPages = (total + perPage - 1) / perPage
		if totalPages > 0 && page > totalPages {
			page = totalPages
		}
		start := (page - 1) * perPage
		if start > total {
			start = total
		}
		end := start + perPage
		if end > total {
			end = total
		}
		selectedIDs = jobIDs[start:end]
	}

	meta := domain.PageMeta{
		Page:       page,
		PerPage:    perPage,
		Total:      total,
		TotalPages: totalPages,
		HasPrev:    page > 1,
		HasNext:    totalPages > 0 && page < totalPages,
		PrevPage:   page - 1,
		NextPage:   page + 1,
	}
	if total > 0 {
		if perPage > 0 {
			meta.From = (page-1)*perPage + 1
			meta.To = meta.From + len(selectedIDs) - 1
		} else {
			meta.TotalPages = 1
			meta.From = 1
			meta.To = len(selectedIDs)
		}
	}
	return selectedIDs, meta
}

func adjustPageMetaItemCount(meta domain.PageMeta, itemCount int) domain.PageMeta {
	if meta.Total == 0 {
		return meta
	}
	if itemCount == 0 {
		meta.From = 0
		meta.To = 0
		return meta
	}
	if meta.PerPage > 0 {
		meta.To = meta.From + itemCount - 1
		return meta
	}
	meta.To = itemCount
	return meta
}
