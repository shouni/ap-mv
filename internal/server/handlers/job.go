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

// jobDocument は GET /jobs/{jobID} の JSON 応答です。
//
// 進行状況（JobStatus）を土台に、完成したジョブでは詳細を同じ文書に載せます。投入直後から
// 削除するまで同じ URL を同じ形で読めるので、呼び出し側は URL を切り替えずに済みます。
// 詳細を埋め込まずに入れ子にするのは、VideoHistoryDetail が job_id や title を自分でも持ち、
// 平らに並べると encoding/json が両方のキーを落とすためです。
type jobDocument struct {
	domain.JobStatus
	Detail *domain.VideoHistoryDetail `json:"detail,omitempty"`
}

// Job はジョブ 1 件を返します（GET /jobs/{jobID}）。
//
// まだ終わっていない・失敗したジョブは進行状況（画面は自動更新、JSON は状態）。完成した
// ジョブ（と、状態機能より前に作られた記録の無いジョブ）は詳細です。
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

// loadJobStatus は記録された進行状況を返します。無い（状態機能より前のジョブ）か
// 読めなければ nil です。読めなかった場合をエラーにしないのは、詳細は成果物から描けるからです。
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

// serveStatus は、成果物がまだ無い（または出来なかった）ジョブの進行状況を返します。
// ブラウザからのポーリングと M2M クライアントの完了検知の両方が同じものを読みます。
func (h *Handler) serveStatus(w http.ResponseWriter, r *http.Request, status domain.JobStatus) {
	if respond.WantsJSON(w, r) {
		respond.JSON(w, r, http.StatusOK, status)
		return
	}
	h.renderPage(w, r, PageData{Title: "Job", JobID: status.JobID, Status: string(status.State)}, "queued.html")
}

// completedStatus は、詳細が読めたジョブの状態を返します。記録が無いジョブでも、
// 成果物が読めた時点で完了しています。
func completedStatus(jobID string, status *domain.JobStatus) domain.JobStatus {
	if status != nil {
		return *status
	}
	var completed domain.JobStatus
	completed.JobID = jobID
	completed.State = domain.JobStateSucceeded
	return completed
}

// serveDetail は完成したジョブの詳細を返します。JSON は状態と詳細を 1 つの文書にまとめます（jobDocument）。
func (h *Handler) serveDetail(w http.ResponseWriter, r *http.Request, jobID string, status *domain.JobStatus) {
	if h.HistoryRepository == nil {
		h.renderPage(w, r, PageData{Title: "History", Message: "history storage adapter is not configured yet"}, "history_detail.html")
		return
	}
	history, err := h.HistoryRepository.GetHistory(r.Context(), jobID)
	if err != nil {
		slog.ErrorContext(r.Context(), "failed to get history detail",
			"job_id", jobID,
			"error", err,
		)
		respond.Error(w, r, http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
		return
	}
	h.applyCostEstimate(r.Context(), jobID, &history)
	if respond.WantsJSON(w, r) {
		// JSON の呼び出し元（ap-mcp）はリダイレクトを辿らず URL 自体を受け取るため、
		// ここでだけ署名します。画面はこの下で同一オリジンのパスを埋めます。
		if err := h.HistoryRepository.SignHistoryURLs(r.Context(), &history); err != nil {
			slog.ErrorContext(r.Context(), "failed to sign history URLs", "job_id", jobID, "error", err)
			respond.Error(w, r, http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
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

// JobDelete はジョブの成果物と記録を削除します（DELETE /jobs/{jobID}）。
func (h *Handler) JobDelete(w http.ResponseWriter, r *http.Request) {
	jobID := strings.TrimSpace(chi.URLParam(r, "jobID"))
	if err := jobid.Validate(jobID); err != nil {
		respond.Error(w, r, http.StatusBadRequest, err.Error())
		return
	}
	if h.HistoryRepository == nil {
		// 何も消していないのに 202 を返すと呼び出し側は成功と誤認する。
		// 読み取り系（Draft 等）が 500/503 を返すのと同じ扱いに揃える。
		respond.Error(w, r, http.StatusServiceUnavailable, "history storage adapter is not configured")
		return
	}
	if err := h.HistoryRepository.DeleteHistory(r.Context(), jobID); err != nil {
		respond.Error(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	h.deleteJobStatus(r, jobID)
	respond.JSON(w, r, http.StatusAccepted, map[string]string{"job_id": jobID, "status": "deleted"})
}
