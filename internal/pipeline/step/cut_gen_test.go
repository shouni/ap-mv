package step

import (
	"context"
	"testing"

	orchestrator "github.com/shouni/go-veo-orchestrator/ports"
	"github.com/shouni/go-veo-orchestrator/video"

	"github.com/shouni/ap-mv/internal/domain"
)

// TestCutKeyframeStepStampsTaskAspectRatio verifies that when a task specifies an aspect
// ratio, it is recorded on the resulting VideoRecipe so later video-generation steps can read a
// single source of truth instead of choosing independently.
func TestCutKeyframeStepStampsTaskAspectRatio(t *testing.T) {
	recipe := &video.Recipe{
		MusicRecipe: video.MusicRecipe{Title: "test"},
		Cuts:        []video.Cut{{CutIndex: 1, VisualAnchor: "a"}},
	}
	task := &domain.Task{JobID: "job-1", Command: domain.CommandVideoRecipeCreate, VeoAspectRatio: "9:16"}
	st := CutKeyframeStep{}

	err := st.Execute(context.Background(), &Context{
		Task:        task,
		VideoRecipe: recipe,
		OutputPath:  "gs://bucket/jobs/job-1/",
		Workflows:   &orchestrator.Workflows{CutKeyframe: &fakeCutKeyframeRunner{resultKeyframeRef: "gs://bucket/keyframe.png"}},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if recipe.AspectRatio != "9:16" {
		t.Errorf("AspectRatio = %q, want %q", recipe.AspectRatio, "9:16")
	}
}

// TestCutKeyframeStepDefaultsAspectRatioWhenTaskHasNone verifies that, absent a task-level
// aspect ratio (e.g. an old task predating this field), the recorded value falls back to
// domain.DefaultAspectRatio — the app owns the fallback now — so the recipe
// always ends up with an explicit, correct value.
func TestCutKeyframeStepDefaultsAspectRatioWhenTaskHasNone(t *testing.T) {
	recipe := &video.Recipe{
		MusicRecipe: video.MusicRecipe{Title: "test"},
		Cuts:        []video.Cut{{CutIndex: 1, VisualAnchor: "a"}},
	}
	task := &domain.Task{JobID: "job-1", Command: domain.CommandVideoRecipeCreate}
	st := CutKeyframeStep{}

	err := st.Execute(context.Background(), &Context{
		Task:        task,
		VideoRecipe: recipe,
		OutputPath:  "gs://bucket/jobs/job-1/",
		Workflows:   &orchestrator.Workflows{CutKeyframe: &fakeCutKeyframeRunner{resultKeyframeRef: "gs://bucket/keyframe.png"}},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if recipe.AspectRatio != domain.DefaultAspectRatio {
		t.Errorf("AspectRatio = %q, want default %q", recipe.AspectRatio, domain.DefaultAspectRatio)
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

func (r *aspectRatioCapturingRunner) GenerateAndSave(_ context.Context, recipe *video.Recipe, _ string) (*video.Recipe, error) {
	r.seenAspectRatio = recipe.AspectRatio
	return recipe, nil
}

func (r *aspectRatioCapturingRunner) EditAndSave(_ context.Context, recipe *video.Recipe, _ int, _ string, _ string) (*video.Recipe, error) {
	return recipe, nil
}

// TestCutKeyframeStepSetsAspectRatioBeforeRunAndSave is a regression test for a real bug:
// RunAndSave persists video_music_meta.json to GCS internally, so AspectRatio must be resolved
// on sc.VideoRecipe before RunAndSave is called, not stamped onto its return value afterward.
func TestCutKeyframeStepSetsAspectRatioBeforeRunAndSave(t *testing.T) {
	recipe := &video.Recipe{
		MusicRecipe: video.MusicRecipe{Title: "test"},
		Cuts:        []video.Cut{{CutIndex: 1, VisualAnchor: "a"}},
	}
	task := &domain.Task{JobID: "job-1", Command: domain.CommandVideoRecipeCreate, VeoAspectRatio: "9:16"}
	runner := &aspectRatioCapturingRunner{}
	st := CutKeyframeStep{}

	err := st.Execute(context.Background(), &Context{
		Task:        task,
		VideoRecipe: recipe,
		OutputPath:  "gs://bucket/jobs/job-1/",
		Workflows:   &orchestrator.Workflows{CutKeyframe: runner},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if runner.seenAspectRatio != "9:16" {
		t.Errorf("AspectRatio seen by RunAndSave = %q, want %q (must be set before the call)", runner.seenAspectRatio, "9:16")
	}
}

// TestCutKeyframeStepPassesExistingKeyframesThrough pins ap-mv's half of the keyframe-reuse
// behaviour. Deciding not to re-bake a cut is CutKeyframeRunner's job (it skips cuts whose
// KeyframeReference is set), so what has to hold here is that the step hands those references
// to the runner untouched — if it cleared or rewrote them, the runner would re-bake everything.
func TestCutKeyframeStepPassesExistingKeyframesThrough(t *testing.T) {
	recipe := &video.Recipe{
		MusicRecipe: video.MusicRecipe{Title: "test"},
		AspectRatio: "16:9",
		Cuts: []video.Cut{
			{CutIndex: 1, VisualAnchor: "a", KeyframeReference: "gs://bucket/jobs/job-1/images/keyframe_001.png"},
			{CutIndex: 2, VisualAnchor: "b"},
		},
	}
	runner := &fakeCutKeyframeRunner{resultKeyframeRef: "gs://bucket/jobs/mv-1/images/keyframe.png"}

	err := CutKeyframeStep{}.Execute(context.Background(), &Context{
		Task:        &domain.Task{JobID: "mv-1", Command: domain.CommandMVFromKeyframeVideoRecipe},
		VideoRecipe: recipe,
		OutputPath:  "gs://bucket/jobs/mv-1/",
		Workflows:   &orchestrator.Workflows{CutKeyframe: runner},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	want := []string{"gs://bucket/jobs/job-1/images/keyframe_001.png", ""}
	if len(runner.keyframeRefsAtRunAndSave) != len(want) {
		t.Fatalf("references seen = %v, want %v", runner.keyframeRefsAtRunAndSave, want)
	}
	for i, ref := range want {
		if runner.keyframeRefsAtRunAndSave[i] != ref {
			t.Errorf("cut[%d] reference reaching RunAndSave = %q, want %q", i, runner.keyframeRefsAtRunAndSave[i], ref)
		}
	}
}
