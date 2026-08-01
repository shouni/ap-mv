package domain

import (
	"time"

	"github.com/shouni/go-job-kit/jobstatus"
)

// JobState はジョブのライフサイクル上の状態です。
// 実体は go-job-kit の jobstatus.State です。
type JobState = jobstatus.State

const (
	// JobStateQueued は Cloud Tasks へ投入済みで、まだワーカーが処理を始めていない状態です。
	JobStateQueued = jobstatus.StateQueued
	// JobStateRunning はワーカーが処理中の状態です。
	// カット単位で分割実行される動画生成では、継続タスクへ引き継がれている間もこの状態です。
	JobStateRunning = jobstatus.StateRunning
	// JobStateSucceeded は成果物の公開まで完了した状態です。
	JobStateSucceeded = jobstatus.StateSucceeded
	// JobStateFailed は処理が失敗した状態です。Cloud Tasks による再試行の対象になり得ます。
	JobStateFailed = jobstatus.StateFailed
)

// JobStatus はジョブの進行状況です。
//
// 生成の成否はこれまで Slack 通知にしか残らず、失敗したジョブは UI から完全に消えていました。
// この記録があることで、UI・M2M クライアントの双方が投入後の状態を追跡できます。
// あわせて、Cloud Tasks の at-least-once 配信に対する再実行ガードの根拠にもなります。
//
// 共通フィールド（JobID・State・Attempts 等）と IsTerminal は jobstatus.Status が持ちます。
// 埋め込みなので JSON はフラットなまま保たれ、既存の status.json をそのまま読み書きできます。
type JobStatus struct {
	jobstatus.Status
	// OriginalJobID は、成果物の書き込み先が別ジョブのときのその ID です
	// （キーフレーム再生成・ZIP 再生成）。UI が参照先の履歴へ案内するために使います。
	OriginalJobID string `json:"original_job_id,omitempty"`
	// OutputURI は成功時の主成果物の保存先です。
	// 署名付き URL は有効期限が切れるため保存しません。
	OutputURI string `json:"output_uri,omitempty"`
}

// NewQueuedJobStatus は、キュー投入直後のジョブ状態を組み立てます。
func NewQueuedJobStatus(task *Task, now time.Time) JobStatus {
	if task == nil {
		return JobStatus{}
	}

	status := JobStatus{
		Status: jobstatus.Status{
			JobID:     task.JobID,
			Command:   string(task.Command),
			State:     JobStateQueued,
			QueuedAt:  now,
			UpdatedAt: now,
		},
		OriginalJobID: task.OriginalJobID,
	}
	if task.Recipe != nil {
		status.Title = task.Recipe.Title
	}
	return status
}
