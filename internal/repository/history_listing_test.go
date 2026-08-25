package repository

import (
	"testing"

	"github.com/shouni/go-veo-orchestrator/video"

	"github.com/shouni/ap-mv/internal/domain"
)

// TestVideoHistoryFromRecipeCarriesAspectRatio verifies the recipe's AspectRatio (set once at
// keyframe creation time by CutKeyframeFilter) flows through to the history list/detail view, so
// handlers building follow-up tasks (generate video, regenerate cut keyframe) can inherit it.
func TestVideoHistoryFromRecipeCarriesAspectRatio(t *testing.T) {
	recipe := domain.VideoRecipe{
		ProjectTitle: "test",
		AspectRatio:  "9:16",
		Cuts:         []video.Cut{{CutIndex: 1, Status: video.CutStatusGenerated}},
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
		Cuts:         []video.Cut{{CutIndex: 1, Status: video.CutStatusGenerated}},
	}

	got := videoHistoryFromRecipe("job-1", "gs://bucket/jobs/job-1/video_music_meta.json", recipe)

	if got.AspectRatio != "" {
		t.Errorf("AspectRatio = %q, want empty for a recipe with none recorded", got.AspectRatio)
	}
}

// TestVideoHistoryFromRecipeSumsGeneratedSeconds verifies the history list carries the billable
// Veo seconds (generated cuts only), so the list page can price a job without loading its cuts.
// The seconds are derived purely from the recipe, which is why they are computed here rather
// than at render time — the value is stable enough to sit in the TTL-cached VideoHistory, while
// the price per second (config, changeable) deliberately is not.
func TestVideoHistoryFromRecipeSumsGeneratedSeconds(t *testing.T) {
	recipe := domain.VideoRecipe{
		ProjectTitle: "test",
		Cuts: []video.Cut{
			{
				CutIndex:    1,
				DurationSec: 8,
				Status:      video.CutStatusGenerated,
			},
			{
				CutIndex:    2,
				DurationSec: 6,
				Status:      video.CutStatusPending,
			},
		},
	}

	got := videoHistoryFromRecipe("job-1", "gs://bucket/jobs/job-1/video_music_meta.json", recipe)

	if got.GeneratedSeconds != 8 {
		t.Errorf("GeneratedSeconds = %v, want 8 (the pending cut never reached Veo)", got.GeneratedSeconds)
	}
	// 単価はリポジトリの責務ではない。キャッシュへ焼き込まないことをここで固定する。
	if got.Cost.HasCost() {
		t.Errorf("Cost = %+v, want it left empty for the handler to fill from config", got.Cost)
	}
}
