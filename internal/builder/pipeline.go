package builder

import (
	"context"
	"fmt"
	"strings"

	"github.com/shouni/go-gemini-client/gemini"
	"github.com/shouni/go-http-kit/httpkit"
	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"github.com/shouni/ap-mv/internal/adapters"
	"github.com/shouni/ap-mv/internal/app"
	"github.com/shouni/ap-mv/internal/config"
	"github.com/shouni/ap-mv/internal/domain"
	"github.com/shouni/ap-mv/internal/ports"
	"github.com/shouni/ap-mv/internal/worker/pipeline"
)

// pipelineExternals は、BuildContainer 側で先に組み立てられ buildPipeline へ渡される
// パイプライン外部依存です。
type pipelineExternals struct {
	notifier          ports.Notifier
	taskQueue         ports.TaskQueue
	historyRepository ports.HistoryRepository
	jobStatus         ports.JobStatusStore
}

// buildPipeline は、パイプラインの実行に必要な境界実装を注入して返します。
// 必須依存の検証は pipeline.New が行うため、後からのフィールド代入はありません。
func buildPipeline(
	ctx context.Context,
	cfg *config.Config,
	rio *app.RemoteIO,
	httpClient httpkit.HTTPClient,
	videoRunner ports.VideoRunner,
	aiClient gemini.Model,
	externals pipelineExternals,
) (*pipeline.Runner, error) {
	workflows, err := buildWorkflow(ctx, cfg, rio, httpClient, videoRunner, aiClient)
	if err != nil {
		return nil, err
	}
	characters, err := buildCharacters()
	if err != nil {
		return nil, fmt.Errorf("load characters for pipeline: %w", err)
	}
	deps := pipeline.Dependencies{
		VideoRunner:       videoRunner,
		TaskQueue:         externals.taskQueue,
		Characters:        characters,
		HistoryRepository: externals.historyRepository,
		WorkflowResolver:  newWorkflowResolver(cfg, rio, httpClient, videoRunner, aiClient, workflows),
		Notifier:          externals.notifier,
		OutputBaseURI:     workflowOutputBaseURI(cfg),
		DraftBaseURI:      workflowDraftBaseURI(cfg),
		Timeout:           cfg.AI.PipelineTimeout,
		JobStatus:         externals.jobStatus,
	}
	planner := &pipeline.DefaultPlanner{UsePreviousVideo: cfg.AI.VeoUsePreviousVideo}
	deps.Planner = planner
	if rio != nil {
		deps.Reader = workflowReader{delegate: rio.Reader}
		deps.Writer = rio.Writer
		planner.VideoProcessor = adapters.NewFFmpegVideoProcessor(deps.Reader, deps.Writer)
	}
	return pipeline.New(deps)
}

// newWorkflowResolver は、共有 Workflows とタスク専用構築処理を束ねた本番用
// workflowResolver を組み立てます。
func newWorkflowResolver(
	cfg *config.Config,
	rio *app.RemoteIO,
	httpClient httpkit.HTTPClient,
	videoRunner ports.VideoRunner,
	aiClient gemini.Model,
	shared *orchestrator.Workflows,
) *workflowResolver {
	orchCfg := buildOrchestratorConfig(cfg)
	return &workflowResolver{
		orchCfg: orchCfg,
		shared:  shared,
		build: func(ctx context.Context, task *domain.Task) (*orchestrator.Workflows, error) {
			taskOrchCfg := orchCfg
			taskVideoRunner := videoRunner
			if task != nil {
				taskOrchCfg = taskOrchCfg.WithModels(task.TextModel, task.ImageModel).WithAspectRatio(task.VeoAspectRatio)
				taskVideoRunner = ports.DeriveVideoRunner(videoRunner, task.VeoModel, task.VeoAspectRatio)
			}
			return buildWorkflowWithConfig(ctx, workflowBuildParams{
				cfg:          cfg,
				orchCfg:      taskOrchCfg,
				rio:          rio,
				httpClient:   httpClient,
				videoRunner:  taskVideoRunner,
				aiClient:     aiClient,
				visualMode:   taskVisualMode(task),
				seedOverride: taskSeedOverride(task),
			})
		},
	}
}

// workflowResolver は pipeline.WorkflowResolver の本番実装です。タスクが既定モデル・
// シード・Veo 設定のままなら共有済み Workflows を再利用し、タスク固有オプションが
// 指定されている場合のみ build でタスク専用の Workflows を構築します。
// 「タスク専用構築が必要か」の判定と構築処理をこの型に集約しているため、
// タスク固有オプションを追加するときはここだけを変更すれば済みます。
type workflowResolver struct {
	orchCfg orchestrator.Config
	shared  *orchestrator.Workflows
	build   func(context.Context, *domain.Task) (*orchestrator.Workflows, error)
}

// Resolve はタスクに応じた Workflows を返します。
func (r *workflowResolver) Resolve(ctx context.Context, task *domain.Task) (*orchestrator.Workflows, func(), error) {
	// 共有インスタンスはプロセス全体で使い回すので閉じない。
	if r.build == nil || (!r.usesCustomModels(task) && !usesSeedOverride(task) && !usesVeoOptions(task)) {
		return r.shared, func() {}, nil
	}
	workflows, err := r.build(ctx, task)
	if err != nil {
		return nil, func() {}, fmt.Errorf("build workflow for selected models: %w", err)
	}
	// Workflows に Close はありません。参照画像を gs:// のまま渡すようになり、
	// 停止すべき画像キャッシュの goroutine が無くなったためです。
	return workflows, func() {}, nil
}

// usesCustomModels はタスクが既定モデル以外を指定しているかを判定します。
func (r *workflowResolver) usesCustomModels(task *domain.Task) bool {
	if task == nil {
		return false
	}
	return !r.orchCfg.UsesModels(task.TextModel, task.ImageModel)
}

// usesSeedOverride はタスクがキャラクターシードの一時的な上書きを指定しているかを判定します。
func usesSeedOverride(task *domain.Task) bool {
	return task != nil && task.SeedOverride != nil && task.SeedOverrideCharacterID != ""
}

// usesVeoOptions はタスクが Veo モデルまたはアスペクト比の差し替えを指定しているかを判定します。
// 指定がある場合は、共有 Workflows ではなく差し替え済み VideoRunner を持つ
// タスク専用 Workflows を構築する必要があります。
func usesVeoOptions(task *domain.Task) bool {
	if task == nil {
		return false
	}
	return strings.TrimSpace(task.VeoModel) != "" || strings.TrimSpace(task.VeoAspectRatio) != ""
}

func taskVisualMode(task *domain.Task) string {
	if task == nil {
		return ""
	}
	return task.VisualMode
}

// taskSeedOverride は、タスクに指定されたキャラクターシード上書きを characterSeedOverride に変換します。
// 指定がなければ nil を返し、buildWorkflowWithConfig は既定のキャラクター定義をそのまま使います。
func taskSeedOverride(task *domain.Task) *characterSeedOverride {
	if task == nil || task.SeedOverride == nil || task.SeedOverrideCharacterID == "" {
		return nil
	}
	return &characterSeedOverride{
		characterID: task.SeedOverrideCharacterID,
		seed:        *task.SeedOverride,
	}
}
