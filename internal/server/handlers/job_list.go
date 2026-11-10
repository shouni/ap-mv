package handlers

import (
	"net/http"
	"strings"

	"github.com/shouni/ap-mv/internal/domain"
	"github.com/shouni/go-serve-kit/respond"
)

// JobList returns the job listing (GET /jobs?stage=). Accept switches between HTML and JSON.
func (h *Handler) JobList(w http.ResponseWriter, r *http.Request) {
	if h.HistoryRepository == nil {
		h.renderPage(w, r, PageData{Title: "History", Message: "history storage adapter is not configured yet"}, "history.html")
		return
	}
	page, err := h.HistoryRepository.ListHistoryPage(r.Context(), pageFromQuery(r), 20, stageFromQuery(r))
	if err != nil {
		respond.ServerError(w, r, http.StatusInternalServerError, err, "failed to list history")
		return
	}
	// The price is resolved at display time rather than stored, so apply it before the branch so the JSON response carries the same values.
	domain.ApplyVeoCostEstimateToHistories(page.Items, h.ModelOptions.DefaultVeoModel, h.VeoPricing)
	if respond.WantsJSON(w, r) {
		respond.JSON(w, r, http.StatusOK, page)
		return
	}
	h.renderPage(w, r, PageData{
		Title:        "History",
		HistoryItems: page.Items,
		PageMeta:     page.PageMeta,
		// Jobs with no artifacts left can be deleted straight from the listing (delete is a fetch DELETE,
		// so it needs the token). Some jobs cannot open their detail page, so without an entrance here
		// they could never be removed from the UI.
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
