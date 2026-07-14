// Package pipeline は、Filter群を順に実行する動画生成ワーカーのパイプライン実行器を提供します。
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	characterkit "github.com/shouni/go-character-kit/character"
	"github.com/shouni/go-remote-io/remoteio"
	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"github.com/shouni/ap-mv/internal/domain"
	"github.com/shouni/ap-mv/internal/ports"
	"github.com/shouni/ap-mv/internal/worker/filter"
)

const notificationTimeout = 10 * time.Second

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
// worker は MusicRecipe の戻り値を使わないため、Run の結果から error だけを返します。
// task は TaskExecutor のシグネチャに合わせて値で受け取り、Run へ渡す時点でポインタ化します。
func (r *Runner) Execute(ctx context.Context, task domain.Task) error {
	result, err := r.run(ctx, &task)
	req := notificationRequest(&task, result)
	if err != nil {
		r.notifyError(ctx, err, req)
		return err
	}
	if result == nil || !result.deferred {
		r.notifyComplete(ctx, req)
	}
	return err
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
		State: filter.State{
			Task:        task,
			Recipe:      task.Recipe,
			VideoRecipe: task.VideoRecipe,
			OutputPath:  r.outputPath(task),
		},
		Services: filter.Services{
			VideoRunner:       videoRunner,
			TaskQueue:         r.deps.TaskQueue,
			Workflows:         workflows,
			Reader:            r.deps.Reader,
			Writer:            r.deps.Writer,
			Characters:        r.deps.Characters,
			HistoryRepository: r.deps.HistoryRepository,
		},
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
