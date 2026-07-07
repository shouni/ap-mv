package filter

import (
	"context"
	"testing"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"github.com/shouni/ap-mv/internal/domain"
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

func (f *fakeCutKeyframeRunner) Run(_ context.Context, recipe *orchestrator.VideoRecipe) ([]*imagePorts.ImageResponse, error) {
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
			{CutIndex: *task.CutIndex, VisualAnchor: "original anchor", KeyframeReference: "gs://bucket/jobs/orig/images/keyframe_1.png"},
		},
	}
	return &Context{
		Task:        task,
		VideoRecipe: recipe,
		Workflows:   &orchestrator.Workflows{CutKeyframe: runner},
		OutputPath:  "gs://bucket/jobs/regen-1/",
	}
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
