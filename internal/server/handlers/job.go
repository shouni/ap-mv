package handlers

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/shouni/ap-mv/internal/domain"
	"github.com/shouni/go-serve-kit/respond"
	"github.com/shouni/go-utils/jobid"
)

// recordQueuedStatus records the queued job status. It is called before enqueueing
// (see Handler.enqueue for why that order). With it, a submitted job can be followed even before
// the worker starts.
// Enqueueing continues even if recording fails. Status is for knowing progress and weighs less than the artifacts.
func (h *Handler) recordQueuedStatus(r *http.Request, task *domain.Task) {
	if h.JobStatus == nil || task == nil {
		return
	}

	status := domain.NewQueuedJobStatus(task, time.Now().UTC())
	if err := h.JobStatus.Save(r.Context(), task.JobID, status); err != nil {
		slog.WarnContext(r.Context(), "failed to record queued job status",
			"job_id", task.JobID,
			"error", err,
		)
	}
}

// deleteJobStatus removes the job status document.
//
// The status lives apart from the artifacts (in Firestore), so the bulk prefix delete of history
// does not remove it. Without calling this, only the status of a job with no artifacts would be
// left behind as an orphan. Even if it cannot be removed, deleting the history itself succeeded, so it only logs a warning.
func (h *Handler) deleteJobStatus(r *http.Request, jobID string) {
	if h.JobStatus == nil {
		return
	}
	if err := h.JobStatus.Delete(r.Context(), jobID); err != nil {
		slog.WarnContext(r.Context(), "failed to delete job status", "job_id", jobID, "error", err)
	}
}

// jobDocument is the JSON response of GET /jobs/{jobID}.
//
// It is built on the progress (JobStatus), and for finished jobs the detail goes in the same
// document. The same URL can be read in the same shape from just after submission until deletion,
// so callers never switch URLs. The detail is nested rather than embedded because
// VideoHistoryDetail has its own job_id and title, and laid flat encoding/json would drop both keys.
type jobDocument struct {
	domain.JobStatus
	Detail *domain.VideoHistoryDetail `json:"detail,omitempty"`
}

// Job returns one job (GET /jobs/{jobID}).
//
// Unfinished or failed jobs get their progress (the page refreshes itself; JSON is the status).
// Finished jobs (and jobs without a record made before the status feature) get the detail.
func (h *Handler) Job(w http.ResponseWriter, r *http.Request) {
	jobID := strings.TrimSpace(chi.URLParam(r, "jobID"))
	if err := jobid.Validate(jobID); err != nil {
		respond.Error(w, r, http.StatusBadRequest, err.Error())
		return
	}

	status := h.loadJobStatus(r, jobID)
	if status != nil && status.State != domain.JobStateSucceeded {
		h.serveStatus(w, r, *status)
		return
	}
	h.serveDetail(w, r, jobID, status)
}

// loadJobStatus returns the recorded progress. It is nil when there is none (jobs from before the
// status feature) or it cannot be read. Unreadable is not an error because the detail can be drawn from the artifacts.
func (h *Handler) loadJobStatus(r *http.Request, jobID string) *domain.JobStatus {
	if h.JobStatus == nil {
		return nil
	}
	status, err := h.JobStatus.Get(r.Context(), jobID)
	if err != nil {
		if !errors.Is(err, domain.ErrJobStatusNotFound) {
			slog.WarnContext(r.Context(), "failed to load job status", "job_id", jobID, "error", err)
		}
		return nil
	}
	return &status
}

// serveStatus returns the progress of a job that has no artifacts yet (or could not make them).
// Browser polling and M2M clients detecting completion both read the same thing.
func (h *Handler) serveStatus(w http.ResponseWriter, r *http.Request, status domain.JobStatus) {
	if respond.WantsJSON(w, r) {
		respond.JSON(w, r, http.StatusOK, status)
		return
	}
	h.renderPage(w, r, PageData{Title: "Job", JobID: status.JobID, Status: string(status.State)}, "queued.html")
}

// completedStatus returns the status of a job whose detail could be read. Even a job without a
// record is complete once its artifacts can be read.
func completedStatus(jobID string, status *domain.JobStatus) domain.JobStatus {
	if status != nil {
		return *status
	}
	var completed domain.JobStatus
	completed.JobID = jobID
	completed.State = domain.JobStateSucceeded
	return completed
}

// serveDetail returns a finished job's detail. JSON puts the status and the detail in one document (jobDocument).
func (h *Handler) serveDetail(w http.ResponseWriter, r *http.Request, jobID string, status *domain.JobStatus) {
	if h.HistoryRepository == nil {
		h.renderPage(w, r, PageData{Title: "History", Message: "history storage adapter is not configured yet"}, "history_detail.html")
		return
	}
	history, err := h.HistoryRepository.GetHistory(r.Context(), jobID)
	if err != nil {
		respond.ServerError(w, r, http.StatusInternalServerError, err, "failed to get history detail", "job_id", jobID)
		return
	}
	h.applyCostEstimate(r.Context(), jobID, &history)
	if respond.WantsJSON(w, r) {
		// JSON callers (the MCP gateway) receive the URLs themselves without following redirects, so
		// only here are they signed. Below, the page fills in same-origin paths.
		if err := h.HistoryRepository.SignHistoryURLs(r.Context(), &history); err != nil {
			respond.ServerError(w, r, http.StatusInternalServerError, err, "failed to sign history URLs", "job_id", jobID)
			return
		}
		respond.JSON(w, r, http.StatusOK, jobDocument{JobStatus: completedStatus(jobID, status), Detail: &history})
		return
	}
	applyWebMediaURLs(&history)
	h.renderPage(w, r, h.withModelOptions(PageData{
		Title:         "History Detail",
		CSRFToken:     csrfTokenFromContext(r.Context()),
		HistoryDetail: history,
	}), "history_detail.html")
}

// JobDelete deletes a job's artifacts and records (DELETE /jobs/{jobID}).
func (h *Handler) JobDelete(w http.ResponseWriter, r *http.Request) {
	jobID := strings.TrimSpace(chi.URLParam(r, "jobID"))
	if err := jobid.Validate(jobID); err != nil {
		respond.Error(w, r, http.StatusBadRequest, err.Error())
		return
	}
	if h.HistoryRepository == nil {
		// Returning 202 when nothing was deleted would make the caller mistake it for success.
		// Align with the read paths (Draft and the like) returning 500/503.
		respond.Error(w, r, http.StatusServiceUnavailable, "history storage adapter is not configured")
		return
	}
	if err := h.HistoryRepository.DeleteHistory(r.Context(), jobID); err != nil {
		respond.ServerError(w, r, http.StatusInternalServerError, err, "failed to delete history", "job_id", jobID)
		return
	}
	h.deleteJobStatus(r, jobID)
	respond.JSON(w, r, http.StatusAccepted, map[string]string{"job_id": jobID, "status": "deleted"})
}
