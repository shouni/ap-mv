package filter

import (
	"context"
	"testing"

	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"github.com/shouni/ap-mv/internal/domain"
)

// TestRunDirectAppliesChainResetKeyframeAndMarksIsChainStart verifies that, when a chain
// resets (mirroring expandCutsToSupportedDurations' behavior for a section needing more than
// Veo's ~30s video_extension continuation limit), the reset cut is marked IsChainStart, its
// KeyframeReference is overridden with the previous chain's extracted last frame, and the
// frame is extracted from the previous cut's color-corrected video rather than the raw Veo
// output.
func TestRunDirectAppliesChainResetKeyframeAndMarksIsChainStart(t *testing.T) {
	recipe := &orchestrator.VideoRecipe{
		MusicRecipe: orchestrator.MusicRecipe{Title: "test"},
		Cuts: []orchestrator.Cut{
			{CutIndex: 1, VisualAnchor: "a", AudioSync: orchestrator.AudioSync{DurationSec: 8}, KeyframeResult: orchestrator.KeyframeResult{KeyframeReference: "gs://bucket/static.png"}},
			{CutIndex: 2, VisualAnchor: "a", AudioSync: orchestrator.AudioSync{DurationSec: 8}, KeyframeResult: orchestrator.KeyframeResult{KeyframeReference: "gs://bucket/static.png"}},
			{CutIndex: 3, VisualAnchor: "a", AudioSync: orchestrator.AudioSync{DurationSec: 8}, KeyframeResult: orchestrator.KeyframeResult{KeyframeReference: "gs://bucket/static.png"}},
			{CutIndex: 4, VisualAnchor: "a", AudioSync: orchestrator.AudioSync{DurationSec: 8}, KeyframeResult: orchestrator.KeyframeResult{KeyframeReference: "gs://bucket/static.png"}},
			{CutIndex: 5, VisualAnchor: "a", AudioSync: orchestrator.AudioSync{DurationSec: 8}, KeyframeResult: orchestrator.KeyframeResult{KeyframeReference: "gs://bucket/static.png"}},
		},
	}
	task := &domain.Task{JobID: "job-1", Command: domain.CommandMVFromKeyframeVideoRecipe, VideoRecipe: recipe}
	vp := &recordingVideoProcessor{}
	flt := VideoGenerationFilter{Runner: indexedURLRunner{}, UsePreviousVideo: true, VideoProcessor: vp}

	err := flt.Execute(context.Background(), &Context{
		State: State{
			Task:        task,
			VideoRecipe: recipe,
			OutputPath:  "gs://bucket/jobs/job-1/",
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// 8,7,7,8(reset),7: cumulative 8->15->22, then 22+7>24 so cut4 resets.
	// veoContinuationMaxDurationSec=24 caps each chain at 2 video_extension continuations
	// (not 3) to limit color-drift accumulation, so this chain is shorter than Veo's ~30s
	// hard API limit would otherwise allow.
	wantChainStart := []bool{true, false, false, true, false}
	for i, want := range wantChainStart {
		if recipe.Cuts[i].IsChainStart != want {
			t.Errorf("cut[%d] IsChainStart = %v, want %v", i, recipe.Cuts[i].IsChainStart, want)
		}
	}

	if len(vp.extractCalls) != 1 {
		t.Fatalf("ExtractLastFrame calls = %d, want 1 (only for the mid-job reset)", len(vp.extractCalls))
	}
	// cut3 is a video_extension continuation, so it goes through colorCorrectExtensionCut
	// before becoming lastVideoID — the reset's frame extraction must read from that
	// corrected video, not the raw (possibly color-drifted) Veo output.
	if vp.extractCalls[0].videoURI != "gs://bucket/jobs/job-1/videos/cut_03_color_corrected.mp4" {
		t.Errorf("ExtractLastFrame videoURI = %q, want previous cut's color-corrected video URL", vp.extractCalls[0].videoURI)
	}
	if vp.extractCalls[0].destURI != "gs://bucket/jobs/job-1/images/chain_frame_cut_04.png" {
		t.Errorf("ExtractLastFrame destURI = %q", vp.extractCalls[0].destURI)
	}
	if recipe.Cuts[3].KeyframeReference != vp.extractCalls[0].destURI {
		t.Errorf("cut[3] KeyframeReference = %q, want extracted frame URI %q", recipe.Cuts[3].KeyframeReference, vp.extractCalls[0].destURI)
	}
}

// TestRunDirectColorCorrectsVideoExtensionCuts verifies that only video_extension cuts (the
// ones that reuse the previous generation as Veo's video-to-video conditioning input) get
// color-matched against the cut's own scene keyframe image, and that the corrected
// VideoURL/VideoID — not the raw Veo output — is what chains forward as the next cut's
// PreviousVideoID. Repeated video_extension rounds drift in saturation (confirmed on a real
// job: +20% after the first continuation, climbing further each round), so leaving the raw
// output uncorrected would let the drift compound round over round instead of being pulled
// back each time.
//
// The correction target is deliberately cut.KeyframeReference, not the character's
// ReferenceURL — that regressed on a real job (short-20260712-044229-211cbcb414ac): the
// character reference art is a white-background turnaround design sheet (measured
// SATAVG=5.2), and correcting toward it desaturated the video into a washed-out look. The
// scene keyframe (a fully rendered "Cinematic anime style" image, SATAVG~28.6) is the
// correct target.
func TestRunDirectColorCorrectsVideoExtensionCuts(t *testing.T) {
	recipe := &orchestrator.VideoRecipe{
		MusicRecipe: orchestrator.MusicRecipe{Title: "test"},
		Cuts: []orchestrator.Cut{
			{CutIndex: 1, VisualAnchor: "a", CharacterID: "tsumugi", AudioSync: orchestrator.AudioSync{DurationSec: 8}, KeyframeResult: orchestrator.KeyframeResult{KeyframeReference: "gs://bucket/keyframes/scene.png"}},
			{CutIndex: 2, VisualAnchor: "a", CharacterID: "tsumugi", AudioSync: orchestrator.AudioSync{DurationSec: 8}, KeyframeResult: orchestrator.KeyframeResult{KeyframeReference: "gs://bucket/keyframes/scene.png"}},
			{CutIndex: 3, VisualAnchor: "a", CharacterID: "tsumugi", AudioSync: orchestrator.AudioSync{DurationSec: 8}, KeyframeResult: orchestrator.KeyframeResult{KeyframeReference: "gs://bucket/keyframes/scene.png"}},
		},
	}
	task := &domain.Task{JobID: "job-1", Command: domain.CommandMVFromKeyframeVideoRecipe, VideoRecipe: recipe}
	vp := &recordingVideoProcessor{}
	flt := VideoGenerationFilter{Runner: indexedURLRunner{}, UsePreviousVideo: true, VideoProcessor: vp}

	err := flt.Execute(context.Background(), &Context{
		State: State{
			Task:        task,
			VideoRecipe: recipe,
			OutputPath:  "gs://bucket/jobs/job-1/",
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// 8,7,7: cumulative 8->15->22, both within the 24s cap so no reset — cut2/cut3 are both
	// video_extension continuations (duration 7) and should be color-corrected; cut1 is the
	// chain start (duration 8) and should not.
	if len(vp.colorMatchCalls) != 2 {
		t.Fatalf("colorMatchCalls = %d, want 2 (cut2 and cut3 only)", len(vp.colorMatchCalls))
	}
	wantVideoURIs := []string{"gs://bucket/cut_2.mp4", "gs://bucket/cut_3.mp4"}
	wantDestURIs := []string{
		"gs://bucket/jobs/job-1/videos/cut_02_color_corrected.mp4",
		"gs://bucket/jobs/job-1/videos/cut_03_color_corrected.mp4",
	}
	for i, call := range vp.colorMatchCalls {
		if call.videoURI != wantVideoURIs[i] {
			t.Errorf("colorMatchCalls[%d].videoURI = %q, want %q", i, call.videoURI, wantVideoURIs[i])
		}
		if call.referenceImageURI != "gs://bucket/keyframes/scene.png" {
			t.Errorf("colorMatchCalls[%d].referenceImageURI = %q, want the cut's scene keyframe, not character art", i, call.referenceImageURI)
		}
		if call.destURI != wantDestURIs[i] {
			t.Errorf("colorMatchCalls[%d].destURI = %q, want %q", i, call.destURI, wantDestURIs[i])
		}
	}

	// The corrected URI must be what's stored on the cut (and thus what chains forward as the
	// next cut's PreviousVideoID) — not the raw, uncorrected Veo output.
	if recipe.Cuts[1].VideoURL != wantDestURIs[0] || recipe.Cuts[1].VideoID != wantDestURIs[0] {
		t.Errorf("cut[1] VideoURL/VideoID = %q/%q, want corrected URI %q", recipe.Cuts[1].VideoURL, recipe.Cuts[1].VideoID, wantDestURIs[0])
	}
	if recipe.Cuts[2].VideoURL != wantDestURIs[1] || recipe.Cuts[2].VideoID != wantDestURIs[1] {
		t.Errorf("cut[2] VideoURL/VideoID = %q/%q, want corrected URI %q", recipe.Cuts[2].VideoURL, recipe.Cuts[2].VideoID, wantDestURIs[1])
	}
	// cut1 (chain start, not video_extension) must be untouched.
	if recipe.Cuts[0].VideoURL != "gs://bucket/cut_1.mp4" {
		t.Errorf("cut[0] VideoURL = %q, want raw uncorrected Veo output (chain start, not video_extension)", recipe.Cuts[0].VideoURL)
	}
}

// TestRunDirectSkipsFrameExtractionAtSectionBoundary verifies that a section-boundary reset
// (Verse -> Chorus) is marked IsChainStart like any other reset (so ChainFinalizeFilter still
// treats it as a hard-cut boundary), but does NOT extract the previous chain's last frame —
// the section's own intended keyframe is used instead, since the scene is meant to change.
func TestRunDirectSkipsFrameExtractionAtSectionBoundary(t *testing.T) {
	recipe := &orchestrator.VideoRecipe{
		MusicRecipe: orchestrator.MusicRecipe{
			Title: "test",
			Sections: []orchestrator.Section{
				{Name: "Verse", StartSeconds: 0, EndSeconds: 16},
				{Name: "Chorus", StartSeconds: 16, EndSeconds: 32},
			},
		},
		Cuts: []orchestrator.Cut{
			{CutIndex: 1, VisualAnchor: "verse", AudioSync: orchestrator.AudioSync{StartSec: 0, DurationSec: 8}, KeyframeResult: orchestrator.KeyframeResult{KeyframeReference: "gs://bucket/verse.png"}},
			{CutIndex: 2, VisualAnchor: "verse", AudioSync: orchestrator.AudioSync{StartSec: 8, DurationSec: 8}, KeyframeResult: orchestrator.KeyframeResult{KeyframeReference: "gs://bucket/verse.png"}},
			{CutIndex: 3, VisualAnchor: "chorus", AudioSync: orchestrator.AudioSync{StartSec: 16, DurationSec: 8}, KeyframeResult: orchestrator.KeyframeResult{KeyframeReference: "gs://bucket/chorus.png"}},
			{CutIndex: 4, VisualAnchor: "chorus", AudioSync: orchestrator.AudioSync{StartSec: 24, DurationSec: 8}, KeyframeResult: orchestrator.KeyframeResult{KeyframeReference: "gs://bucket/chorus.png"}},
		},
	}
	task := &domain.Task{JobID: "job-1", Command: domain.CommandMVFromKeyframeVideoRecipe, VideoRecipe: recipe}
	vp := &recordingVideoProcessor{}
	flt := VideoGenerationFilter{Runner: indexedURLRunner{}, UsePreviousVideo: true, VideoProcessor: vp}

	err := flt.Execute(context.Background(), &Context{
		State: State{
			Task:        task,
			VideoRecipe: recipe,
			OutputPath:  "gs://bucket/jobs/job-1/",
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if !recipe.Cuts[2].IsChainStart {
		t.Error("cut[2] IsChainStart = false, want true (section boundary is still a chain boundary)")
	}
	if !recipe.Cuts[2].IsSectionStart {
		t.Error("cut[2] IsSectionStart = false, want true")
	}
	if len(vp.extractCalls) != 0 {
		t.Fatalf("ExtractLastFrame calls = %d, want 0 (section boundary must not extract the previous chain's frame)", len(vp.extractCalls))
	}
	if recipe.Cuts[2].KeyframeReference != "gs://bucket/chorus.png" {
		t.Errorf("cut[2] KeyframeReference = %q, want unchanged section keyframe", recipe.Cuts[2].KeyframeReference)
	}
}
