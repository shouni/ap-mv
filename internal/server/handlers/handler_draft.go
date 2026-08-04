package handlers

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/shouni/go-utils/jobid"
)

// Drafts renders the VideoRecipe draft list, or returns JSON when Accept: application/json is set.
func (h *Handler) Drafts(w http.ResponseWriter, r *http.Request) {
	if h.DraftRepository == nil {
		h.renderPage(w, PageData{Title: "Drafts", Message: "draft storage adapter is not configured yet"}, "drafts.html")
		return
	}
	page, err := h.DraftRepository.ListDraftPage(r.Context(), pageFromQuery(r), 20)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, page)
		return
	}
	h.renderPage(w, PageData{
		Title:      "Drafts",
		CSRFToken:  csrfTokenFromContext(r.Context()),
		DraftItems: page.Items,
		PageMeta:   page.PageMeta,
	}, "drafts.html")
}

// Draft returns a single draft's VideoRecipe as JSON.
//
// 下書きに専用の詳細画面はありません。下書きを確認する相手は ap-mcp 越しの
// エージェントで、人間が読む画面としては一覧に並ぶカット数・尺・セクション数で足ります。
// ブラウザからのアクセスは一覧へ戻します（詳細画面の代わりに JSON を生で見せても、
// 一覧に用意した削除・生成の導線から離れるだけです）。
func (h *Handler) Draft(w http.ResponseWriter, r *http.Request) {
	jobID := strings.TrimSpace(chi.URLParam(r, "jobID"))
	if err := jobid.Validate(jobID); err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	if !wantsJSON(r) {
		http.Redirect(w, r, "/web/drafts", http.StatusFound)
		return
	}
	if h.DraftRepository == nil {
		writeError(w, r, http.StatusInternalServerError, "draft storage adapter is not configured")
		return
	}
	recipe, err := h.DraftRepository.GetDraftRecipe(r.Context(), jobID)
	if err != nil {
		slog.ErrorContext(r.Context(), "failed to get draft recipe",
			"job_id", jobID,
			"error", err,
		)
		writeError(w, r, http.StatusNotFound, "draft not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"job_id": jobID,
		"recipe": recipe,
	})
}

// DeleteDraft handles draft deletion requests.
func (h *Handler) DeleteDraft(w http.ResponseWriter, r *http.Request) {
	jobID := strings.TrimSpace(chi.URLParam(r, "jobID"))
	if err := jobid.Validate(jobID); err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	if h.DraftRepository != nil {
		if err := h.DraftRepository.DeleteDraft(r.Context(), jobID); err != nil {
			writeError(w, r, http.StatusInternalServerError, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"job_id": jobID, "status": "deleted"})
}
