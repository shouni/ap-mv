// Package pipeline は、Filter群を順に実行する動画生成ワーカーのパイプライン実行器を提供します。
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"runtime/pprof"
	"strings"
	"time"

	characterkit "github.com/shouni/go-character-kit/character"
	"github.com/shouni/go-remote-io/remoteio"
	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"github.com/shouni/go-utils/slogctx"

	"github.com/shouni/ap-mv/internal/domain"
	"github.com/shouni/ap-mv/internal/ports"
	"github.com/shouni/ap-mv/internal/worker/filter"
)

const (
	notificationTimeout = 10 * time.Second
	statusRecordTimeout = 30 * time.Second
)

// Dependencies は Runner の実行に必要な依存の束です。New が必須依存の欠落を
// 生成時に検証するため、実行時まで依存不足が分からない状態を防ぎます。
type Dependencies struct {
	VideoRunner       ports.VideoRunner
	TaskQueue         ports.TaskQueue
	Reader            orchestrator.ContentReader
	Writer            remoteio.OutputWriter
	Characters        *characterkit.Characters
	HistoryRepository ports.HistoryRepository
	// WorkflowResolver はタスクに応じた orchestrator Workflows を解決します。
	WorkflowResolver WorkflowResolver
	// Planner はタスクごとの実行計画（フィルター列）を決定します。
	// 未設定の場合はゼロ値の DefaultPlanner を使います（任意）。
	Planner FilterPlanner
	// Notifier は完了・エラー通知を送ります。未設定の場合は通知しません（任意）。
	Notifier ports.Notifier
	// OutputBaseURI はタスク成果物のベース URI です。空の場合はフィルター側で
	// 保存先を指定しません（任意）。
	OutputBaseURI string
	// Timeout はタスク 1 件の実行時間の上限です。0 以下は無制限を意味します（任意）。
	// カット分割された継続タスクにはそれぞれ個別に適用されます。
	Timeout time.Duration
	// JobStatus はジョブ進行状況の記録先です。未設定の場合は状態記録と
	// 再実行ガードが無効になります（任意）。
	JobStatus ports.JobStatusStore
}

// Runner は domain.Task を MusicRecipe 生成、キーフレーム生成、動画生成、公開の
// 各フィルターへ順番に流す worker パイプラインです。
type Runner struct {
	deps Dependencies
}

// New は依存を検証して Runner を生成します。必須依存が欠けている場合はエラーを返し、
// 実行時ではなく起動時に構成ミスを検出します。
func New(deps Dependencies) (*Runner, error) {
	var missing []string
	for _, dep := range []struct {
		name string
		ok   bool
	}{
		{"VideoRunner", deps.VideoRunner != nil},
		{"TaskQueue", deps.TaskQueue != nil},
		{"Reader", deps.Reader != nil},
		{"Writer", deps.Writer != nil},
		{"Characters", deps.Characters != nil},
		{"HistoryRepository", deps.HistoryRepository != nil},
		{"WorkflowResolver", deps.WorkflowResolver != nil},
	} {
		if !dep.ok {
			missing = append(missing, dep.name)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("pipeline runner missing required dependencies: %s", strings.Join(missing, ", "))
	}
	if deps.Planner == nil {
		deps.Planner = DefaultPlanner{}
	}
	return &Runner{deps: deps}, nil
}

// Execute は gcp-kit/worker.TaskExecutor に適合するためのエントリーポイントです。
//
// worker は MusicRecipe の戻り値を使わないため、run の結果から error だけを返します。
// task は TaskExecutor のシグネチャに合わせて値で受け取り、run へ渡す時点でポインタ化します。
// 終端状態（succeeded / failed）の記録と通知は recordOutcome が一手に行います。
func (r *Runner) Execute(ctx context.Context, task domain.Task) error {
	// 以降このジョブから出るログすべてに job_id / command を載せ、
	// 各フィルターのログを 1 ジョブ単位で追えるようにする。
	// 継続タスク（video_gen_continuation）は同じ job_id を引き継ぐため、
	// カット分割されたチェーン全体が 1 本の job_id でまとまる。
	ctx = slogctx.With(ctx,
		slog.String("job_id", task.JobID),
		slog.String("command", string(task.Command)),
	)

	// ログの相関に加えて、pprof のゴルーチンラベルにも同じ値を載せます。
	// Go 1.27 以降、ラベルは**パニックのトレースバックの見出し行にも出る**ため、
	// 落ちたときにどのジョブだったかがスタックだけで分かります。slogctx は
	// panic の経路では効かないので、そこを埋めるのがこちらの役目です。
	// ラベルは子ゴルーチン（並列生成など）へも継承されます。
	ctx = pprof.WithLabels(ctx, pprof.Labels("job_id", task.JobID, "command", string(task.Command)))
	pprof.SetGoroutineLabels(ctx)

	// Cloud Tasks の再配信で完了済みジョブを作り直さないためのガード。
	// 通知の失敗などで一度エラーを返しただけでも再配信されるため、ここで打ち切らないと
	// Veo の生成コストがそのまま二重に発生します。
	status := newStatusRecorder(r.deps.JobStatus)
	// 未完了ならここで running を記録する（入力検証より前。全試行が Attempts に載る）。
	done, err := status.begin(ctx, &task)
	if err != nil {
		// 状態を読めない。判断できないので再配信に委ねる。
		return err
	}
	if done {
		slog.InfoContext(ctx, "skipping already completed job")
		return nil
	}

	result, err := r.runWithTimeout(ctx, &task)
	return r.recordOutcome(ctx, &task, status, result, err)
}

// runWithTimeout はフィルター列を実行時間の上限つきで実行します。
//
// 上限は、Veo の動画生成が応答しなくなった場合に Cloud Run のインスタンスを占有し
// 続けないためのものです。締切はフィルターの実行にだけ被せ、呼び出し元の ctx には
// 影響させません。ctx を上書きすると、打ち切られた直後の終端記録まで期限切れの
// context で行うことになり、いちばん記録が要る場面で残りません。
//
// この上限は Cloud Tasks の dispatch deadline より**短く**保つこと。長いと先に
// Cloud Tasks がリクエストを打ち切り、アプリが自分の失敗を書く機会を失う。
func (r *Runner) runWithTimeout(ctx context.Context, task *domain.Task) (*runResult, error) {
	if r.deps.Timeout <= 0 {
		return r.run(ctx, task)
	}

	runCtx, cancel := context.WithTimeout(ctx, r.deps.Timeout)
	defer cancel()

	return r.run(runCtx, task)
}

// recordOutcome は終端状態の記録と通知を、成功・失敗の別なく同じ経路で行います。
//
// 記録も通知も打ち切られた context から切り離して行います。打ち切りこそが終端の理由で
// ある場面（Timeout の発火、Cloud Tasks の dispatch deadline 超過によるリクエストの
// キャンセル）では ctx は既に Done で、そのまま使うと GCS への状態書き込みも Slack 通知も
// 失敗する。状態は running のまま固着し、mv-queue は max_attempts = 1 なので再試行も
// 来ない。記録失敗は Recorder が握り潰すため、ジョブが黙って消えたように見える。
// これは失敗だけでなく、期限や切断と前後して完了したジョブの「成功」の記録にも
// そのまま当てはまる。
func (r *Runner) recordOutcome(
	ctx context.Context,
	task *domain.Task,
	status statusRecorder,
	result *runResult,
	cause error,
) error {
	req := notificationRequest(task, result)

	switch {
	case cause != nil:
		statusCtx, cancel := statusContext(ctx)
		defer cancel()

		status.markFailed(statusCtx, task, cause)
		r.notifyError(ctx, cause, req)
		return cause

	case result != nil && result.deferred:
		// 継続タスクが同じ job_id で処理を引き継ぐため、ここで succeeded にすると
		// 再配信ガードが途中で発動して残りのカットが生成されなくなる。
		// 完了通知を送らない条件とまったく同じ判定を使う。
		return nil

	default:
		statusCtx, cancel := statusContext(ctx)
		defer cancel()

		// 記録 → 通知の順。記録できていないジョブの成功を人に知らせない。
		status.markSucceeded(statusCtx, task, req)
		r.notifyComplete(ctx, req)
		return nil
	}
}

// Close は Runner が保持する実行時リソースを解放します。
//
// 構築成功後の VideoRunner のライフサイクルは Runner が所有します（app.Container.Close →
// Pipeline.Close 経由で解放される。構築途中の失敗時のみ builder.BuildContainer が解放する）。
func (r *Runner) Close() error {
	if r == nil || r.deps.VideoRunner == nil {
		return nil
	}
	if closer, ok := r.deps.VideoRunner.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

// Run はタスク内容に応じたフィルター列を実行し、処理後の MusicRecipe を返します。
//
// Cloud Tasks worker からは Execute 経由で呼ばれますが、Run はテストや同期実行で
// パイプライン結果を確認したい場合の本体メソッドとして残しています。
func (r *Runner) Run(ctx context.Context, task *domain.Task) (*domain.MusicRecipe, error) {
	result, err := r.run(ctx, task)
	if result == nil {
		return nil, err
	}
	return result.recipe, err
}

type runResult struct {
	recipe      *domain.MusicRecipe
	videoRecipe *domain.VideoRecipe
	outputPath  string
	deferred    bool
}

func (r *Runner) run(ctx context.Context, task *domain.Task) (*runResult, error) {
	if err := task.Validate(); err != nil {
		return nil, err
	}
	workflows, err := r.resolveWorkflows(ctx, task)
	if err != nil {
		return nil, err
	}
	videoRunner := ports.DeriveVideoRunner(r.deps.VideoRunner, task.VeoModel, task.VeoAspectRatio)
	fc := &filter.Context{
		Task:              task,
		Recipe:            task.Recipe,
		VideoRecipe:       task.VideoRecipe,
		OutputPath:        r.outputPath(task),
		VideoRunner:       videoRunner,
		TaskQueue:         r.deps.TaskQueue,
		Workflows:         workflows,
		Reader:            r.deps.Reader,
		Writer:            r.deps.Writer,
		Characters:        r.deps.Characters,
		HistoryRepository: r.deps.HistoryRepository,
	}
	// deps.Planner は New が DefaultPlanner{} で補完済みのため、ここでは nil になりません。
	filters, err := r.deps.Planner.Plan(task, videoRunner)
	if err != nil {
		return nil, err
	}
	for _, flt := range filters {
		if err := flt.Execute(ctx, fc); err != nil {
			if errors.Is(err, filter.ErrPipelineDeferred) {
				return newRunResult(fc, true), nil
			}
			return nil, fmt.Errorf("filter %s: %w", flt.Name(), err)
		}
	}
	return newRunResult(fc, false), nil
}

// newRunResult はフィルター実行後の Context から結果を組み立てます。
// VideoRecipe の正規化はここ（パイプライン実行の一部）で済ませ、通知データ作成
// （notificationRequest）が実行結果を変更する副作用を持たないようにします。
func newRunResult(fc *filter.Context, deferred bool) *runResult {
	if fc.VideoRecipe != nil {
		fc.VideoRecipe.Normalize()
	}
	return &runResult{
		recipe:      fc.Recipe,
		videoRecipe: fc.VideoRecipe,
		outputPath:  fc.OutputPath,
		deferred:    deferred,
	}
}

func (r *Runner) notifyComplete(ctx context.Context, req domain.NotificationRequest) {
	if r == nil || r.deps.Notifier == nil {
		return
	}
	notifyCtx, cancel := notificationContext(ctx)
	defer cancel()
	if err := r.deps.Notifier.NotifyTaskComplete(notifyCtx, req); err != nil {
		slog.ErrorContext(ctx, "failed to send completion notification", "job_id", req.JobID, "error", err)
	}
}

func (r *Runner) notifyError(ctx context.Context, errDetail error, req domain.NotificationRequest) {
	if r == nil || r.deps.Notifier == nil {
		return
	}
	notifyCtx, cancel := notificationContext(ctx)
	defer cancel()
	if err := r.deps.Notifier.NotifyTaskError(notifyCtx, errDetail, req); err != nil {
		slog.ErrorContext(ctx, "failed to send error notification", "job_id", req.JobID, "error", err)
	}
}

func notificationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), notificationTimeout)
}

// statusContext は失敗の記録用に、呼び出し元から切り離した短い context を返します。
// 通知より長いのは、GCS への書き込みがリトライを伴うためです。
func statusContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), statusRecordTimeout)
}

func notificationRequest(task *domain.Task, result *runResult) domain.NotificationRequest {
	if task == nil {
		return domain.NotificationRequest{}
	}
	historyJobID := task.JobID
	if task.OriginalJobID != "" {
		historyJobID = task.OriginalJobID
	}
	req := domain.NotificationRequest{
		JobID:        task.JobID,
		HistoryJobID: historyJobID,
		Command:      string(task.Command),
		SourceURL:    task.SourceURL,
		RecipeURL:    task.RecipeURL,
		AudioURL:     task.AudioURL,
		CharacterID:  task.CharacterID,
		VisualMode:   task.VisualMode,
		TextModel:    task.TextModel,
		ImageModel:   task.ImageModel,
		CreatedAt:    task.CreatedAt,
	}
	if result != nil {
		// 台本のみのジョブも完成ジョブと同じ jobs 配下へ保存するので、案内先は共通です。
		req.OutputURI = result.outputPath
		// videoRecipe は newRunResult で正規化済み。ここでは読み取りのみ行います。
		if result.videoRecipe != nil {
			req.Title = result.videoRecipe.MusicRecipe.Title
			req.CutCount = len(result.videoRecipe.Cuts)
		}
	}
	if req.Title == "" && task.Recipe != nil {
		req.Title = task.Recipe.Title
	}
	return req
}

// resolveWorkflows は WorkflowResolver へタスクに応じた Workflows の解決を委譲します。
// Resolver が未設定の場合は Workflows なし（VideoRunner 直接実行のみ）で実行します。
func (r *Runner) resolveWorkflows(ctx context.Context, task *domain.Task) (*orchestrator.Workflows, error) {
	if r == nil || r.deps.WorkflowResolver == nil {
		return nil, nil
	}
	return r.deps.WorkflowResolver.Resolve(ctx, task)
}

// outputPath はタスク成果物を配置するベースパスを返します。
//
// OutputBaseURI が未設定の場合は、フィルター側で保存先を指定しないため空文字を返します。
func (r *Runner) outputPath(task *domain.Task) string {
	if task == nil || strings.TrimSpace(r.deps.OutputBaseURI) == "" {
		return ""
	}
	return strings.TrimRight(r.deps.OutputBaseURI, "/") + "/" + task.JobID + "/"
}
