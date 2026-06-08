package pipeline

import (
	"testing"

	"ap-mv/internal/domain"
)

func TestDefaultFiltersForComposeToKeyframeStopsAfterCutKeyframe(t *testing.T) {
	filters := defaultFilters(domain.CommandComposeToKeyframe, nil)

	got := make([]string, 0, len(filters))
	for _, flt := range filters {
		got = append(got, flt.Name())
	}
	want := []string{"scripting", "cut_keyframe_gen"}
	if len(got) != len(want) {
		t.Fatalf("filters = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("filters = %v, want %v", got, want)
		}
	}
}

func TestDefaultFiltersForComposeStillRunsFullPipeline(t *testing.T) {
	filters := defaultFilters(domain.CommandCompose, nil)

	got := make([]string, 0, len(filters))
	for _, flt := range filters {
		got = append(got, flt.Name())
	}
	want := []string{"scripting", "cut_keyframe_gen", "video_gen", "publishing"}
	if len(got) != len(want) {
		t.Fatalf("filters = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("filters = %v, want %v", got, want)
		}
	}
}
