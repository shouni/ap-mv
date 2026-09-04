package step

import (
	"context"
	"fmt"
	"testing"

	orchestrator "github.com/shouni/go-veo-orchestrator/ports"
	"github.com/shouni/go-veo-orchestrator/video"

	"github.com/shouni/ap-mv/internal/domain"
)

// fakeCutKeyframeRunner records which of RunAndSave/EditAndSave was called, so tests can
// assert the step picked the right mode (full regenerate vs. local edit).
type fakeCutKeyframeRunner struct {
	runAndSaveCalled   bool
	editAndSaveCalled  bool
	editPromptSeen     string
	keyframeSeenAtEdit string
	resultKeyframeRef  string
	// runAndSaveCuts records the CutIndex list of each RunAndSave call, so section tests can
	// assert every target cut went through one batched call rather than several.
	runAndSaveCuts [][]int
	// editPaths records the output path of each EditAndSave call, so section tests can assert
	// per-cut calls do not collide on the same keyframe_1.png destination.
	editPaths []string
	// keyframeRefsAtRunAndSave records the KeyframeReference values as they arrived, before the
	// fake overwrites them. Skipping already-baked cuts is the runner's job (go-veo-orchestrator),
	// so what ap-mv must be checked for is that it hands those references through untouched.
	keyframeRefsAtRunAndSave []string
}

func (f *fakeCutKeyframeRunner) GenerateAndSave(_ context.Context, recipe *video.Recipe, _ string) (*video.Recipe, error) {
	f.runAndSaveCalled = true
	cutIndexes := make([]int, 0, len(recipe.Cuts))
	for i := range recipe.Cuts {
		f.keyframeRefsAtRunAndSave = append(f.keyframeRefsAtRunAndSave, recipe.Cuts[i].KeyframeReference)
		recipe.Cuts[i].KeyframeReference = f.resultKeyframeRef
		cutIndexes = append(cutIndexes, recipe.Cuts[i].CutIndex)
	}
	f.runAndSaveCuts = append(f.runAndSaveCuts, cutIndexes)
	return recipe, nil
}

func (f *fakeCutKeyframeRunner) EditAndSave(_ context.Context, recipe *video.Recipe, _ int, editPrompt string, outputPath string) (*video.Recipe, error) {
	f.editAndSaveCalled = true
	f.editPromptSeen = editPrompt
	f.keyframeSeenAtEdit = recipe.Cuts[0].KeyframeReference
	f.editPaths = append(f.editPaths, outputPath)
	recipe.Cuts[0].KeyframeReference = f.resultKeyframeRef
	return recipe, nil
}

func newRegenTestContext(task *domain.Task, runner *fakeCutKeyframeRunner) *Context {
	cutIndex := 1
	if task.CutIndex == nil {
		task.CutIndex = &cutIndex
	}
	recipe := &video.Recipe{
		ProjectTitle: "test",
		Cuts: []video.Cut{
			{
				CutIndex:          *task.CutIndex,
				VisualAnchor:      "original anchor",
				KeyframeReference: "gs://bucket/jobs/orig/images/keyframe_1.png",
			},
		},
	}
	return &Context{
		Task: task, VideoRecipe: recipe, OutputPath: "gs://bucket/jobs/regen-1/",
		Workflows: &orchestrator.Workflows{CutKeyframe: runner},
	}
}

// newRegenSectionTestContext builds a 3-cut recipe spanning two sections (cuts 1-2 in section 1,
// cut 3 in section 2) for exercising the section-targeted regeneration path.
func newRegenSectionTestContext(task *domain.Task, runner *fakeCutKeyframeRunner) *Context {
	cut := func(index, sectionIndex int, startSec float64) video.Cut {
		return video.Cut{
			CutIndex:     index,
			SectionIndex: sectionIndex,
			VisualAnchor: fmt.Sprintf("anchor %d", index),
			StartSec:     startSec, EndSec: startSec + 8, DurationSec: 8,
			KeyframeReference: fmt.Sprintf("gs://bucket/jobs/orig/images/keyframe_%d.png", index),
		}
	}
	recipe := &video.Recipe{
		ProjectTitle: "test",
		MusicRecipe: domain.MusicRecipe{
			Sections: []domain.MusicSection{
				{Name: "Verse", StartSeconds: 0, EndSeconds: 16},
				{Name: "Chorus", StartSeconds: 16, EndSeconds: 24},
			},
		},
		Cuts: []video.Cut{cut(1, 1, 0), cut(2, 1, 8), cut(3, 2, 16)},
	}
	return &Context{
		Task: task, VideoRecipe: recipe, OutputPath: "gs://bucket/jobs/regen-1/",
		Workflows: &orchestrator.Workflows{CutKeyframe: runner},
	}
}

// fakePublishRunner records where it was asked to save, standing in for
// orchestrator.VideoPublishRunner in tests that exercise the OverwriteKeyframe path.
type fakePublishRunner struct {
	paths []string
}

func (p *fakePublishRunner) Run(_ context.Context, _ *video.Recipe, outputPath string) (*video.PublishResult, error) {
	p.paths = append(p.paths, outputPath)
	return &video.PublishResult{}, nil
}

func (p *fakePublishRunner) BuildMetadata(_ *video.Recipe) ([]byte, error) {
	return nil, nil
}

// 上書きは**元ジョブの**ディレクトリへ書きます。sc.OutputPath は再生成用に採番した新しい
// ジョブを指しているので、そちらへ書くと元ジョブのレシピは古いキーフレームを指したままに
// なり、画面には何も変化が出ません（生成そのものは成功して見えます）。書き先は
// Task.RecipeURL から導きます。
func TestRegenerateCutKeyframeStepOverwritesTheOriginalJob(t *testing.T) {
	runner := &fakeCutKeyframeRunner{resultKeyframeRef: "gs://bucket/jobs/regen-1/regens/cut-1/images/keyframe_1.png"}
	task := &domain.Task{
		Command:           domain.CommandRegenerateCutKeyframe,
		OverwriteKeyframe: true,
		OriginalJobID:     "original-job-1",
		RecipeURL:         "gs://bucket/jobs/original-job-1/video_music_meta.json",
	}
	sc := newRegenTestContext(task, runner)
	publish := &fakePublishRunner{}
	sc.Workflows.Publish = publish

	if err := (RegenerateCutKeyframeStep{}).Execute(context.Background(), sc); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	want := "gs://bucket/jobs/original-job-1/"
	if len(publish.paths) != 1 || publish.paths[0] != want {
		t.Fatalf("published to %v, want [%s]", publish.paths, want)
	}
}

// 上書きを指定しなければ元ジョブには一切書きません。再生成した画像を見てから
// 採用するかを決められるのがこの操作の要点です。
func TestRegenerateCutKeyframeStepKeepsTheOriginalJobWithoutOverwrite(t *testing.T) {
	runner := &fakeCutKeyframeRunner{resultKeyframeRef: "gs://bucket/jobs/regen-1/regens/cut-1/images/keyframe_1.png"}
	task := &domain.Task{
		Command:           domain.CommandRegenerateCutKeyframe,
		OverwriteKeyframe: false,
		OriginalJobID:     "original-job-1",
		RecipeURL:         "gs://bucket/jobs/original-job-1/video_music_meta.json",
	}
	sc := newRegenTestContext(task, runner)
	publish := &fakePublishRunner{}
	sc.Workflows.Publish = publish

	if err := (RegenerateCutKeyframeStep{}).Execute(context.Background(), sc); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(publish.paths) != 0 {
		t.Fatalf("published to %v, want nothing", publish.paths)
	}
}

func TestRegenerateCutKeyframeStepUsesFullRegenerateByDefault(t *testing.T) {
	runner := &fakeCutKeyframeRunner{resultKeyframeRef: "gs://bucket/jobs/regen-1/regens/cut-1/images/keyframe_1.png"}
	task := &domain.Task{Command: domain.CommandRegenerateCutKeyframe, OverwriteKeyframe: true}
	sc := newRegenTestContext(task, runner)

	if err := (RegenerateCutKeyframeStep{}).Execute(context.Background(), sc); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !runner.runAndSaveCalled {
		t.Error("expected RunAndSave to be called")
	}
	if runner.editAndSaveCalled {
		t.Error("did not expect EditAndSave to be called")
	}
	if sc.VideoRecipe.Cuts[0].KeyframeReference != runner.resultKeyframeRef {
		t.Errorf("KeyframeReference = %q, want %q", sc.VideoRecipe.Cuts[0].KeyframeReference, runner.resultKeyframeRef)
	}
}

// TestRegenerateCutKeyframeStepUsesEditModeWhenEditPromptSet verifies that an EditPrompt
// routes through EditAndSave (preserving the existing keyframe as the edit source) instead of
// clearing it and doing a full regenerate.
func TestRegenerateCutKeyframeStepUsesEditModeWhenEditPromptSet(t *testing.T) {
	runner := &fakeCutKeyframeRunner{resultKeyframeRef: "gs://bucket/jobs/regen-1/regens/cut-1/images/keyframe_1.png"}
	task := &domain.Task{
		Command:              domain.CommandRegenerateCutKeyframe,
		OverwriteKeyframe:    true,
		EditPrompt:           "腕には絆創膏を1〜2枚のみ",
		VisualAnchorOverride: "this should be ignored in edit mode",
	}
	sc := newRegenTestContext(task, runner)

	if err := (RegenerateCutKeyframeStep{}).Execute(context.Background(), sc); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !runner.editAndSaveCalled {
		t.Fatal("expected EditAndSave to be called")
	}
	if runner.runAndSaveCalled {
		t.Error("did not expect RunAndSave to be called")
	}
	if runner.editPromptSeen != "腕には絆創膏を1〜2枚のみ" {
		t.Errorf("edit prompt seen = %q", runner.editPromptSeen)
	}
	if runner.keyframeSeenAtEdit != "gs://bucket/jobs/orig/images/keyframe_1.png" {
		t.Errorf("editor should have received the existing keyframe as source, got %q", runner.keyframeSeenAtEdit)
	}
	if sc.VideoRecipe.Cuts[0].VisualAnchor != "original anchor" {
		t.Errorf("VisualAnchor changed in edit mode: got %q, want unchanged", sc.VideoRecipe.Cuts[0].VisualAnchor)
	}
	if sc.VideoRecipe.Cuts[0].KeyframeReference != runner.resultKeyframeRef {
		t.Errorf("KeyframeReference = %q, want %q", sc.VideoRecipe.Cuts[0].KeyframeReference, runner.resultKeyframeRef)
	}
}

// TestRegenerateCutKeyframeStepRegeneratesWholeSection verifies that a section-targeted task
// regenerates every cut of that section — and only that section — in a single batched RunAndSave,
// so the cuts are produced together instead of drifting apart across separate calls.
func TestRegenerateCutKeyframeStepRegeneratesWholeSection(t *testing.T) {
	runner := &fakeCutKeyframeRunner{resultKeyframeRef: "gs://bucket/jobs/regen-1/regens/section-0/images/keyframe_1.png"}
	sectionIndex := 0
	task := &domain.Task{
		Command:           domain.CommandRegenerateSectionKeyframes,
		SectionIndex:      &sectionIndex,
		OverwriteKeyframe: true,
	}
	sc := newRegenSectionTestContext(task, runner)

	if err := (RegenerateCutKeyframeStep{}).Execute(context.Background(), sc); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(runner.runAndSaveCuts) != 1 {
		t.Fatalf("RunAndSave calls = %d, want 1 batched call, got %v", len(runner.runAndSaveCuts), runner.runAndSaveCuts)
	}
	if got, want := runner.runAndSaveCuts[0], []int{1, 2}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("regenerated cut indexes = %v, want %v", got, want)
	}
	for _, i := range []int{0, 1} {
		if sc.VideoRecipe.Cuts[i].KeyframeReference != runner.resultKeyframeRef {
			t.Errorf("cut %d KeyframeReference = %q, want %q", i+1, sc.VideoRecipe.Cuts[i].KeyframeReference, runner.resultKeyframeRef)
		}
	}
	// 別セクションのカットは対象外なので元のキーフレームのまま。
	if got, want := sc.VideoRecipe.Cuts[2].KeyframeReference, "gs://bucket/jobs/orig/images/keyframe_3.png"; got != want {
		t.Errorf("cut 3 KeyframeReference = %q, want unchanged %q", got, want)
	}
}

// TestRegenerateCutKeyframeStepEditsSectionCutsIntoSeparatePaths verifies that section edit mode
// calls EditAndSave once per cut (the editor only accepts single-cut recipes) and writes each one
// to its own output path, so the per-call keyframe_1.png destinations don't overwrite each other.
func TestRegenerateCutKeyframeStepEditsSectionCutsIntoSeparatePaths(t *testing.T) {
	runner := &fakeCutKeyframeRunner{resultKeyframeRef: "gs://bucket/jobs/regen-1/regens/section-0/cut-1/images/keyframe_1.png"}
	sectionIndex := 0
	task := &domain.Task{
		Command:           domain.CommandRegenerateSectionKeyframes,
		SectionIndex:      &sectionIndex,
		OverwriteKeyframe: true,
		EditPrompt:        "もっと明るい照明に",
	}
	sc := newRegenSectionTestContext(task, runner)

	if err := (RegenerateCutKeyframeStep{}).Execute(context.Background(), sc); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if runner.runAndSaveCalled {
		t.Error("did not expect RunAndSave to be called in edit mode")
	}
	want := []string{
		"gs://bucket/jobs/regen-1/regens/section-0/cut-1/",
		"gs://bucket/jobs/regen-1/regens/section-0/cut-2/",
	}
	if len(runner.editPaths) != len(want) {
		t.Fatalf("EditAndSave paths = %v, want %v", runner.editPaths, want)
	}
	for i, path := range want {
		if runner.editPaths[i] != path {
			t.Errorf("EditAndSave path[%d] = %q, want %q", i, runner.editPaths[i], path)
		}
	}
}

// TestRegenerateCutKeyframeStepRejectsUntargetedTask verifies the step refuses a task that
// names neither a cut nor a section, rather than silently regenerating nothing.
func TestRegenerateCutKeyframeStepRejectsUntargetedTask(t *testing.T) {
	runner := &fakeCutKeyframeRunner{}
	task := &domain.Task{Command: domain.CommandRegenerateSectionKeyframes}
	sc := newRegenSectionTestContext(task, runner)

	if err := (RegenerateCutKeyframeStep{}).Execute(context.Background(), sc); err == nil {
		t.Fatal("Execute() error = nil, want an error for a task with no cut_index or section_index")
	}
}
