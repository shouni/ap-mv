package filter

import (
	"context"
	"testing"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	"github.com/shouni/go-veo-orchestrator/keyframe"
	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"github.com/shouni/ap-mv/internal/domain"
)

// TestCutKeyframeFilterStampsTaskAspectRatio verifies that when a task specifies an aspect
// ratio, it is recorded on the resulting VideoRecipe so later video-generation steps can read a
// single source of truth instead of choosing independently.
func TestCutKeyframeFilterStampsTaskAspectRatio(t *testing.T) {
	recipe := &orchestrator.VideoRecipe{
		MusicRecipe: orchestrator.MusicRecipe{Title: "test"},
		Cuts:        []orchestrator.Cut{{CutIndex: 1, VisualAnchor: "a"}},
	}
	task := &domain.Task{JobID: "job-1", Command: domain.CommandVideoRecipeCreate, VeoAspectRatio: "9:16"}
	flt := CutKeyframeFilter{}

	err := flt.Execute(context.Background(), &Context{
		State: State{
			Task:        task,
			VideoRecipe: recipe,
			OutputPath:  "gs://bucket/jobs/job-1/",
		},
		Services: Services{
			Workflows: &orchestrator.Workflows{CutKeyframe: &fakeCutKeyframeRunner{resultKeyframeRef: "gs://bucket/keyframe.png"}},
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if recipe.AspectRatio != "9:16" {
		t.Errorf("AspectRatio = %q, want %q", recipe.AspectRatio, "9:16")
	}
}

// TestCutKeyframeFilterDefaultsAspectRatioWhenTaskHasNone verifies that, absent a task-level
// aspect ratio (e.g. an old task predating this field), the recorded value falls back to
// keyframe.CutAspectRatio — the same default the Generator itself applies — so the recipe
// always ends up with an explicit, correct value.
func TestCutKeyframeFilterDefaultsAspectRatioWhenTaskHasNone(t *testing.T) {
	recipe := &orchestrator.VideoRecipe{
		MusicRecipe: orchestrator.MusicRecipe{Title: "test"},
		Cuts:        []orchestrator.Cut{{CutIndex: 1, VisualAnchor: "a"}},
	}
	task := &domain.Task{JobID: "job-1", Command: domain.CommandVideoRecipeCreate}
	flt := CutKeyframeFilter{}

	err := flt.Execute(context.Background(), &Context{
		State: State{
			Task:        task,
			VideoRecipe: recipe,
			OutputPath:  "gs://bucket/jobs/job-1/",
		},
		Services: Services{
			Workflows: &orchestrator.Workflows{CutKeyframe: &fakeCutKeyframeRunner{resultKeyframeRef: "gs://bucket/keyframe.png"}},
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if recipe.AspectRatio != keyframe.CutAspectRatio {
		t.Errorf("AspectRatio = %q, want default %q", recipe.AspectRatio, keyframe.CutAspectRatio)
	}
}

// aspectRatioCapturingRunner records the recipe's AspectRatio at the moment RunAndSave is
// invoked (not after it returns). This matters because the real CutKeyframeRunner.RunAndSave
// (go-veo-orchestrator/runner/keyframe.go) writes video_music_meta.json to GCS *inside itself*
// before returning — so AspectRatio must already be set on the recipe passed *into* RunAndSave,
// not stamped on afterward, or the persisted file silently ends up without it (the actual bug
// this test guards against).
type aspectRatioCapturingRunner struct {
	seenAspectRatio string
}

func (r *aspectRatioCapturingRunner) Run(context.Context, *orchestrator.VideoRecipe) ([]*imagePorts.ImageResponse, error) {
	return nil, nil
}

func (r *aspectRatioCapturingRunner) RunAndSave(_ context.Context, recipe *orchestrator.VideoRecipe, _ string) (*orchestrator.VideoRecipe, error) {
	r.seenAspectRatio = recipe.AspectRatio
	return recipe, nil
}

func (r *aspectRatioCapturingRunner) EditAndSave(_ context.Context, recipe *orchestrator.VideoRecipe, _ string, _ string) (*orchestrator.VideoRecipe, error) {
	return recipe, nil
}

// TestCutKeyframeFilterSetsAspectRatioBeforeRunAndSave is a regression test for a real bug:
// RunAndSave persists video_music_meta.json to GCS internally, so AspectRatio must be resolved
// on fc.VideoRecipe before RunAndSave is called, not stamped onto its return value afterward.
func TestCutKeyframeFilterSetsAspectRatioBeforeRunAndSave(t *testing.T) {
	recipe := &orchestrator.VideoRecipe{
		MusicRecipe: orchestrator.MusicRecipe{Title: "test"},
		Cuts:        []orchestrator.Cut{{CutIndex: 1, VisualAnchor: "a"}},
	}
	task := &domain.Task{JobID: "job-1", Command: domain.CommandVideoRecipeCreate, VeoAspectRatio: "9:16"}
	runner := &aspectRatioCapturingRunner{}
	flt := CutKeyframeFilter{}

	err := flt.Execute(context.Background(), &Context{
		State: State{
			Task:        task,
			VideoRecipe: recipe,
			OutputPath:  "gs://bucket/jobs/job-1/",
		},
		Services: Services{
			Workflows: &orchestrator.Workflows{CutKeyframe: runner},
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if runner.seenAspectRatio != "9:16" {
		t.Errorf("AspectRatio seen by RunAndSave = %q, want %q (must be set before the call)", runner.seenAspectRatio, "9:16")
	}
}

// recordingPublishRunner records whether the metadata write happened, so the keyframe-reuse path
// can be checked for the side effect RunAndSave used to provide. The shared fakePublishRunner
// (regen_cut_keyframe_test.go) records nothing, which is all its own tests need.
type recordingPublishRunner struct {
	runCalled bool
}

func (f *recordingPublishRunner) Run(_ context.Context, _ *orchestrator.VideoRecipe, _ string) (*orchestrator.PublishResult, error) {
	f.runCalled = true
	return &orchestrator.PublishResult{}, nil
}

func (f *recordingPublishRunner) BuildMetadata(*orchestrator.VideoRecipe) ([]byte, error) {
	return nil, nil
}

// TestCutKeyframeFilterReusesExistingKeyframes pins the cost fix: generating a video from a job
// whose keyframes are already baked must not re-bake them. CutKeyframeRunner.RunAndSave has no
// per-cut skip, so the filter has to decide.
func TestCutKeyframeFilterReusesExistingKeyframes(t *testing.T) {
	recipe := &orchestrator.VideoRecipe{
		MusicRecipe: orchestrator.MusicRecipe{Title: "test"},
		AspectRatio: "16:9",
		Cuts: []orchestrator.Cut{
			{CutIndex: 1, VisualAnchor: "a", KeyframeResult: orchestrator.KeyframeResult{KeyframeReference: "gs://bucket/jobs/job-1/images/keyframe_001.png"}},
			{CutIndex: 2, VisualAnchor: "b", KeyframeResult: orchestrator.KeyframeResult{KeyframeReference: "gs://bucket/jobs/job-1/images/keyframe_002.png"}},
		},
	}
	runner := &fakeCutKeyframeRunner{resultKeyframeRef: "gs://bucket/regenerated.png"}
	publisher := &recordingPublishRunner{}

	err := CutKeyframeFilter{}.Execute(context.Background(), &Context{
		State: State{
			Task:        &domain.Task{JobID: "mv-1", Command: domain.CommandMVFromKeyframeVideoRecipe},
			VideoRecipe: recipe,
			OutputPath:  "gs://bucket/jobs/mv-1/",
		},
		Services: Services{Workflows: &orchestrator.Workflows{CutKeyframe: runner, Publish: publisher}},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if runner.runAndSaveCalled {
		t.Error("RunAndSave was called; want existing keyframes reused without regeneration")
	}
	if recipe.Cuts[0].KeyframeReference != "gs://bucket/jobs/job-1/images/keyframe_001.png" {
		t.Errorf("cut[0] keyframe = %q, want the existing reference", recipe.Cuts[0].KeyframeReference)
	}
	// 履歴一覧は video_music_meta.json を目印にジョブを拾うため、RunAndSave を飛ばしても
	// metadata は書かれていなければならない。
	if !publisher.runCalled {
		t.Error("metadata was not written on the reuse path; the job would disappear from history while generating")
	}
}

// TestCutKeyframeFilterRegeneratesWhenAnyKeyframeMissing pins the conservative half: a partially
// baked recipe (e.g. a cut that scene splitting re-divided) goes through normal generation, so
// the scene beats within one recipe stay consistent.
func TestCutKeyframeFilterRegeneratesWhenAnyKeyframeMissing(t *testing.T) {
	recipe := &orchestrator.VideoRecipe{
		MusicRecipe: orchestrator.MusicRecipe{Title: "test"},
		AspectRatio: "16:9",
		Cuts: []orchestrator.Cut{
			{CutIndex: 1, VisualAnchor: "a", KeyframeResult: orchestrator.KeyframeResult{KeyframeReference: "gs://bucket/jobs/job-1/images/keyframe_001.png"}},
			{CutIndex: 2, VisualAnchor: "b"},
		},
	}
	runner := &fakeCutKeyframeRunner{resultKeyframeRef: "gs://bucket/regenerated.png"}

	err := CutKeyframeFilter{}.Execute(context.Background(), &Context{
		State: State{
			Task:        &domain.Task{JobID: "mv-1", Command: domain.CommandMVFromKeyframeVideoRecipe},
			VideoRecipe: recipe,
			OutputPath:  "gs://bucket/jobs/mv-1/",
		},
		Services: Services{Workflows: &orchestrator.Workflows{CutKeyframe: runner}},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !runner.runAndSaveCalled {
		t.Error("RunAndSave was not called; a partially baked recipe must go through generation")
	}
}

// TestCutKeyframeFilterRegeneratesRelativeKeyframes pins that legacy recipes storing relative
// keyframe paths are not reused: those resolve against their original job's base, not this job's
// output path, so reusing them would point at the wrong (or missing) object.
func TestCutKeyframeFilterRegeneratesRelativeKeyframes(t *testing.T) {
	recipe := &orchestrator.VideoRecipe{
		MusicRecipe: orchestrator.MusicRecipe{Title: "test"},
		AspectRatio: "16:9",
		Cuts: []orchestrator.Cut{
			{CutIndex: 1, VisualAnchor: "a", KeyframeResult: orchestrator.KeyframeResult{KeyframeReference: "images/keyframe_001.png"}},
		},
	}
	runner := &fakeCutKeyframeRunner{resultKeyframeRef: "gs://bucket/regenerated.png"}

	err := CutKeyframeFilter{}.Execute(context.Background(), &Context{
		State: State{
			Task:        &domain.Task{JobID: "mv-1", Command: domain.CommandMVFromKeyframeVideoRecipe},
			VideoRecipe: recipe,
			OutputPath:  "gs://bucket/jobs/mv-1/",
		},
		Services: Services{Workflows: &orchestrator.Workflows{CutKeyframe: runner}},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !runner.runAndSaveCalled {
		t.Error("RunAndSave was not called for a relative keyframe reference; want regeneration")
	}
}
