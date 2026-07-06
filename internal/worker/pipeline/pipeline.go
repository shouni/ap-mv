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

// Runner は domain.Task を MusicRecipe 生成、キーフレーム生成、動画生成、公開の
// 各フィルターへ順番に流す worker パイプラインです。
type Runner struct {
	VideoRunner        ports.VideoRunner
	TaskQueue          ports.TaskQueue
	Filters            []filter.Filter
	OrchestratorConfig orchestrator.Config
	Workflows          *orchestrator.Workflows
	WorkflowFactory    func(context.Context, *domain.Task) (*orchestrator.Workflows, error)
	Reader             orchestrator.ContentReader
	Writer             remoteio.OutputWriter
	OutputBaseURI      string
	Notifier           ports.Notifier
	Characters         *characterkit.Characters
}

// New は VideoRunner と任意の orchestrator 設定から Runner を生成します。
//
// cfg が指定された場合は、タスクで選択されたモデルが既定値と異なるかを判定するために使われます。
func New(videoRunner ports.VideoRunner, cfg ...orchestrator.Config) *Runner {
	r := &Runner{VideoRunner: videoRunner}
	if len(cfg) > 0 {
		r.OrchestratorConfig = cfg[0]
	}
	return r
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
func (r *Runner) Close() error {
	if r == nil || r.VideoRunner == nil {
		return nil
	}
	if closer, ok := r.VideoRunner.(io.Closer); ok {
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
	workflows, err := r.workflowsForTask(ctx, task)
	if err != nil {
		return nil, err
	}
	fc := &filter.Context{
		Task:        task,
		Recipe:      task.Recipe,
		VideoRecipe: task.VideoRecipe,
		VideoRunner: r.VideoRunner,
		TaskQueue:   r.TaskQueue,
		Workflows:   workflows,
		Reader:      r.Reader,
		Writer:      r.Writer,
		Characters:  r.Characters,
		OutputPath:  r.outputPath(task),
	}
	filters := r.Filters
	if len(filters) == 0 {
		filters = defaultFilters(task.Command, r.VideoRunner)
	}
	for _, flt := range filters {
		if err := flt.Execute(ctx, fc); err != nil {
			if errors.Is(err, filter.ErrPipelineDeferred) {
				return &runResult{
					recipe:      fc.Recipe,
					videoRecipe: fc.VideoRecipe,
					outputPath:  fc.OutputPath,
					deferred:    true,
				}, nil
			}
			return nil, fmt.Errorf("filter %s: %w", flt.Name(), err)
		}
	}
	return &runResult{
		recipe:      fc.Recipe,
		videoRecipe: fc.VideoRecipe,
		outputPath:  fc.OutputPath,
	}, nil
}

func (r *Runner) notifyComplete(ctx context.Context, req domain.NotificationRequest) {
	if r == nil || r.Notifier == nil {
		return
	}
	notifyCtx, cancel := notificationContext(ctx)
	defer cancel()
	if err := r.Notifier.NotifyTaskComplete(notifyCtx, req); err != nil {
		slog.ErrorContext(ctx, "failed to send completion notification", "job_id", req.JobID, "error", err)
	}
}

func (r *Runner) notifyError(ctx context.Context, errDetail error, req domain.NotificationRequest) {
	if r == nil || r.Notifier == nil {
		return
	}
	notifyCtx, cancel := notificationContext(ctx)
	defer cancel()
	if err := r.Notifier.NotifyTaskError(notifyCtx, errDetail, req); err != nil {
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
		if result.videoRecipe != nil {
			result.videoRecipe.Normalize()
			req.Title = result.videoRecipe.MusicRecipe.Title
			req.CutCount = len(result.videoRecipe.Cuts)
		}
	}
	if req.Title == "" && task.Recipe != nil {
		req.Title = task.Recipe.Title
	}
	return req
}

// workflowsForTask はタスクで選択されたモデルに対応する Workflows を返します。
//
// タスクのモデル指定が Runner の既定設定と同じ場合は共有済み Workflows を使い、
// 異なる場合、またはキャラクターシードの一時的な上書きが指定されている場合のみ
// WorkflowFactory でタスク専用の Workflows を構築します。
func (r *Runner) workflowsForTask(ctx context.Context, task *domain.Task) (*orchestrator.Workflows, error) {
	if r == nil {
		return nil, nil
	}
	if r.WorkflowFactory == nil || (!r.usesCustomModels(task) && !usesSeedOverride(task)) {
		return r.Workflows, nil
	}
	workflows, err := r.WorkflowFactory(ctx, task)
	if err != nil {
		return nil, fmt.Errorf("build workflow for selected models: %w", err)
	}
	return workflows, nil
}

// usesCustomModels はタスクが Runner の既定モデル以外を指定しているかを判定します。
func (r *Runner) usesCustomModels(task *domain.Task) bool {
	if task == nil {
		return false
	}
	return !r.OrchestratorConfig.UsesModels(task.TextModel, task.ImageModel)
}

// usesSeedOverride はタスクがキャラクターシードの一時的な上書きを指定しているかを判定します。
func usesSeedOverride(task *domain.Task) bool {
	return task != nil && task.SeedOverride != nil && task.SeedOverrideCharacterID != ""
}

// outputPath はタスク成果物を配置するベースパスを返します。
//
// OutputBaseURI が未設定の場合は、フィルター側で保存先を指定しないため空文字を返します。
func (r *Runner) outputPath(task *domain.Task) string {
	if task == nil || strings.TrimSpace(r.OutputBaseURI) == "" {
		return ""
	}
	return strings.TrimRight(r.OutputBaseURI, "/") + "/" + task.JobID + "/"
}

// defaultFilters はタスクコマンドに応じた標準フィルター列を返します。
//
// compose/video_recipe_create 系はスクリプト生成から始め、recipe 入力系は既存 recipe の読み込みから始めます。
// video_recipe_create はキーフレーム生成までで停止し、動画生成と公開は実行しません。
// regenerate_cut_keyframe は指定カット 1 枚のキーフレームのみ再生成します。
func defaultFilters(command domain.TaskCommand, videoRunner ports.VideoRunner) []filter.Filter {
	switch command {
	case domain.CommandRegenerateCutKeyframe:
		return []filter.Filter{
			filter.RecipeLoadFilter{},
			filter.RegenerateCutKeyframeFilter{},
			filter.ZipUploadFilter{},
		}
	case domain.CommandRegenerateZip:
		return []filter.Filter{
			filter.RecipeLoadFilter{},
			filter.ZipUploadFilter{},
		}
	}
	filters := []filter.Filter{}
	switch command {
	case domain.CommandCompose, domain.CommandVideoRecipeCreate, domain.CommandComposeToKeyframe:
		filters = append(filters, filter.ScriptingFilter{})
	default:
		filters = append(filters, filter.RecipeLoadFilter{})
	}
	filters = append(filters,
		filter.CutKeyframeFilter{},
		filter.ZipUploadFilter{},
	)
	if command == domain.CommandVideoRecipeCreate || command == domain.CommandComposeToKeyframe {
		return filters
	}
	filters = append(filters,
		filter.VideoGenerationFilter{Runner: videoRunner},
		filter.PublishingFilter{},
	)
	return filters
}
