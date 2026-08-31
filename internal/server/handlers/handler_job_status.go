package handlers

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/shouni/go-utils/jobid"

	"github.com/shouni/ap-mv/internal/domain"

	"github.com/shouni/go-serve-kit/respond"
)

// recordQueuedStatus は投入直後のジョブ状態を記録します。
// これがあることで、ワーカーが動き出す前でも投入済みジョブを追跡できます。
// 記録に失敗してもタスク自体は既にキューへ入っているため、警告ログに留めて受付は成功とします。
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

// deleteJobStatus はジョブ状態のドキュメントを消します。
//
// 状態は成果物と別の場所（Firestore）にあるため、履歴のプレフィックス一括削除では
// 消えません。呼ばないと、成果物の無いジョブの状態だけが孤児として残り続けます。
// 消せなくても履歴の削除そのものは成功しているので、警告ログに留めます。
func (h *Handler) deleteJobStatus(r *http.Request, jobID string) {
	if h.JobStatus == nil {
		return
	}
	if err := h.JobStatus.Delete(r.Context(), jobID); err != nil {
		slog.WarnContext(r.Context(), "failed to delete job status", "job_id", jobID, "error", err)
	}
}

// JobStatusDetail は、ジョブの進行状況（queued/running/succeeded/failed）を返します。
// ブラウザからのポーリングと M2M クライアントの完了検知の両方が利用します。
func (h *Handler) JobStatusDetail(w http.ResponseWriter, r *http.Request) {
	jobID := strings.TrimSpace(chi.URLParam(r, "jobID"))
	if err := jobid.Validate(jobID); err != nil {
		respond.Error(w, r, http.StatusBadRequest, err.Error())
		return
	}
	if h.JobStatus == nil {
		respond.Error(w, r, http.StatusServiceUnavailable, "job status tracking is not configured")
		return
	}

	status, err := h.JobStatus.Get(r.Context(), jobID)
	if err != nil {
		// 状態が無いのは異常ではなく「この機能より前に作られたジョブ」でも起こるため、
		// 404 で明確に区別できるようにします。
		if errors.Is(err, domain.ErrJobStatusNotFound) {
			respond.Error(w, r, http.StatusNotFound, "job status not found")
			return
		}
		slog.ErrorContext(r.Context(), "failed to load job status", "job_id", jobID, "error", err)
		respond.Error(w, r, http.StatusInternalServerError, "failed to load job status")
		return
	}

	respond.JSON(w, r, http.StatusOK, status)
}
