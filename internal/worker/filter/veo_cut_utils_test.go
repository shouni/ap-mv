package filter

import (
	"testing"

	orchestrator "github.com/shouni/go-veo-orchestrator/ports"
)

// TestExpandCutsToSupportedDurationsUsePreviousVideo verifies that, when usePreviousVideo is
// enabled, every cut after the first is normalized to Veo's video_extension duration (7s)
// instead of the image_to_video set ({4,6,8}), since those cuts carry a PreviousVideoID.
func TestExpandCutsToSupportedDurationsUsePreviousVideo(t *testing.T) {
	cuts := []orchestrator.Cut{
		{CutIndex: 1, StartSec: 90, DurationSec: 8},
		{CutIndex: 2, StartSec: 98, DurationSec: 8},
		{CutIndex: 3, StartSec: 106, DurationSec: 8},
	}

	got := expandCutsToSupportedDurations(cuts, true, nil)

	if len(got) != 3 {
		t.Fatalf("cuts = %d, want 3", len(got))
	}
	if got[0].DurationSec != 8 {
		t.Errorf("cut[0] duration = %v, want 8 (no previous video)", got[0].DurationSec)
	}
	for i := 1; i < len(got); i++ {
		if got[i].DurationSec != veoVideoExtensionDurationSec {
			t.Errorf("cut[%d] duration = %v, want %v (video_extension)", i, got[i].DurationSec, veoVideoExtensionDurationSec)
		}
		wantEnd := got[i].StartSec + veoVideoExtensionDurationSec
		if got[i].EndSec != wantEnd {
			t.Errorf("cut[%d] end = %v, want %v", i, got[i].EndSec, wantEnd)
		}
	}
}

// TestExpandCutsToSupportedDurationsSkipsGeneratedCuts verifies that already-generated cuts
// keep their recorded duration even when usePreviousVideo is enabled, so resuming a job never
// rewrites metadata for cuts whose video already exists.
func TestExpandCutsToSupportedDurationsSkipsGeneratedCuts(t *testing.T) {
	cuts := []orchestrator.Cut{
		{CutIndex: 1, StartSec: 90, DurationSec: 8, Status: orchestrator.CutStatusGenerated, VideoID: "gs://bucket/cut_01.mp4"},
		{CutIndex: 2, StartSec: 98, DurationSec: 8},
	}

	got := expandCutsToSupportedDurations(cuts, true, nil)

	if got[0].DurationSec != 8 {
		t.Errorf("generated cut[0] duration = %v, want unchanged 8", got[0].DurationSec)
	}
	if got[1].DurationSec != veoVideoExtensionDurationSec {
		t.Errorf("cut[1] duration = %v, want %v", got[1].DurationSec, veoVideoExtensionDurationSec)
	}
}

// TestExpandCutsToSupportedDurationsDisabled verifies the previous {4,6,8} snapping behavior is
// unchanged when usePreviousVideo is false (e.g. SectionSelectFilter's initial split/cap pass).
func TestExpandCutsToSupportedDurationsDisabled(t *testing.T) {
	cuts := []orchestrator.Cut{
		{CutIndex: 1, StartSec: 0, DurationSec: 8},
		{CutIndex: 2, StartSec: 8, DurationSec: 8},
	}

	got := expandCutsToSupportedDurations(cuts, false, nil)

	for i, cut := range got {
		if cut.DurationSec != 8 {
			t.Errorf("cut[%d] duration = %v, want 8", i, cut.DurationSec)
		}
	}
}

// TestExpandCutsToSupportedDurationsSectionBoundaryForcesReset verifies that a section change
// resets the cumulative-duration chain even though the technical cap hasn't been reached, and
// that only the section-boundary cut is marked IsSectionStart (not ordinary technical resets).
// Note: IsChainStart itself is set later by runDirect (3_video_gen.go), not by this function.
func TestExpandCutsToSupportedDurationsSectionBoundaryForcesReset(t *testing.T) {
	sections := []orchestrator.Section{
		{Name: "Verse", StartSeconds: 0, EndSeconds: 16},
		{Name: "Chorus", StartSeconds: 16, EndSeconds: 32},
	}
	cuts := []orchestrator.Cut{
		{CutIndex: 1, StartSec: 0, DurationSec: 8},  // Verse start (i==0, not a "section boundary")
		{CutIndex: 2, StartSec: 8, DurationSec: 8},  // still Verse, would continue (cumulative 8+7=15<=30)
		{CutIndex: 3, StartSec: 16, DurationSec: 8}, // Chorus start: section boundary despite cumulative headroom
		{CutIndex: 4, StartSec: 24, DurationSec: 8}, // still Chorus, continuation
	}

	got := expandCutsToSupportedDurations(cuts, true, sections)

	wantSectionStart := []bool{false, false, true, false}
	for i := range got {
		if got[i].IsSectionStart != wantSectionStart[i] {
			t.Errorf("cut[%d] IsSectionStart = %v, want %v", i, got[i].IsSectionStart, wantSectionStart[i])
		}
	}
	// cut[1] is an ordinary continuation: forced to the video_extension duration (7s).
	if got[1].DurationSec != veoVideoExtensionDurationSec {
		t.Errorf("cut[1] duration = %v, want %v (ordinary continuation)", got[1].DurationSec, veoVideoExtensionDurationSec)
	}
	// cut[2] is the section boundary: it must keep its snapped base duration (8s), not be
	// forced to the technical reset's 7s continuation value.
	if got[2].DurationSec != 8 {
		t.Errorf("cut[2] duration = %v, want 8 (section-boundary reset keeps base duration)", got[2].DurationSec)
	}
}

// TestExpandCutsToSupportedDurationsNoSectionsIsNoop verifies that omitting sections (nil)
// behaves exactly like before section-boundary detection existed.
func TestExpandCutsToSupportedDurationsNoSectionsIsNoop(t *testing.T) {
	cuts := []orchestrator.Cut{
		{CutIndex: 1, StartSec: 0, DurationSec: 8},
		{CutIndex: 2, StartSec: 8, DurationSec: 8},
	}

	got := expandCutsToSupportedDurations(cuts, true, nil)

	for i, cut := range got {
		if cut.IsSectionStart {
			t.Errorf("cut[%d] IsSectionStart = true, want false when sections is nil", i)
		}
	}
}
