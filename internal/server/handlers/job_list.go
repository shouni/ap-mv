package handlers

import (
	"net/http"
	"strings"

	"github.com/shouni/ap-mv/internal/domain"
	"github.com/shouni/go-serve-kit/respond"
)

// JobList はジョブ一覧を返します（GET /jobs?stage=）。Accept で HTML と JSON を切り替えます。
func (h *Handler) JobList(w http.ResponseWriter, r *http.Request) {
	if h.HistoryRepository == nil {
		h.renderPage(w, r, PageData{Title: "History", Message: "history storage adapter is not configured yet"}, "history.html")
		return
	}
	page, err := h.HistoryRepository.ListHistoryPage(r.Context(), pageFromQuery(r), 20, stageFromQuery(r))
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	// 単価は保存せず表示時に解決するため、JSON 応答にも同じ値が乗るよう分岐の前で適用する。
	domain.ApplyVeoCostEstimateToHistories(page.Items, h.ModelOptions.DefaultVeoModel, h.VeoPricing)
	if respond.WantsJSON(w, r) {
		respond.JSON(w, r, http.StatusOK, page)
		return
	}
	h.renderPage(w, r, PageData{
		Title:        "History",
		HistoryItems: page.Items,
		PageMeta:     page.PageMeta,
		// 成果物の残っていないジョブは一覧から直接消せます（削除は fetch の DELETE なので
		// トークンが要ります）。詳細画面を開けないジョブがあるため、ここに口が無いと
		// 画面からは二度と消せません。
		CSRFToken: csrfTokenFromContext(r.Context()),
	}, "history.html")
}

// stageFromQuery reads the optional ?stage= filter. An unknown value is treated as no filter
// rather than an error, so a stale bookmark shows the full list instead of an error page.
func stageFromQuery(r *http.Request) domain.JobStage {
	switch stage := domain.JobStage(strings.TrimSpace(r.URL.Query().Get("stage"))); stage {
	case domain.StageScript, domain.StageKeyframes, domain.StageKeyframesDone,
		domain.StageVideos, domain.StageCompleted:
		return stage
	default:
		return ""
	}
}
