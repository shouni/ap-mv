package filter

import (
	"context"
	"fmt"
	"testing"

	orchestrator "github.com/shouni/go-veo-orchestrator/ports"
	"github.com/shouni/go-veo-orchestrator/video"

	"github.com/shouni/ap-mv/internal/domain"
	"github.com/shouni/ap-mv/internal/ports"
)

// fakeCutKeyframeRunner records which of RunAndSave/EditAndSave was called, so tests can
// assert the filter picked the right mode (full regenerate vs. local edit).
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
				CutIndex:       *task.CutIndex,
				VisualAnchor:   "original anchor",
				KeyframeResult: video.KeyframeResult{KeyframeReference: "gs://bucket/jobs/orig/images/keyframe_1.png"},
			},
		},
	}
	return &Context{
		State:    State{Task: task, VideoRecipe: recipe, OutputPath: "gs://bucket/jobs/regen-1/"},
		Services: Services{Workflows: &orchestrator.Workflows{CutKeyframe: runner}},
	}
}

// newRegenSectionTestContext builds a 3-cut recipe spanning two sections (cuts 1-2 in section 1,
// cut 3 in section 2) for exercising the section-targeted regeneration path.
func newRegenSectionTestContext(task *domain.Task, runner *fakeCutKeyframeRunner) *Context {
	cut := func(index, sectionIndex int, startSec float64) video.Cut {
		return video.Cut{
			CutIndex:       index,
			SectionIndex:   sectionIndex,
			VisualAnchor:   fmt.Sprintf("anchor %d", index),
			AudioSync:      video.AudioSync{StartSec: startSec, EndSec: startSec + 8, DurationSec: 8},
			KeyframeResult: video.KeyframeResult{KeyframeReference: fmt.Sprintf("gs://bucket/jobs/orig/images/keyframe_%d.png", index)},
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
		State:    State{Task: task, VideoRecipe: recipe, OutputPath: "gs://bucket/jobs/regen-1/"},
		Services: Services{Workflows: &orchestrator.Workflows{CutKeyframe: runner}},
	}
}

// fakePublishRunner records the recipe it was asked to save, standing in for
// orchestrator.VideoPublishRunner in tests that exercise the OverwriteKeyframe path.
type fakePublishRunner struct{}

func (fakePublishRunner) Run(_ context.Context, _ *video.Recipe, _ string) (*video.PublishResult, error) {
	return &video.PublishResult{}, nil
}

func (fakePublishRunner) BuildMetadata(_ *video.Recipe) ([]byte, error) {
	return nil, nil
}

// fakeInvalidatingHistoryRepository records InvalidateJob calls. The other ports.HistoryRepository
// methods are stubbed out since this filter only ever calls InvalidateJob.
type fakeInvalidatingHistoryRepository struct {
	invalidatedJobIDs []string
}

func (f *fakeInvalidatingHistoryRepository) ListHistoryPage(context.Context, int, int) (domain.VideoHistoryPage, error) {
	return domain.VideoHistoryPage{}, nil
}

func (f *fakeInvalidatingHistoryRepository) GetHistory(context.Context, string) (domain.VideoHistoryDetail, error) {
	return domain.VideoHistoryDetail{}, nil
}

func (f *fakeInvalidatingHistoryRepository) DeleteHistory(context.Context, string) error {
	return nil
}

func (f *fakeInvalidatingHistoryRepository) GetVeoUsage(context.Context, string) (*domain.VeoUsage, error) {
	return nil, nil
}

func (f *fakeInvalidatingHistoryRepository) DownloadKeyframes(context.Context, string, ports.KeyframeSink) error {
	return nil
}

func (f *fakeInvalidatingHistoryRepository) KeyframeZipSignedURL(context.Context, string) (string, error) {
	return "", nil
}

func (f *fakeInvalidatingHistoryRepository) InvalidateJob(jobID string) {
	f.invalidatedJobIDs = append(f.invalidatedJobIDs, jobID)
}

// TestRegenerateCutKeyframeFilterUsesFullRegenerateByDefault verifies that without an
// EditPrompt, the filter clears the existing keyframe and calls RunAndSave (full regenerate).
func TestRegenerateCutKeyframeFilterUsesFullRegenerateByDefault(t *testing.T) {
	runner := &fakeCutKeyframeRunner{resultKeyframeRef: "gs://bucket/jobs/regen-1/regens/cut-1/images/keyframe_1.png"}
	task := &domain.Task{Command: domain.CommandRegenerateCutKeyframe, OverwriteKeyframe: true}
	fc := newRegenTestContext(task, runner)

	if err := (RegenerateCutKeyframeFilter{}).Execute(context.Background(), fc); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !runner.runAndSaveCalled {
		t.Error("expected RunAndSave to be called")
	}
	if runner.editAndSaveCalled {
		t.Error("did not expect EditAndSave to be called")
	}
	if fc.VideoRecipe.Cuts[0].KeyframeReference != runner.resultKeyframeRef {
		t.Errorf("KeyframeReference = %q, want %q", fc.VideoRecipe.Cuts[0].KeyframeReference, runner.resultKeyframeRef)
	}
}

// TestRegenerateCutKeyframeFilterUsesEditModeWhenEditPromptSet verifies that an EditPrompt
// routes through EditAndSave (preserving the existing keyframe as the edit source) instead of
// clearing it and doing a full regenerate.
func TestRegenerateCutKeyframeFilterUsesEditModeWhenEditPromptSet(t *testing.T) {
	runner := &fakeCutKeyframeRunner{resultKeyframeRef: "gs://bucket/jobs/regen-1/regens/cut-1/images/keyframe_1.png"}
	task := &domain.Task{
		Command:              domain.CommandRegenerateCutKeyframe,
		OverwriteKeyframe:    true,
		EditPrompt:           "腕には絆創膏を1〜2枚のみ",
		VisualAnchorOverride: "this should be ignored in edit mode",
	}
	fc := newRegenTestContext(task, runner)

	if err := (RegenerateCutKeyframeFilter{}).Execute(context.Background(), fc); err != nil {
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
	if fc.VideoRecipe.Cuts[0].VisualAnchor != "original anchor" {
		t.Errorf("VisualAnchor changed in edit mode: got %q, want unchanged", fc.VideoRecipe.Cuts[0].VisualAnchor)
	}
	if fc.VideoRecipe.Cuts[0].KeyframeReference != runner.resultKeyframeRef {
		t.Errorf("KeyframeReference = %q, want %q", fc.VideoRecipe.Cuts[0].KeyframeReference, runner.resultKeyframeRef)
	}
}

// TestRegenerateCutKeyframeFilterRegeneratesWholeSection verifies that a section-targeted task
// regenerates every cut of that section — and only that section — in a single batched RunAndSave,
// so the cuts are produced together instead of drifting apart across separate calls.
func TestRegenerateCutKeyframeFilterRegeneratesWholeSection(t *testing.T) {
	runner := &fakeCutKeyframeRunner{resultKeyframeRef: "gs://bucket/jobs/regen-1/regens/section-0/images/keyframe_1.png"}
	sectionIndex := 0
	task := &domain.Task{
		Command:           domain.CommandRegenerateSectionKeyframes,
		SectionIndex:      &sectionIndex,
		OverwriteKeyframe: true,
	}
	fc := newRegenSectionTestContext(task, runner)

	if err := (RegenerateCutKeyframeFilter{}).Execute(context.Background(), fc); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(runner.runAndSaveCuts) != 1 {
		t.Fatalf("RunAndSave calls = %d, want 1 batched call, got %v", len(runner.runAndSaveCuts), runner.runAndSaveCuts)
	}
	if got, want := runner.runAndSaveCuts[0], []int{1, 2}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("regenerated cut indexes = %v, want %v", got, want)
	}
	for _, i := range []int{0, 1} {
		if fc.VideoRecipe.Cuts[i].KeyframeReference != runner.resultKeyframeRef {
			t.Errorf("cut %d KeyframeReference = %q, want %q", i+1, fc.VideoRecipe.Cuts[i].KeyframeReference, runner.resultKeyframeRef)
		}
	}
	// 別セクションのカットは対象外なので元のキーフレームのまま。
	if got, want := fc.VideoRecipe.Cuts[2].KeyframeReference, "gs://bucket/jobs/orig/images/keyframe_3.png"; got != want {
		t.Errorf("cut 3 KeyframeReference = %q, want unchanged %q", got, want)
	}
}

// TestRegenerateCutKeyframeFilterEditsSectionCutsIntoSeparatePaths verifies that section edit mode
// calls EditAndSave once per cut (the editor only accepts single-cut recipes) and writes each one
// to its own output path, so the per-call keyframe_1.png destinations don't overwrite each other.
func TestRegenerateCutKeyframeFilterEditsSectionCutsIntoSeparatePaths(t *testing.T) {
	runner := &fakeCutKeyframeRunner{resultKeyframeRef: "gs://bucket/jobs/regen-1/regens/section-0/cut-1/images/keyframe_1.png"}
	sectionIndex := 0
	task := &domain.Task{
		Command:           domain.CommandRegenerateSectionKeyframes,
		SectionIndex:      &sectionIndex,
		OverwriteKeyframe: true,
		EditPrompt:        "もっと明るい照明に",
	}
	fc := newRegenSectionTestContext(task, runner)

	if err := (RegenerateCutKeyframeFilter{}).Execute(context.Background(), fc); err != nil {
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

// TestRegenerateCutKeyframeFilterRejectsUntargetedTask verifies the filter refuses a task that
// names neither a cut nor a section, rather than silently regenerating nothing.
func TestRegenerateCutKeyframeFilterRejectsUntargetedTask(t *testing.T) {
	runner := &fakeCutKeyframeRunner{}
	task := &domain.Task{Command: domain.CommandRegenerateSectionKeyframes}
	fc := newRegenSectionTestContext(task, runner)

	if err := (RegenerateCutKeyframeFilter{}).Execute(context.Background(), fc); err == nil {
		t.Fatal("Execute() error = nil, want an error for a task with no cut_index or section_index")
	}
}

// TestRegenerateCutKeyframeFilterInvalidatesOriginalJobCacheOnOverwrite verifies that, once the
// updated recipe is published back to the original job, the filter invalidates that job's cached
// history/recipe metadata so History Detail doesn't keep serving a stale pre-edit copy for the
// remainder of the cache TTL.
func TestRegenerateCutKeyframeFilterInvalidatesOriginalJobCacheOnOverwrite(t *testing.T) {
	runner := &fakeCutKeyframeRunner{resultKeyframeRef: "gs://bucket/jobs/regen-1/regens/cut-1/images/keyframe_1.png"}
	task := &domain.Task{
		Command:           domain.CommandRegenerateCutKeyframe,
		OverwriteKeyframe: true,
		OriginalJobID:     "original-job-1",
		RecipeURL:         "gs://bucket/jobs/original-job-1/video_music_meta.json",
	}
	fc := newRegenTestContext(task, runner)
	fc.Workflows.Publish = fakePublishRunner{}
	historyRepo := &fakeInvalidatingHistoryRepository{}
	fc.HistoryRepository = historyRepo

	if err := (RegenerateCutKeyframeFilter{}).Execute(context.Background(), fc); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(historyRepo.invalidatedJobIDs) != 1 || historyRepo.invalidatedJobIDs[0] != "original-job-1" {
		t.Fatalf("invalidated job IDs = %v, want [original-job-1]", historyRepo.invalidatedJobIDs)
	}
}

// TestRegenerateCutKeyframeFilterSkipsInvalidationWithoutOverwrite verifies that when
// OverwriteKeyframe is false (nothing gets published back to the original job), the filter does
// not invalidate any cache, since there is nothing stale to fix.
func TestRegenerateCutKeyframeFilterSkipsInvalidationWithoutOverwrite(t *testing.T) {
	runner := &fakeCutKeyframeRunner{resultKeyframeRef: "gs://bucket/jobs/regen-1/regens/cut-1/images/keyframe_1.png"}
	task := &domain.Task{
		Command:           domain.CommandRegenerateCutKeyframe,
		OverwriteKeyframe: false,
		OriginalJobID:     "original-job-1",
	}
	fc := newRegenTestContext(task, runner)
	fc.Workflows.Publish = fakePublishRunner{}
	historyRepo := &fakeInvalidatingHistoryRepository{}
	fc.HistoryRepository = historyRepo

	if err := (RegenerateCutKeyframeFilter{}).Execute(context.Background(), fc); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(historyRepo.invalidatedJobIDs) != 0 {
		t.Fatalf("invalidated job IDs = %v, want none", historyRepo.invalidatedJobIDs)
	}
}
