// Package pipeline は、Step 群を順に実行する動画生成ワーカーのパイプライン実行器を提供します。
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/shouni/gcp-kit/worker"
	characterkit "github.com/shouni/go-character-kit/character"
	"github.com/shouni/go-remote-io/remoteio"
	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"github.com/shouni/go-utils/slogctx"

	"github.com/shouni/ap-mv/internal/domain"
	"github.com/shouni/ap-mv/internal/pipeline/step"
	"github.com/shouni/ap-mv/internal/ports"
)

// notificationTimeout は通知 1 回に与える上限です。結末の記録と通知の全体には
// Lifecycle が別に上限を持ちます（DefaultFinishTimeout）。
const notificationTimeout = 10 * time.Second

// Dependencies は Runner の実行に必要な依存の束です。New が必須依存の欠落を
// 生成時に検証するため、実行時まで依存不足が分からない状態を防ぎます。
type Dependencies struct {
	VideoRunner       ports.VideoRunner
	TaskQueue         ports.TaskQueue
	Reader            orchestrator.ContentReader
	Writer            remoteio.Writer
	Characters        *characterkit.Characters
	HistoryRepository ports.HistoryRepository
	// WorkflowResolver はタスクに応じた orchestrator Workflows を解決します。
	WorkflowResolver WorkflowResolver
	// Planner はタスクごとの実行計画（ステップ列）を決定します。
	// 未設定の場合はゼロ値の DefaultPlanner を使います（任意）。
	Planner Planner
	// Notifier は完了・エラー通知を送ります。未設定の場合は通知しません（任意）。
	Notifier ports.Notifier
	// OutputBaseURI はタスク成果物のベース URI です。空の場合はステップ側で
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
// 各ステップへ順番に流す worker パイプラインです。
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

// Execute は worker.TaskExecutor を満たします。
//
// ジョブの一生（再配信ガード → 検証 → 実行 → 結末の記録）は gcp-kit/worker.Lifecycle が
// 持ち、ここはそれぞれの中身だけを渡します（public-docs のワーカー規約）。panic の回復、
// 実行時間の上限、結末の記録の ctx の切り離しはライブラリ側の仕事で、ここには書きません。
// task は TaskExecutor のシグネチャに合わせて値で受け取り、run へ渡す時点でポインタ化します。
func (r *Runner) Execute(ctx context.Context, task domain.Task) error {
	return r.lifecycle().Execute(ctx, task)
}

// lifecycle は、ジョブの一生の各段にこのアプリの中身を当てはめます。
func (r *Runner) lifecycle() worker.Lifecycle[domain.Task, *runResult] {
	// 記録先は Dependencies から毎回引きます。テストが Runner をリテラルで組み立てても
	// 記録が効くようにするためで、Recorder の生成は軽い。
	status := newStatusRecorder(r.deps.JobStatus)
	return worker.Lifecycle[domain.Task, *runResult]{
		// 以降このジョブから出るログすべてに job_id / command を載せ、
		// 各ステップのログを 1 ジョブ単位で追えるようにする。
		// 継続タスク（video_gen_continuation）は同じ job_id を引き継ぐため、
		// カット分割されたチェーン全体が 1 本の job_id でまとまる。
		Prepare: func(ctx context.Context, task domain.Task) context.Context {
			return slogctx.With(ctx,
				slog.String("job_id", task.JobID),
				slog.String("command", string(task.Command)),
			)
		},
		Labels: func(task domain.Task) map[string]string {
			return map[string]string{"job_id": task.JobID, "command": string(task.Command)}
		},
		// 再配信ガード。通知の失敗などで一度エラーを返しただけでも再配信されるため、ここで
		// 打ち切らないと Veo の生成コストがそのまま二重に発生します。未完了ならここで
		// running を記録します（入力検証より前。全試行が Attempts に載ります）。状態を
		// 読めなければ判断できないので、実行せずにエラーを返します。
		Begin: func(ctx context.Context, task domain.Task) (bool, error) {
			return status.begin(ctx, &task)
		},
		// 依頼そのものが不正なら、配り直しても同じ行で落ちます（Lifecycle が Permanent に包みます）。
		Validate: func(task domain.Task) error { return task.Validate() },
		Run: func(ctx context.Context, task domain.Task) (*runResult, error) {
			return r.run(ctx, &task)
		},
		Finish: func(ctx context.Context, task domain.Task, result *runResult, cause error) error {
			return r.recordOutcome(ctx, &task, status, result, cause)
		},
		// Cloud Tasks の dispatch deadline より短く保つこと。長いと先に Cloud Tasks が
		// リクエストを打ち切り、アプリが自分の失敗を書く機会を失う。
		Timeout: r.deps.Timeout,
	}
}

// recordOutcome は終端状態の記録と通知を、成功・失敗の別なく同じ経路で行います
// （Lifecycle の Finish）。
//
// 打ち切りこそが終端の理由である場面（Timeout の発火、Cloud Tasks の dispatch deadline
// 超過によるリクエストのキャンセル）では呼び出し元の ctx は既に Done です。切り離しは
// Lifecycle が行い、成功と失敗のどちらもここを通るので、片方だけ切り離し忘れる書き方が
// 表現できません。
func (r *Runner) recordOutcome(ctx context.Context, task *domain.Task, status statusRecorder, result *runResult, cause error) error {
	req := notificationRequest(task, result)

	switch {
	case cause != nil:
		status.markFailed(ctx, task, cause, resultVideoRecipe(result))
		r.notifyError(ctx, cause, req)
		return cause

	case result != nil && result.deferred:
		// 継続タスクが同じ job_id で処理を引き継ぐため、ここで succeeded にすると
		// 再配信ガードが途中で発動して残りのカットが生成されなくなる。
		// 完了通知を送らない条件とまったく同じ判定を使う。
		return nil

	default:
		// 記録 → 通知の順。記録できていないジョブの成功を人に知らせない。
		status.markSucceeded(ctx, task, req, resultVideoRecipe(result))
		r.notifyComplete(ctx, req)
		return nil
	}
}

// resultVideoRecipe は、実行後のレシピを取り出します。ステップ列が途中で落ちた場合は
// nil になり、見出しは前回の記録のままになります。
func resultVideoRecipe(result *runResult) *domain.VideoRecipe {
	if result == nil {
		return nil
	}
	return result.videoRecipe
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

// Run はタスク内容に応じたステップ列を実行し、処理後の MusicRecipe を返します。
//
// Cloud Tasks worker からは Execute 経由で呼ばれますが、Run はテストや同期実行で
// パイプライン結果を確認したい場合の本体メソッドとして残しています。
func (r *Runner) Run(ctx context.Context, task *domain.Task) (*domain.MusicRecipe, error) {
	if err := task.Validate(); err != nil {
		return nil, err
	}
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
	workflows, err := r.resolveWorkflows(ctx, task)
	if err != nil {
		return nil, err
	}
	videoRunner := ports.DeriveVideoRunner(r.deps.VideoRunner, task.VeoModel, task.VeoAspectRatio)
	sc := &step.Context{
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
	steps, err := r.deps.Planner.Plan(task, videoRunner)
	if err != nil {
		return nil, err
	}
	for _, st := range steps {
		if err := st.Execute(ctx, sc); err != nil {
			if errors.Is(err, step.ErrPipelineDeferred) {
				return newRunResult(sc, true), nil
			}
			return nil, fmt.Errorf("step %s: %w", st.Name(), err)
		}
	}
	return newRunResult(sc, false), nil
}

// newRunResult はステップ実行後の Context から結果を組み立てます。
// VideoRecipe の正規化はここ（パイプライン実行の一部）で済ませ、通知データ作成
// （notificationRequest）が実行結果を変更する副作用を持たないようにします。
func newRunResult(sc *step.Context, deferred bool) *runResult {
	if sc.VideoRecipe != nil {
		sc.VideoRecipe.Normalize()
	}
	return &runResult{
		recipe:      sc.Recipe,
		videoRecipe: sc.VideoRecipe,
		outputPath:  sc.OutputPath,
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

// notificationContext は通知 1 回に上限を与えます。ctx は Lifecycle が呼び出し元から
// 切り離して渡してくるので、ここでは切り離しません。
func notificationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, notificationTimeout)
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
// OutputBaseURI が未設定の場合は、ステップ側で保存先を指定しないため空文字を返します。
func (r *Runner) outputPath(task *domain.Task) string {
	if task == nil || strings.TrimSpace(r.deps.OutputBaseURI) == "" {
		return ""
	}
	return strings.TrimRight(r.deps.OutputBaseURI, "/") + "/" + task.JobID + "/"
}
