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

// alreadySucceeded は、そのジョブが既に完了しているかどうかを返します。
//
// Cloud Tasks は at-least-once 配信なので、通知の失敗などでワーカーがエラーを返すと
// 同じタスクが再配信されます。動画生成をまるごと呼び直すと Veo の生成コストが
// そのまま二重に発生するため、完了済みならここで打ち切ります。
//
// なお、これはジョブ単位のガードです。カット単位で分割実行される動画生成の途中
// （state=running）で再配信された場合は、そのカットの再生成までは防げません。
// それには各カット生成前に永続化済みの状態を確認する仕組みが別途必要です。
func (s statusRecorder) alreadySucceeded(ctx context.Context, jobID string) bool {
	return s.recorder.AlreadySucceeded(ctx, jobID)
}

// markRunning は処理開始を記録し、試行回数を 1 つ進めます。
func (s statusRecorder) markRunning(ctx context.Context, task *domain.Task) {
	s.record(ctx, task, domain.JobStateRunning, func(next, _ *domain.JobStatus) {
		next.Attempts++
	})
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

	status := domain.JobStatus{
		Status: jobstatus.Status{
			JobID:   task.JobID,
			Command: string(task.Command),
			State:   state,
		},
		OriginalJobID: task.OriginalJobID,
	}
	if task.Recipe != nil {
		status.Title = task.Recipe.Title
	}

	s.recorder.Record(ctx, task.JobID, status, apply)
}
