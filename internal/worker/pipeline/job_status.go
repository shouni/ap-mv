package pipeline

import (
	"context"

	"github.com/shouni/go-job-kit/jobstatus"

	"github.com/shouni/ap-mv/internal/domain"
	"github.com/shouni/ap-mv/internal/ports"
)

// statusRecorder はジョブ状態の記録を担当します。
//
// 再実行ガード・前回記録からの引き継ぎ・記録失敗を握り潰す振る舞いは jobstatus.Recorder に
// 集約しており、ここが与えるのは ap-mv 固有の状態の組み立てだけです。
// 記録先が未設定でもパイプラインは動作し、記録だけが行われません。
type statusRecorder struct {
	recorder *jobstatus.Recorder[domain.JobStatus]
}

// newStatusRecorder は ports.JobStatusStore を裏付けとした statusRecorder を構築します。
// store が nil の場合、記録は行われません。
//
// ports.JobStatusStore は jobstatus.StatusStore と同じシグネチャなので、そのまま渡せます。
func newStatusRecorder(store ports.JobStatusStore) statusRecorder {
	if store == nil {
		return statusRecorder{recorder: jobstatus.NewRecorder[domain.JobStatus](nil)}
	}
	return statusRecorder{recorder: jobstatus.NewRecorder[domain.JobStatus](store)}
}

// begin は、そのジョブが既に完了していれば true を返し、未完了なら処理開始を記録して
// false を返します。判定と記録を前回の記録の 1 回の読みで行うので、間に別の配信が
// 割り込む隙がありません。試行回数はここで 1 つ進みます。
//
// 状態を読めなかった場合はエラーを返し、記録もしません。呼び出し側はそのままエラーを
// 返して Cloud Tasks の再配信に委ねてください。ここで「未完了」に倒すと、完了済みジョブを
// 作り直してこのガードが防ぐはずのコストを発生させ、「完了済み」に倒すと未完了の
// ジョブがタスクごと ACK されて二度と実行されません。
func (s statusRecorder) begin(ctx context.Context, task *domain.Task) (bool, error) {
	if task == nil {
		return false, nil
	}
	return s.recorder.Begin(ctx, task.JobID, s.newStatus(task, domain.JobStateRunning),
		func(next, _ *domain.JobStatus) { next.Attempts++ },
	)
}

// markSucceeded は成功と成果物の保存先を記録します。
func (s statusRecorder) markSucceeded(ctx context.Context, task *domain.Task, req domain.NotificationRequest) {
	s.record(ctx, task, domain.JobStateSucceeded, func(next, _ *domain.JobStatus) {
		next.OutputURI = req.OutputURI
		if req.Title != "" {
			next.Title = req.Title
		}
	})
}

// markFailed は失敗と理由を記録します。
func (s statusRecorder) markFailed(ctx context.Context, task *domain.Task, cause error) {
	if cause == nil {
		return
	}
	s.record(ctx, task, domain.JobStateFailed, func(next, _ *domain.JobStatus) {
		next.Error = cause.Error()
	})
}

// record は今回の記録ぶんの状態を組み立てて保存します。
// Attempts・QueuedAt と、レシピから題目が取れなかったときの Title は Recorder が
// 前回の記録から引き継ぎます。apply はその引き継ぎの後に呼ばれます。
func (s statusRecorder) record(
	ctx context.Context,
	task *domain.Task,
	state domain.JobState,
	apply func(next, prev *domain.JobStatus),
) {
	if task == nil {
		return
	}
	s.recorder.Record(ctx, task.JobID, s.newStatus(task, state), apply)
}

// newStatus は今回の記録ぶんの状態を組み立てます。
func (s statusRecorder) newStatus(task *domain.Task, state domain.JobState) domain.JobStatus {
	status := domain.JobStatus{
		JobID:         task.JobID,
		Command:       string(task.Command),
		State:         state,
		OriginalJobID: task.OriginalJobID,
	}
	if task.Recipe != nil {
		status.Title = task.Recipe.Title
	}
	return status
}
