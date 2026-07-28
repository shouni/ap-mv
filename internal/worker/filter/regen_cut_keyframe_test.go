package filter

import (
	"context"
	"testing"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

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
}

func (f *fakeCutKeyframeRunner) Run(_ context.Context, _ *orchestrator.VideoRecipe) ([]*imagePorts.ImageResponse, error) {
	return nil, nil
}

func (f *fakeCutKeyframeRunner) RunAndSave(_ context.Context, recipe *orchestrator.VideoRecipe, _ string) (*orchestrator.VideoRecipe, error) {
	f.runAndSaveCalled = true
	recipe.Cuts[0].KeyframeReference = f.resultKeyframeRef
	return recipe, nil
}

func (f *fakeCutKeyframeRunner) EditAndSave(_ context.Context, recipe *orchestrator.VideoRecipe, editPrompt string, _ string) (*orchestrator.VideoRecipe, error) {
	f.editAndSaveCalled = true
	f.editPromptSeen = editPrompt
	f.keyframeSeenAtEdit = recipe.Cuts[0].KeyframeReference
	recipe.Cuts[0].KeyframeReference = f.resultKeyframeRef
	return recipe, nil
}

func newRegenTestContext(task *domain.Task, runner *fakeCutKeyframeRunner) *Context {
	cutIndex := 1
	if task.CutIndex == nil {
		task.CutIndex = &cutIndex
	}
	recipe := &orchestrator.VideoRecipe{
		ProjectTitle: "test",
		Cuts: []orchestrator.Cut{
			{
				CutIndex:       *task.CutIndex,
				VisualAnchor:   "original anchor",
				KeyframeResult: orchestrator.KeyframeResult{KeyframeReference: "gs://bucket/jobs/orig/images/keyframe_1.png"},
			},
		},
	}
	return &Context{
		State:    State{Task: task, VideoRecipe: recipe, OutputPath: "gs://bucket/jobs/regen-1/"},
		Services: Services{Workflows: &orchestrator.Workflows{CutKeyframe: runner}},
	}
}

// fakePublishRunner records the recipe it was asked to save, standing in for
// orchestrator.VideoPublishRunner in tests that exercise the OverwriteKeyframe path.
type fakePublishRunner struct{}

func (fakePublishRunner) Run(_ context.Context, _ *orchestrator.VideoRecipe, _ string) (*orchestrator.PublishResult, error) {
	return &orchestrator.PublishResult{}, nil
}

func (fakePublishRunner) BuildMetadata(_ *orchestrator.VideoRecipe) ([]byte, error) {
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
