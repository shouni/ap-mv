package handlers

import (
	"archive/zip"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/shouni/go-utils/jobid"

	"github.com/shouni/ap-mv/internal/domain"
)

// History renders the history page, or returns JSON when Accept: application/json is set.
func (h *Handler) History(w http.ResponseWriter, r *http.Request) {
	if h.HistoryRepository == nil {
		h.renderPage(w, PageData{Title: "History", Message: "history storage adapter is not configured yet"}, "history.html")
		return
	}
	page, err := h.HistoryRepository.ListHistoryPage(r.Context(), pageFromQuery(r), 20)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	// 単価は保存せず表示時に解決するため、JSON 応答にも同じ値が乗るよう分岐の前で適用する。
	domain.ApplyVeoCostEstimateToHistories(page.Items, h.ModelOptions.DefaultVeoModel, h.VeoPricing)
	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, page)
		return
	}
	h.renderPage(w, PageData{
		Title:        "History",
		HistoryItems: page.Items,
		PageMeta:     page.PageMeta,
	}, "history.html")
}

// HistoryDetail renders a generated MV history detail page, or returns JSON when Accept: application/json is set.
func (h *Handler) HistoryDetail(w http.ResponseWriter, r *http.Request) {
	if h.HistoryRepository == nil {
		h.renderPage(w, PageData{Title: "History", Message: "history storage adapter is not configured yet"}, "history_detail.html")
		return
	}
	jobID := strings.TrimSpace(chi.URLParam(r, "jobID"))
	if err := jobid.Validate(jobID); err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	history, err := h.HistoryRepository.GetHistory(r.Context(), jobID)
	if err != nil {
		slog.ErrorContext(r.Context(), "failed to get history detail",
			"job_id", jobID,
			"error", err,
		)
		writeError(w, r, http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
		return
	}
	h.applyCostEstimate(r.Context(), jobID, &history)
	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, history)
		return
	}
	h.renderPage(w, h.withModelOptions(PageData{
		Title:         "History Detail",
		CSRFToken:     csrfTokenFromContext(r.Context()),
		HistoryDetail: history,
	}), "history_detail.html")
}

// DownloadKeyframes redirects to the pre-built keyframe zip in GCS.
// Falls back to on-demand zip generation for jobs that predate the pipeline change.
func (h *Handler) DownloadKeyframes(w http.ResponseWriter, r *http.Request) {
	jobID := strings.TrimSpace(chi.URLParam(r, "jobID"))
	if err := jobid.Validate(jobID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if h.HistoryRepository == nil {
		http.Error(w, "history storage adapter is not configured", http.StatusInternalServerError)
		return
	}

	// Try to redirect to the pre-built zip uploaded by the pipeline.
	// Continue to on-demand fallback even on signing error — intentional fail-soft.
	signedURL, err := h.HistoryRepository.KeyframeZipSignedURL(r.Context(), jobID)
	if err != nil {
		slog.ErrorContext(r.Context(), "keyframe zip signed URL failed, falling back to on-demand generation", "job_id", jobID, "error", err)
	}
	if signedURL != "" {
		http.Redirect(w, r, signedURL, http.StatusFound)
		return
	}

	// Fall back: build zip on-demand for jobs without a pre-built zip.
	history, err := h.HistoryRepository.GetHistory(r.Context(), jobID)
	if err != nil {
		slog.ErrorContext(r.Context(), "failed to get history for keyframe download", "job_id", jobID, "error", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	hasKeyframes := false
	for _, cut := range history.Cuts {
		if strings.TrimSpace(cut.KeyframeReference) != "" {
			hasKeyframes = true
			break
		}
	}
	if !hasKeyframes {
		http.Error(w, "no keyframes available", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="keyframes-%s.zip"`, jobID))
	zw := zip.NewWriter(w)
	if err := h.HistoryRepository.DownloadKeyframes(r.Context(), jobID, func(name string, reader io.Reader) error {
		fw, err := zw.Create(name)
		if err != nil {
			slog.ErrorContext(r.Context(), "failed to create zip entry", "job_id", jobID, "file", name, "error", err)
			return err
		}
		if _, err := io.Copy(fw, reader); err != nil {
			slog.ErrorContext(r.Context(), "failed to write zip entry", "job_id", jobID, "file", name, "error", err)
			return err
		}
		return nil
	}); err != nil {
		slog.ErrorContext(r.Context(), "failed to stream keyframe zip", "job_id", jobID, "error", err)
		return
	}
	if assContent := domain.GenerateASS(history.Cuts, domain.ASSColors{}, history.Tempo); assContent != "" {
		fw, err := zw.Create("lyrics.ass")
		if err != nil {
			slog.ErrorContext(r.Context(), "failed to create ass zip entry", "job_id", jobID, "error", err)
		} else if _, err := io.WriteString(fw, assContent); err != nil {
			slog.ErrorContext(r.Context(), "failed to write ass zip entry", "job_id", jobID, "error", err)
		}
	}
	if err := zw.Close(); err != nil {
		slog.ErrorContext(r.Context(), "failed to finalize zip", "job_id", jobID, "error", err)
	}
}

// DeleteHistory handles history deletion requests.
func (h *Handler) DeleteHistory(w http.ResponseWriter, r *http.Request) {
	jobID := strings.TrimSpace(chi.URLParam(r, "jobID"))
	if err := jobid.Validate(jobID); err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	if h.HistoryRepository != nil {
		if err := h.HistoryRepository.DeleteHistory(r.Context(), jobID); err != nil {
			writeError(w, r, http.StatusInternalServerError, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"job_id": jobID, "status": "deleted"})
}
