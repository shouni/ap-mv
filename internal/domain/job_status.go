package domain

import (
	"strings"
	"time"

	"github.com/shouni/go-job-firestore/jobfirestore"
)

// JobState はジョブのライフサイクル上の状態です。
// 実体は go-job-firestore の jobfirestore.State です。
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

	// ここから下は履歴一覧の見出しです。以前は一覧の 1 件ごとに GCS の
	// video_music_meta.json を開いて組み立てていました。写しを置くことで、一覧は
	// クエリ 1 回で済み、進行段階での絞り込みも Firestore 側で効きます。写した値が
	// 古くなる代償は、状態を記録するたびに ApplyVideoRecipe で塗り直すことで払います。
	Mood        string `json:"mood,omitempty" firestore:"mood,omitempty"`
	Tempo       int    `json:"tempo,omitzero" firestore:"tempo,omitempty"`
	VisualMode  string `json:"visual_mode,omitempty" firestore:"visual_mode,omitempty"`
	AspectRatio string `json:"aspect_ratio,omitempty" firestore:"aspect_ratio,omitempty"`
	// FinalVideoURL は継続チェーンを 1 本に結合した完成動画の gs:// URI です。
	FinalVideoURL string `json:"final_video_url,omitempty" firestore:"final_video_url,omitempty"`
	// GeneratedSeconds は生成済みカットの尺の合計（Veo の課金対象秒数の概算）です。
	// 単価を掛けるのは表示側なので、ここには秒数だけを残します。
	GeneratedSeconds float64 `json:"generated_seconds,omitzero" firestore:"generated_seconds,omitempty"`
	// Progress は進行段階と、その根拠になる数え上げです。
	//
	// firestore タグに omitempty を付けてはいけません。付けるとカットが 1 つも無い
	// ジョブでフィールドごと書かれず、progress.stage での絞り込みがそのドキュメントを
	// 拾えなくなります。
	Progress JobProgress `json:"progress" firestore:"progress"`
}

// ApplyVideoRecipe は、レシピの内容を一覧の見出しへ写します。レシピが無ければ何もしません。
//
// 投入時・処理開始時・終端の記録のすべてで呼びます。呼ばない経路があると、その記録が
// 状態を丸ごと上書きした瞬間に見出しだけが消えます（記録は前回の共通フィールドしか
// 引き継ぎません）。
func (s *JobStatus) ApplyVideoRecipe(recipe *VideoRecipe) {
	if recipe == nil {
		return
	}
	if title := strings.TrimSpace(firstNonEmptyString(recipe.MusicRecipe.Title, recipe.ProjectTitle)); title != "" {
		s.Title = title
	}
	s.Mood = strings.TrimSpace(recipe.MusicRecipe.Mood)
	s.Tempo = recipe.MusicRecipe.Tempo
	if mode := strings.TrimSpace(recipe.MusicRecipe.ComposeMode); mode != "" {
		s.VisualMode = mode
	}
	s.AspectRatio = strings.TrimSpace(recipe.AspectRatio)
	s.FinalVideoURL = strings.TrimSpace(recipe.FinalVideoURL)
	s.GeneratedSeconds = GeneratedSecondsOfCuts(recipe.Cuts)
	s.Progress = NewJobProgress(recipe.Cuts)
}

// firstNonEmptyString は、最初の空でない値を返します。
func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// NewJobStatus は、タスクから状態 1 件分の見出しを組み立てます。
//
// 投入時（web）と各遷移の記録（worker）で同じ組み立てを使います。別々に書くと、worker の
// 記録が状態を丸ごと上書きする一方で写す項目が web 側より少なく、投入時にだけ埋めた
// 見出しが running を記録した瞬間に消えます。片方に項目を足してもコンパイルは通るので、
// 気付けるのは一覧が空欄になったときだけです。
func NewJobStatus(task *Task, state JobState) JobStatus {
	if task == nil {
		return JobStatus{}
	}

	status := JobStatus{
		JobID:         task.JobID,
		Command:       string(task.ListedCommand()),
		State:         state,
		OriginalJobID: task.OriginalJobID,
		VisualMode:    strings.TrimSpace(task.VisualMode),
	}
	if task.Recipe != nil {
		status.Title = strings.TrimSpace(task.Recipe.Title)
	}
	status.ApplyVideoRecipe(task.VideoRecipe)
	return status
}

// NewQueuedJobStatus は、キュー投入直後のジョブ状態を組み立てます。
//
// 一覧の見出しもここで埋めます。投入時点のレシピから写せる分を入れておかないと、
// 生成が終わるまで一覧に題名すら出ません。
func NewQueuedJobStatus(task *Task, now time.Time) JobStatus {
	status := NewJobStatus(task, JobStateQueued)
	status.QueuedAt = now
	status.UpdatedAt = now
	return status
}
