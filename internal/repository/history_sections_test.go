package repository

import (
	"testing"

	"github.com/shouni/go-veo-orchestrator/video"

	"github.com/shouni/ap-mv/internal/domain"
)

func TestHistorySectionsFromRecipeMarksFullyGeneratedSections(t *testing.T) {
	t.Parallel()

	recipe := domain.VideoRecipe{
		MusicRecipe: domain.MusicRecipe{
			Sections: []domain.MusicSection{
				{Name: "Verse", StartSeconds: 0, EndSeconds: 10},
				{Name: "Chorus", StartSeconds: 10, EndSeconds: 20},
				{Name: "Bridge", StartSeconds: 20, EndSeconds: 30},
			},
		},
		Cuts: []video.Cut{
			{CutIndex: 1, StartSec: 0, EndSec: 5, Status: video.CutStatusGenerated},
			{CutIndex: 2, StartSec: 5, EndSec: 10, Status: video.CutStatusGenerated},
			{CutIndex: 3, StartSec: 10, EndSec: 15, Status: video.CutStatusGenerated},
			{CutIndex: 4, StartSec: 15, EndSec: 20, Status: video.CutStatusPending},
			// セクション3（Bridge, 20-30s）にはこのレシピ上カットが存在しない
			// （例: short_video_from_section で作られたジョブの保存済みレシピ）。
		},
	}

	sections := historySectionsFromRecipe(recipe)
	if len(sections) != 3 {
		t.Fatalf("len(sections) = %d, want 3", len(sections))
	}
	if !sections[0].Generated {
		t.Errorf("section 0 (Verse, all cuts generated) Generated = false, want true")
	}
	if sections[1].Generated {
		t.Errorf("section 1 (Chorus, has a pending cut) Generated = true, want false")
	}
	if sections[2].Generated {
		t.Errorf("section 2 (Bridge, no cuts in this recipe) Generated = true, want false")
	}
}
