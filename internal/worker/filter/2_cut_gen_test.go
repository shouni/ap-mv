package filter

import (
	"context"
	"testing"

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
		Task:        task,
		VideoRecipe: recipe,
		Workflows:   &orchestrator.Workflows{CutKeyframe: &fakeCutKeyframeRunner{resultKeyframeRef: "gs://bucket/keyframe.png"}},
		OutputPath:  "gs://bucket/jobs/job-1/",
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
		Task:        task,
		VideoRecipe: recipe,
		Workflows:   &orchestrator.Workflows{CutKeyframe: &fakeCutKeyframeRunner{resultKeyframeRef: "gs://bucket/keyframe.png"}},
		OutputPath:  "gs://bucket/jobs/job-1/",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if recipe.AspectRatio != keyframe.CutAspectRatio {
		t.Errorf("AspectRatio = %q, want default %q", recipe.AspectRatio, keyframe.CutAspectRatio)
	}
}
