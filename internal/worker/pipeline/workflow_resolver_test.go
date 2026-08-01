package pipeline

import (
	"context"
	"testing"

	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"github.com/shouni/ap-mv/internal/domain"
)

// releaseRecordingResolver は、解決した Workflows の後始末が呼ばれたかを記録します。
type releaseRecordingResolver struct {
	workflows *orchestrator.Workflows
	released  bool
}

func (r *releaseRecordingResolver) Resolve(context.Context, *domain.Task) (*orchestrator.Workflows, func(), error) {
	return r.workflows, func() { r.released = true }, nil
}

// TestRunReleasesResolvedWorkflows は、1タスクの実行が終わったら解決した Workflows の
// 後始末が呼ばれることを確認します。タスク専用に構築した Workflows を閉じ損ねると、
// 画像キャッシュのクリーンアップ goroutine がタスクごとに積み上がります。
func TestRunReleasesResolvedWorkflows(t *testing.T) {
	resolver := &releaseRecordingResolver{workflows: &orchestrator.Workflows{}}
	runner := &Runner{deps: Dependencies{
		Planner:          StaticPlanner{noopFilter{}},
		WorkflowResolver: resolver,
		OutputBaseURI:    "gs://bucket/ap-mv/veo/jobs",
	}}

	// 実行そのものの成否は問わない。後始末が defer されているかだけを見る。
	_, _ = runner.run(context.Background(), &domain.Task{
		JobID:      "job-1",
		Command:    domain.CommandVideoRecipeCreate,
		SourceURL:  "gs://bucket/music_recipe.json",
		VisualMode: "sparkle_rock",
	})

	if !resolver.released {
		t.Error("resolved workflows were not released after the task finished")
	}
}

// TestStaticWorkflowResolverReleaseIsNoop は、テスト用の共有リゾルバが返す release が
// 安全に呼べることを確認します。
func TestStaticWorkflowResolverReleaseIsNoop(t *testing.T) {
	workflows := &orchestrator.Workflows{}
	got, release, err := StaticWorkflowResolver{Workflows: workflows}.Resolve(context.Background(), nil)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got != workflows {
		t.Error("Resolve() did not return the configured workflows")
	}
	release()
	release()
}
