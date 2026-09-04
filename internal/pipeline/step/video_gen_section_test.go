package step

import (
	"context"
	"errors"
	"testing"

	"github.com/shouni/go-veo-orchestrator/video"

	"github.com/shouni/ap-mv/internal/domain"
)

// sectionScopedRecipe は 2 セクション・各 2 カットのレシピを組み立てます。
// cut.SectionIndex は1始まりです（Normalize / SectionSelectStep と同じ規則）。
func sectionScopedRecipe() *video.Recipe {
	return &video.Recipe{
		MusicRecipe: video.MusicRecipe{
			Title: "test",
			Sections: []video.Section{
				{Name: "Verse", Duration: 16, StartSeconds: 0, EndSeconds: 16},
				{Name: "Chorus", Duration: 16, StartSeconds: 16, EndSeconds: 32},
			},
		},
		Cuts: []video.Cut{
			{CutIndex: 1, SectionIndex: 1, VisualAnchor: "v1", StartSec: 0, DurationSec: 8},
			{CutIndex: 2, SectionIndex: 1, VisualAnchor: "v2", StartSec: 8, DurationSec: 8},
			{CutIndex: 3, SectionIndex: 2, VisualAnchor: "c1", StartSec: 16, DurationSec: 8},
			{CutIndex: 4, SectionIndex: 2, VisualAnchor: "c2", StartSec: 24, DurationSec: 8},
		},
	}
}

func sectionScopedContext(recipe *video.Recipe, sectionIndex int, queue *captureQueue) *Context {
	return &Context{
		Task: &domain.Task{
			JobID:        "job-1",
			Command:      domain.CommandSectionVideo,
			SectionIndex: &sectionIndex,
			VideoRecipe:  recipe,
		},
		VideoRecipe: recipe,
		TaskQueue:   queue,
	}
}

// TestSectionScopedGenerationKeepsOtherSectionsInTheRecipe は、この機能の一番壊れては
// いけない性質を留めます。対象外セクションのカットは「飛ばす」だけで、レシピからは
// 取り除かれてはいけません。継続タスクはレシピをペイロードで持ち回り、最後の Publishing が
// それをそのまま元ジョブへ保存するため、ここで削れば他セクションのカットは復元不能に消えます。
func TestSectionScopedGenerationKeepsOtherSectionsInTheRecipe(t *testing.T) {
	recipe := sectionScopedRecipe()
	queue := &captureQueue{}
	st := VideoGenerationStep{Runner: sequenceRunner{}, SectionScoped: true}

	// セクション 2（0始まり index 1）だけを生成する。
	err := st.Execute(context.Background(), sectionScopedContext(recipe, 1, queue))
	if err != nil && !errors.Is(err, ErrPipelineDeferred) {
		t.Fatalf("Execute() error = %v", err)
	}

	if len(recipe.Cuts) != 4 {
		t.Fatalf("recipe has %d cuts, want 4 (the untargeted section must survive)", len(recipe.Cuts))
	}
	for _, cut := range recipe.Cuts {
		if cut.SectionIndex == 1 && cut.Status == video.CutStatusGenerated {
			t.Errorf("cut %d is outside the target section but was generated", cut.CutIndex)
		}
	}
	if recipe.Cuts[2].Status != video.CutStatusGenerated {
		t.Errorf("first cut of the target section was not generated (status %q)", recipe.Cuts[2].Status)
	}
	// 継続タスクへ渡されるレシピも削れていないこと。
	if len(queue.tasks) > 0 && len(queue.tasks[0].VideoRecipe.Cuts) != 4 {
		t.Errorf("continuation carried %d cuts, want 4", len(queue.tasks[0].VideoRecipe.Cuts))
	}
}

// TestSectionScopedGenerationStopsWhenTheSectionIsDone は、担当セクションを焼き終えたら
// 継続タスクを撒かないことを留めます。範囲を見ずに「未生成カットが残っているか」だけで
// 判断すると、他セクションのぶんを数えて何も生成しない継続を毎回1本余計に投げます。
func TestSectionScopedGenerationStopsWhenTheSectionIsDone(t *testing.T) {
	recipe := sectionScopedRecipe()
	// 対象セクションのカットを1つ残して生成済みにしておく。
	recipe.Cuts[2].Status = video.CutStatusGenerated
	recipe.Cuts[2].VideoID = "vid-3"
	recipe.Cuts[2].VideoURL = "gs://bucket/jobs/job-1/videos/cut_3.mp4"

	queue := &captureQueue{}
	st := VideoGenerationStep{Runner: sequenceRunner{}, SectionScoped: true}

	if err := st.Execute(context.Background(), sectionScopedContext(recipe, 1, queue)); err != nil {
		t.Fatalf("Execute() error = %v, want the run to finish without deferring", err)
	}
	if len(queue.tasks) != 0 {
		t.Errorf("enqueued %d continuation tasks, want 0 (the target section is complete)", len(queue.tasks))
	}
	if recipe.Cuts[3].Status != video.CutStatusGenerated {
		t.Errorf("last cut of the target section was not generated (status %q)", recipe.Cuts[3].Status)
	}
	if recipe.Cuts[0].Status == video.CutStatusGenerated || recipe.Cuts[1].Status == video.CutStatusGenerated {
		t.Error("cuts outside the target section were generated")
	}
}

// TestSectionScopedGenerationMarksChainStartAfterASkippedGap は、飛ばした穴の直後のカットが
// チェーンの起点として印付けされることを留めます。ここが抜けると chain_finalize が境界を
// 見落とし、直前のチェーンの最終動画を結合対象から落とすため、完成動画からそのセクションが
// 丸ごと消えます（生成もコストも済んだあとで、静かに）。
func TestSectionScopedGenerationMarksChainStartAfterASkippedGap(t *testing.T) {
	recipe := sectionScopedRecipe()
	queue := &captureQueue{}
	st := VideoGenerationStep{Runner: sequenceRunner{}, SectionScoped: true, UsePreviousVideo: true}

	err := st.Execute(context.Background(), sectionScopedContext(recipe, 1, queue))
	if err != nil && !errors.Is(err, ErrPipelineDeferred) {
		t.Fatalf("Execute() error = %v", err)
	}
	if !recipe.Cuts[2].IsChainStart {
		t.Error("the first generated cut after the skipped section is not marked as a chain start")
	}
}

// TestSectionScopedGenerationRejectsAnEmptySection は、対象セクションにカットが1つも無い
// ときにエラーになることを留めます。黙って正常終了すると、押した本人には「動いたのに何も
// 起きない」としか見えません。
func TestSectionScopedGenerationRejectsAnEmptySection(t *testing.T) {
	recipe := sectionScopedRecipe()
	recipe.Cuts = recipe.Cuts[:2] // セクション2のカットを取り除く
	st := VideoGenerationStep{Runner: sequenceRunner{}, SectionScoped: true}

	if err := st.Execute(context.Background(), sectionScopedContext(recipe, 1, &captureQueue{})); err == nil {
		t.Fatal("Execute() = nil, want an error for a section with no cuts")
	}
}

// TestSectionScopedGenerationRejectsAnOutOfRangeSection pins the range check.
func TestSectionScopedGenerationRejectsAnOutOfRangeSection(t *testing.T) {
	st := VideoGenerationStep{Runner: sequenceRunner{}, SectionScoped: true}

	if err := st.Execute(context.Background(), sectionScopedContext(sectionScopedRecipe(), 9, &captureQueue{})); err == nil {
		t.Fatal("Execute() = nil, want an error for an out-of-range section_index")
	}
}

// TestContinuationRemembersTheOriginCommand は、継続タスクが元のコマンドを保持することを
// 留めます。Command は video_gen_continuation で上書きされるため、これが失われると
// 実行計画が「結合するか否か」を復元できず、section_video の続きが結合まで走ります。
func TestContinuationRemembersTheOriginCommand(t *testing.T) {
	recipe := sectionScopedRecipe()
	queue := &captureQueue{}
	st := VideoGenerationStep{Runner: sequenceRunner{}, SectionScoped: true}

	if err := st.Execute(context.Background(), sectionScopedContext(recipe, 1, queue)); !errors.Is(err, ErrPipelineDeferred) {
		t.Fatalf("Execute() error = %v, want ErrPipelineDeferred", err)
	}
	if len(queue.tasks) != 1 {
		t.Fatalf("enqueued tasks = %d, want 1", len(queue.tasks))
	}
	if got := queue.tasks[0].OriginCommand; got != domain.CommandSectionVideo {
		t.Errorf("continuation OriginCommand = %q, want %q", got, domain.CommandSectionVideo)
	}

	// 継続の継続でも元コマンドが残ること（Command は既に continuation なので上書きしない）。
	next := queue.tasks[0]
	nextRecipe := next.VideoRecipe
	nextQueue := &captureQueue{}
	nextCtx := &Context{
		Task: next, VideoRecipe: nextRecipe,
		TaskQueue: nextQueue,
	}
	if err := st.Execute(context.Background(), nextCtx); err != nil && !errors.Is(err, ErrPipelineDeferred) {
		t.Fatalf("continuation Execute() error = %v", err)
	}
	for _, task := range nextQueue.tasks {
		if task.OriginCommand != domain.CommandSectionVideo {
			t.Errorf("second continuation OriginCommand = %q, want it preserved", task.OriginCommand)
		}
	}
}

// TestUnscopedGenerationStillCoversEveryCut は、SectionScoped でない従来の実行が全カットを
// 対象にし続けることを留めます（絞り込みの導入で既定の挙動を変えていないこと）。
func TestUnscopedGenerationStillCoversEveryCut(t *testing.T) {
	recipe := sectionScopedRecipe()
	st := VideoGenerationStep{Runner: sequenceRunner{}}
	ctx := &Context{
		Task:        &domain.Task{JobID: "job-1", Command: domain.CommandMVFromKeyframeVideoRecipe, VideoRecipe: recipe},
		VideoRecipe: recipe,
	}
	// TaskQueue 無しなら継続へ逃げず、その場で全カットを生成する。
	if err := st.Execute(context.Background(), ctx); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, cut := range recipe.Cuts {
		if cut.Status != video.CutStatusGenerated {
			t.Errorf("cut %d status = %q, want generated", cut.CutIndex, cut.Status)
		}
	}
}
