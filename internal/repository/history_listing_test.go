package repository

import (
	"testing"

	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"github.com/shouni/ap-mv/internal/domain"
)

// TestVideoHistoryFromRecipeCarriesAspectRatio verifies the recipe's AspectRatio (set once at
// keyframe creation time by CutKeyframeFilter) flows through to the history list/detail view, so
// handlers building follow-up tasks (generate video, regenerate cut keyframe) can inherit it.
func TestVideoHistoryFromRecipeCarriesAspectRatio(t *testing.T) {
	recipe := domain.VideoRecipe{
		ProjectTitle: "test",
		AspectRatio:  "9:16",
		Cuts:         []orchestrator.Cut{{CutIndex: 1, VideoResult: orchestrator.VideoResult{Status: orchestrator.CutStatusGenerated}}},
	}

	got := videoHistoryFromRecipe("job-1", "gs://bucket/jobs/job-1/video_music_meta.json", recipe)

	if got.AspectRatio != "9:16" {
		t.Errorf("AspectRatio = %q, want %q", got.AspectRatio, "9:16")
	}
}

// TestVideoHistoryFromRecipeEmptyAspectRatio verifies old recipes predating this field (no
// aspect_ratio in their JSON) surface an empty AspectRatio rather than a fabricated default —
// callers decide their own fallback (e.g. handlers pass it through as-is; CutKeyframeFilter and
// the keyframe Generator apply the actual "16:9" default when generating).
func TestVideoHistoryFromRecipeEmptyAspectRatio(t *testing.T) {
	recipe := domain.VideoRecipe{
		ProjectTitle: "test",
		Cuts:         []orchestrator.Cut{{CutIndex: 1, VideoResult: orchestrator.VideoResult{Status: orchestrator.CutStatusGenerated}}},
	}

	got := videoHistoryFromRecipe("job-1", "gs://bucket/jobs/job-1/video_music_meta.json", recipe)

	if got.AspectRatio != "" {
		t.Errorf("AspectRatio = %q, want empty for a recipe with none recorded", got.AspectRatio)
	}
}
