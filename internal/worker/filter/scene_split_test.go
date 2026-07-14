package filter

import (
	"context"
	"strings"
	"testing"

	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"github.com/shouni/ap-mv/internal/domain"
)

func TestSceneSplitFilterExpandsLongCutsBeforeKeyframeGeneration(t *testing.T) {
	recipe := &orchestrator.VideoRecipe{
		ProjectTitle: "test",
		MusicRecipe: orchestrator.MusicRecipe{
			Title: "test",
			Sections: []orchestrator.Section{{
				Name:         "Chorus",
				Duration:     20,
				StartSeconds: 0,
				EndSeconds:   20,
				Prompt:       "chorus lift",
			}},
		},
		Cuts: []orchestrator.Cut{{
			CutIndex:          1,
			DurationSec:       20,
			AudioCue:          "chorus lift",
			VisualAnchor:      "protagonist on a glowing rooftop",
			Dialogue:          "line one\nline two\nline three",
			KeyframeReference: "gs://bucket/old.png",
			VideoID:           "old-video",
			VideoURL:          "gs://bucket/old.mp4",
			Status:            orchestrator.CutStatusGenerated,
		}},
	}

	err := (SceneSplitFilter{}).Execute(context.Background(), &Context{State: State{Task: &domain.Task{JobID: "job-1", Command: domain.CommandVideoRecipeCreate}, VideoRecipe: recipe}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(recipe.Cuts) != 3 {
		t.Fatalf("cuts = %d, want 3", len(recipe.Cuts))
	}
	wantDurations := []float64{8, 8, 4}
	for i, cut := range recipe.Cuts {
		if cut.CutIndex != i+1 {
			t.Errorf("cut[%d].CutIndex = %d, want %d", i, cut.CutIndex, i+1)
		}
		if cut.DurationSec != wantDurations[i] {
			t.Errorf("cut[%d].DurationSec = %v, want %v", i, cut.DurationSec, wantDurations[i])
		}
		if cut.Status != orchestrator.CutStatusPending {
			t.Errorf("cut[%d].Status = %q, want pending", i, cut.Status)
		}
		if cut.KeyframeReference != "" || cut.VideoID != "" || cut.VideoURL != "" {
			t.Errorf("cut[%d] generation fields were not reset: keyframe=%q videoID=%q videoURL=%q", i, cut.KeyframeReference, cut.VideoID, cut.VideoURL)
		}
		if !strings.Contains(cut.VisualAnchor, "Scene beat") {
			t.Errorf("cut[%d].VisualAnchor = %q, want scene beat direction", i, cut.VisualAnchor)
		}
	}
	if recipe.Cuts[0].VisualAnchor == recipe.Cuts[1].VisualAnchor {
		t.Fatal("split cuts reused the same visual anchor; want distinct keyframe direction")
	}
}

func TestSceneSplitFilterBalancesVideoToVideoSceneBlocks(t *testing.T) {
	recipe := &orchestrator.VideoRecipe{
		ProjectTitle: "test",
		MusicRecipe:  orchestrator.MusicRecipe{Title: "test"},
		Cuts: []orchestrator.Cut{{
			CutIndex:     1,
			DurationSec:  50,
			AudioCue:     "long chorus",
			VisualAnchor: "protagonist crossing a luminous city stage",
			Dialogue:     "a\nb\nc\nd\ne",
		}},
	}

	err := (SceneSplitFilter{UsePreviousVideo: true}).Execute(context.Background(), &Context{State: State{Task: &domain.Task{JobID: "job-1", Command: domain.CommandVideoRecipeCreate}, VideoRecipe: recipe}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	wantDurations := []float64{15, 15, 22}
	if len(recipe.Cuts) != len(wantDurations) {
		t.Fatalf("cuts = %d, want %d", len(recipe.Cuts), len(wantDurations))
	}
	for i, want := range wantDurations {
		if recipe.Cuts[i].DurationSec != want {
			t.Errorf("cut[%d].DurationSec = %v, want %v", i, recipe.Cuts[i].DurationSec, want)
		}
		if i == 0 && recipe.Cuts[i].IsSectionStart {
			t.Errorf("cut[0].IsSectionStart = true, want false")
		}
		if i > 0 && !recipe.Cuts[i].IsSectionStart {
			t.Errorf("cut[%d].IsSectionStart = false, want true for a new scene/keyframe block", i)
		}
	}
}
