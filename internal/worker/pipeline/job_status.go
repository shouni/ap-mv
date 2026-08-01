package pipeline

import (
	"context"
	"log/slog"

	"github.com/shouni/go-job-kit/jobstatus"

	"github.com/shouni/ap-mv/internal/domain"
	"github.com/shouni/ap-mv/internal/ports"
)

// statusRecorder はジョブ状態の記録を担当します。
// 記録先が未設定（store == nil）でもパイプラインは動作し、記録だけが行われません。
// 状態の記録に失敗しても生成そのものは止めず、警告ログに留めます。
type statusRecorder struct {
	store ports.JobStatusStore
}

func (s statusRecorder) enabled() bool {
	return s.store != nil
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
	if !s.enabled() {
		return false
	}

	status, err := s.store.Get(ctx, jobID)
	if err != nil {
		// 未記録・読み取り失敗はいずれも「完了していない」とみなして処理を続行します。
		// 状態を読めないことを理由に生成を止めるほうが害が大きいためです。
		return false
	}
	return status.IsTerminal()
}

// markRunning は処理開始を記録し、試行回数を 1 つ進めます。
func (s statusRecorder) markRunning(ctx context.Context, task *domain.Task) {
	s.save(ctx, s.build(ctx, task, domain.JobStateRunning, func(status *domain.JobStatus) {
		status.Attempts++
	}))
}

// markSucceeded は成功と成果物の保存先を記録します。
func (s statusRecorder) markSucceeded(ctx context.Context, task *domain.Task, req domain.NotificationRequest) {
	s.save(ctx, s.build(ctx, task, domain.JobStateSucceeded, func(status *domain.JobStatus) {
		status.OutputURI = req.OutputURI
		if req.Title != "" {
			status.Title = req.Title
		}
	}))
}

// markFailed は失敗と理由を記録します。
func (s statusRecorder) markFailed(ctx context.Context, task *domain.Task, cause error) {
	if cause == nil {
		return
	}
	s.save(ctx, s.build(ctx, task, domain.JobStateFailed, func(status *domain.JobStatus) {
		status.Error = cause.Error()
	}))
}

// build は既存の記録（試行回数・投入時刻・タイトル）を引き継いで新しい状態を組み立てます。
func (s statusRecorder) build(
	ctx context.Context,
	task *domain.Task,
	state domain.JobState,
	apply func(*domain.JobStatus),
) *domain.JobStatus {
	if !s.enabled() || task == nil {
		return nil
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
	if previous, err := s.store.Get(ctx, task.JobID); err == nil {
		status.Attempts = previous.Attempts
		status.QueuedAt = previous.QueuedAt
		if status.Title == "" {
			status.Title = previous.Title
		}
	}
	apply(&status)

	return &status
}

// save は状態を書き込み、失敗しても警告ログに留めます。
func (s statusRecorder) save(ctx context.Context, status *domain.JobStatus) {
	if status == nil {
		return
	}
	if err := s.store.Save(ctx, *status); err != nil {
		slog.WarnContext(ctx, "failed to record job status",
			"state", status.State,
			"error", err,
		)
	}
}
