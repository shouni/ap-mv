package domain

import "time"

// JobState はジョブのライフサイクル上の状態です。
type JobState string

const (
	// JobStateQueued は Cloud Tasks へ投入済みで、まだワーカーが処理を始めていない状態です。
	JobStateQueued JobState = "queued"
	// JobStateRunning はワーカーが処理中の状態です。
	// カット単位で分割実行される動画生成では、継続タスクへ引き継がれている間もこの状態です。
	JobStateRunning JobState = "running"
	// JobStateSucceeded は成果物の公開まで完了した状態です。
	JobStateSucceeded JobState = "succeeded"
	// JobStateFailed は処理が失敗した状態です。Cloud Tasks による再試行の対象になり得ます。
	JobStateFailed JobState = "failed"
)

// JobStatus はジョブの進行状況です。
//
// 生成の成否はこれまで Slack 通知にしか残らず、失敗したジョブは UI から完全に消えていました。
// この記録があることで、UI・M2M クライアントの双方が投入後の状態を追跡できます。
// あわせて、Cloud Tasks の at-least-once 配信に対する再実行ガードの根拠にもなります。
type JobStatus struct {
	JobID   string   `json:"job_id"`
	Command string   `json:"command"`
	State   JobState `json:"state"`
	// Title はレシピが確定した時点で埋まります。
	Title string `json:"title,omitempty"`
	// Error は State が failed のときの失敗理由です。
	Error string `json:"error,omitempty"`
	// Attempts はワーカーが処理を開始した回数です。
	// 継続タスクへ分割される動画生成では、チェーン全体の実行回数になります。
	Attempts int `json:"attempts,omitempty"`
	// OriginalJobID は、成果物の書き込み先が別ジョブのときのその ID です
	// （キーフレーム再生成・ZIP 再生成）。UI が参照先の履歴へ案内するために使います。
	OriginalJobID string `json:"original_job_id,omitempty"`
	// OutputURI は成功時の主成果物の保存先です。
	// 署名付き URL は有効期限が切れるため保存しません。
	OutputURI string    `json:"output_uri,omitempty"`
	QueuedAt  time.Time `json:"queued_at,omitzero"`
	UpdatedAt time.Time `json:"updated_at"`
}

// IsTerminal は、これ以上状態が変化しないかどうかを返します。
func (s JobStatus) IsTerminal() bool {
	return s.State == JobStateSucceeded
}

// NewQueuedJobStatus は、キュー投入直後のジョブ状態を組み立てます。
func NewQueuedJobStatus(task *Task, now time.Time) JobStatus {
	if task == nil {
		return JobStatus{}
	}

	status := JobStatus{
		JobID:         task.JobID,
		Command:       string(task.Command),
		State:         JobStateQueued,
		OriginalJobID: task.OriginalJobID,
		QueuedAt:      now,
		UpdatedAt:     now,
	}
	if task.Recipe != nil {
		status.Title = task.Recipe.Title
	}
	return status
}
