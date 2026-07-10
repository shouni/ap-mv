package filter

import (
	"context"
	"strings"
	"testing"

	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"github.com/shouni/ap-mv/internal/domain"
)

func newSectionSelectRecipe() *orchestrator.VideoRecipe {
	return &orchestrator.VideoRecipe{
		ProjectTitle: "test",
		MusicRecipe: domain.MusicRecipe{
			Title: "test",
			Sections: []domain.MusicSection{
				{Name: "Aメロ", StartSeconds: 0, EndSeconds: 10},
				{Name: "サビ", StartSeconds: 10, EndSeconds: 20},
			},
		},
		Cuts: []orchestrator.Cut{
			{
				CutIndex:          1,
				StartSec:          0,
				EndSec:            10,
				DurationSec:       10,
				KeyframeReference: "gs://bucket/jobs/orig-job/images/cut_1.png",
				Status:            orchestrator.CutStatusGenerated,
				VideoID:           "video-1",
				VideoURL:          "gs://bucket/jobs/orig-job/videos/cut_1.mp4",
			},
			{
				CutIndex:          2,
				StartSec:          10,
				EndSec:            20,
				DurationSec:       10,
				KeyframeReference: "images/cut_2.png",
				Status:            orchestrator.CutStatusGenerated,
				VideoID:           "video-2",
				VideoURL:          "gs://bucket/jobs/orig-job/videos/cut_2.mp4",
			},
		},
	}
}

// TestSectionSelectFilterTrimsToSectionCuts verifies that only the selected section's cuts
// remain, their generation state is reset, and relative keyframe references are resolved
// against the original job root.
func TestSectionSelectFilterTrimsToSectionCuts(t *testing.T) {
	sectionIndex := 1
	fc := &Context{
		Task: &domain.Task{
			JobID:        "short-1",
			Command:      domain.CommandShortVideoFromSection,
			SectionIndex: &sectionIndex,
			RecipeURL:    "gs://bucket/jobs/orig-job/video_music_meta.json",
		},
		VideoRecipe: newSectionSelectRecipe(),
	}

	if err := (SectionSelectFilter{}).Execute(context.Background(), fc); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if len(fc.VideoRecipe.Cuts) != 1 {
		t.Fatalf("cuts = %d, want 1", len(fc.VideoRecipe.Cuts))
	}
	cut := fc.VideoRecipe.Cuts[0]
	if cut.CutIndex != 2 {
		t.Fatalf("cut index = %d, want 2", cut.CutIndex)
	}
	if cut.Status != orchestrator.CutStatusPending {
		t.Errorf("status = %q, want %q", cut.Status, orchestrator.CutStatusPending)
	}
	if cut.VideoID != "" || cut.VideoURL != "" {
		t.Errorf("video state not cleared: id=%q url=%q", cut.VideoID, cut.VideoURL)
	}
	if want := "gs://bucket/jobs/orig-job/images/cut_2.png"; cut.KeyframeReference != want {
		t.Errorf("keyframe reference = %q, want %q", cut.KeyframeReference, want)
	}
}

// TestResolveRecipeObjectURI verifies safe joining regardless of trailing/leading slashes.
func TestResolveRecipeObjectURI(t *testing.T) {
	tests := []struct {
		name string
		base string
		ref  string
		want string
	}{
		{name: "base with trailing slash", base: "gs://bucket/jobs/job-1/", ref: "images/cut_1.png", want: "gs://bucket/jobs/job-1/images/cut_1.png"},
		{name: "base without trailing slash", base: "gs://bucket/jobs/job-1", ref: "images/cut_1.png", want: "gs://bucket/jobs/job-1/images/cut_1.png"},
		{name: "ref with leading slash", base: "gs://bucket/jobs/job-1/", ref: "/images/cut_1.png", want: "gs://bucket/jobs/job-1/images/cut_1.png"},
		{name: "absolute ref kept", base: "gs://bucket/jobs/job-1/", ref: "gs://other/keyframe.png", want: "gs://other/keyframe.png"},
		{name: "empty base kept", base: "", ref: "images/cut_1.png", want: "images/cut_1.png"},
		{name: "empty ref kept", base: "gs://bucket/jobs/job-1/", ref: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveRecipeObjectURI(tt.base, tt.ref); got != tt.want {
				t.Fatalf("resolveRecipeObjectURI(%q, %q) = %q, want %q", tt.base, tt.ref, got, tt.want)
			}
		})
	}
}

// TestSectionSelectFilterRejectsOutOfRangeSection verifies out-of-range section indexes fail.
func TestSectionSelectFilterRejectsOutOfRangeSection(t *testing.T) {
	sectionIndex := 5
	fc := &Context{
		Task: &domain.Task{
			JobID:        "short-1",
			Command:      domain.CommandShortVideoFromSection,
			SectionIndex: &sectionIndex,
		},
		VideoRecipe: newSectionSelectRecipe(),
	}

	err := (SectionSelectFilter{}).Execute(context.Background(), fc)
	if err == nil || !strings.Contains(err.Error(), "out of range") {
		t.Fatalf("Execute() error = %v, want out of range error", err)
	}
}

// TestSectionSelectFilterRejectsEmptySection verifies sections without cuts fail rather than
// enqueueing an empty video generation.
func TestSectionSelectFilterRejectsEmptySection(t *testing.T) {
	sectionIndex := 1
	recipe := newSectionSelectRecipe()
	recipe.Cuts = recipe.Cuts[:1] // サビのカットを取り除く
	fc := &Context{
		Task: &domain.Task{
			JobID:        "short-1",
			Command:      domain.CommandShortVideoFromSection,
			SectionIndex: &sectionIndex,
		},
		VideoRecipe: recipe,
	}

	err := (SectionSelectFilter{}).Execute(context.Background(), fc)
	if err == nil || !strings.Contains(err.Error(), "no cuts found") {
		t.Fatalf("Execute() error = %v, want no cuts found error", err)
	}
}
