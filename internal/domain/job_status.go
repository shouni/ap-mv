package domain

import (
	"time"

	"github.com/shouni/go-job-firestore/jobfirestore"
)

// JobState はジョブのライフサイクル上の状態です。
// 実体は go-job-kit の jobfirestore.State です。
type JobState = jobfirestore.State

const (
	// JobStateQueued は Cloud Tasks へ投入済みで、まだワーカーが処理を始めていない状態です。
	JobStateQueued = jobfirestore.StateQueued
	// JobStateRunning はワーカーが処理中の状態です。
	// カット単位で分割実行される動画生成では、継続タスクへ引き継がれている間もこの状態です。
	JobStateRunning = jobfirestore.StateRunning
	// JobStateSucceeded は成果物の公開まで完了した状態です。
	JobStateSucceeded = jobfirestore.StateSucceeded
	// JobStateFailed は処理が失敗した状態です。Cloud Tasks による再試行の対象になり得ます。
	JobStateFailed = jobfirestore.StateFailed
)

// ErrJobStatusNotFound は、ジョブ状態がまだ記録されていないことを表します。
// 「状態が無い」は異常ではなく正常な状態（記録前の投入や、この機能より前に作られた
// ジョブ）なので、呼び出し側がストレージ障害と区別できるよう独立したエラーにしています。
//
// JobState と同じく go-job-firestore の値をそのまま指しています。状態の定数だけを domain で
// 別名にしてエラーを repository に置いていたため、同じジョブ状態の面を扱うのに
// 「状態は domain 経由・エラーは具象パッケージ経由」と参照先が割れていました。
var ErrJobStatusNotFound = jobfirestore.ErrNotFound

// ErrJobStatusUnavailable は、ジョブ状態が「あるはずなのに読めなかった」ことを表します。
// 未記録と混ぜると、完了済みのジョブを未完了と誤認して生成をまるごとやり直します。
var ErrJobStatusUnavailable = jobfirestore.ErrUnavailable

// JobStatus はジョブの進行状況です。
//
// 生成の成否はこれまで Slack 通知にしか残らず、失敗したジョブは UI から完全に消えていました。
// この記録があることで、UI・M2M クライアントの双方が投入後の状態を追跡できます。
// あわせて、Cloud Tasks の at-least-once 配信に対する再実行ガードの根拠にもなります。
//
// 共通フィールド（JobID・State・Attempts 等）と IsTerminal は jobfirestore.Status が
// 持ちます。埋め込みなので Firestore のドキュメントもレスポンス JSON もフラットなままです。
//
// firestore タグを省略しないでください。省略すると保存されるフィールド名が Go の識別子
// （OriginalJobID）になり、json タグで組み立てた既存のレスポンスと食い違います。
type JobStatus struct {
	jobfirestore.Status
	// OriginalJobID は、成果物の書き込み先が別ジョブのときのその ID です
	// （キーフレーム再生成・ZIP 再生成）。UI が参照先の履歴へ案内するために使います。
	OriginalJobID string `json:"original_job_id,omitempty" firestore:"original_job_id,omitempty"`
	// OutputURI は成功時の主成果物の保存先です。
	// 署名付き URL は有効期限が切れるため保存しません。
	OutputURI string `json:"output_uri,omitempty" firestore:"output_uri,omitempty"`
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
		QueuedAt:      now,
		UpdatedAt:     now,
		OriginalJobID: task.OriginalJobID,
	}
	if task.Recipe != nil {
		status.Title = task.Recipe.Title
	}
	return status
}
