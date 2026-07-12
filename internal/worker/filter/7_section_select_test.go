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
// remain, their generation state is reset, relative keyframe references are resolved against
// the original job root, and durations are normalized to Veo-supported values (a 10s cut is
// split into 8s + 4s sub-cuts sharing the same keyframe).
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

	if len(fc.VideoRecipe.Cuts) != 2 {
		t.Fatalf("cuts = %d, want 2 (10s cut split into 8s + 4s)", len(fc.VideoRecipe.Cuts))
	}
	for i, wantDuration := range []float64{8, 4} {
		cut := fc.VideoRecipe.Cuts[i]
		if cut.CutIndex != i+1 {
			t.Errorf("cut[%d] index = %d, want %d", i, cut.CutIndex, i+1)
		}
		if cut.DurationSec != wantDuration {
			t.Errorf("cut[%d] duration = %v, want %v", i, cut.DurationSec, wantDuration)
		}
		if cut.Status != orchestrator.CutStatusPending {
			t.Errorf("cut[%d] status = %q, want %q", i, cut.Status, orchestrator.CutStatusPending)
		}
		if cut.VideoID != "" || cut.VideoURL != "" {
			t.Errorf("cut[%d] video state not cleared: id=%q url=%q", i, cut.VideoID, cut.VideoURL)
		}
		if want := "gs://bucket/jobs/orig-job/images/cut_2.png"; cut.KeyframeReference != want {
			t.Errorf("cut[%d] keyframe reference = %q, want %q", i, cut.KeyframeReference, want)
		}
	}
	if got := fc.VideoRecipe.Cuts[0].StartSec; got != 10 {
		t.Errorf("cut[0] start = %v, want 10", got)
	}
	if got := fc.VideoRecipe.Cuts[1].StartSec; got != 18 {
		t.Errorf("cut[1] start = %v, want 18", got)
	}
}

// TestSplitCutBySupportedDurations verifies long cuts are split into Veo-supported durations
// and dialogue lines are distributed across the sub-cuts.
func TestSplitCutBySupportedDurations(t *testing.T) {
	cut := orchestrator.Cut{
		CutIndex:          3,
		StartSec:          40,
		EndSec:            75,
		DurationSec:       35,
		KeyframeReference: "gs://bucket/jobs/orig/images/cut_3.png",
		Dialogue:          "line1\nline2\nline3\nline4\nline5",
	}

	subCuts := splitCutBySupportedDurations(cut, veoSupportedDurationsSec)

	wantDurations := []float64{8, 8, 8, 8, 4}
	if len(subCuts) != len(wantDurations) {
		t.Fatalf("sub cuts = %d, want %d", len(subCuts), len(wantDurations))
	}
	offset := 40.0
	for i, want := range wantDurations {
		if subCuts[i].DurationSec != want {
			t.Errorf("sub[%d] duration = %v, want %v", i, subCuts[i].DurationSec, want)
		}
		if subCuts[i].StartSec != offset {
			t.Errorf("sub[%d] start = %v, want %v", i, subCuts[i].StartSec, offset)
		}
		if subCuts[i].KeyframeReference != cut.KeyframeReference {
			t.Errorf("sub[%d] keyframe = %q, want parent keyframe", i, subCuts[i].KeyframeReference)
		}
		offset += want
	}
	if subCuts[0].Dialogue != "line1" || subCuts[4].Dialogue != "line5" {
		t.Errorf("dialogue distribution = %q ... %q, want line1 ... line5", subCuts[0].Dialogue, subCuts[4].Dialogue)
	}
}

// TestCapCutsTotalDuration verifies the YouTube Shorts 60s cap keeps whole cuts within the
// limit and always keeps at least the first cut.
func TestCapCutsTotalDuration(t *testing.T) {
	cuts := make([]orchestrator.Cut, 9)
	for i := range cuts {
		cuts[i] = orchestrator.Cut{CutIndex: i + 1, DurationSec: 8}
	}
	capped := capCutsTotalDuration(cuts, 60)
	if len(capped) != 7 {
		t.Fatalf("capped cuts = %d, want 7 (7*8=56s <= 60s)", len(capped))
	}

	// 56s + 4s = 60s ちょうどは収まる。
	cuts = append(cuts[:7], orchestrator.Cut{CutIndex: 8, DurationSec: 4})
	capped = capCutsTotalDuration(cuts, 60)
	if len(capped) != 8 {
		t.Fatalf("capped cuts = %d, want 8 (56s+4s=60s)", len(capped))
	}

	// 上限超えの単独カットでも先頭は必ず残す。
	capped = capCutsTotalDuration([]orchestrator.Cut{{CutIndex: 1, DurationSec: 90}}, 60)
	if len(capped) != 1 {
		t.Fatalf("capped cuts = %d, want 1 (first cut always kept)", len(capped))
	}
}

// TestSnapToSupportedDuration verifies snapping to Veo-supported durations (ties round up).
func TestSnapToSupportedDuration(t *testing.T) {
	tests := []struct {
		in   float64
		want float64
	}{
		{in: 0, want: 4},
		{in: 3, want: 4},
		{in: 4, want: 4},
		{in: 5, want: 6},
		{in: 6, want: 6},
		{in: 7, want: 8},
		{in: 8, want: 8},
	}
	for _, tt := range tests {
		if got := snapToSupportedDuration(tt.in, veoSupportedDurationsSec); got != tt.want {
			t.Errorf("snapToSupportedDuration(%v) = %v, want %v", tt.in, got, tt.want)
		}
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
