package filter

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	characterkit "github.com/shouni/go-character-kit/character"
	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"github.com/shouni/ap-mv/internal/domain"
	"github.com/shouni/ap-mv/internal/ports"
)

// TestVideoGenerationFilterEnqueuesContinuationAfterOneCut verifies continuation task enqueueing after one cut.
func TestVideoGenerationFilterEnqueuesContinuationAfterOneCut(t *testing.T) {
	recipe := &orchestrator.VideoRecipe{
		MusicRecipe: orchestrator.MusicRecipe{Title: "test"},
		Cuts: []orchestrator.Cut{
			{CutIndex: 1, DurationSec: 8, VisualAnchor: "first"},
			{CutIndex: 2, DurationSec: 8, VisualAnchor: "second"},
		},
	}
	task := &domain.Task{
		JobID:       "job-1",
		Command:     domain.CommandMVFromKeyframeVideoRecipe,
		VideoRecipe: recipe,
	}
	queue := &captureQueue{}
	flt := VideoGenerationFilter{Runner: sequenceRunner{}}

	err := flt.Execute(context.Background(), &Context{
		Task:        task,
		VideoRecipe: recipe,
		TaskQueue:   queue,
	})
	if !errors.Is(err, ErrPipelineDeferred) {
		t.Fatalf("Execute() error = %v, want ErrPipelineDeferred", err)
	}
	if recipe.Cuts[0].Status != orchestrator.CutStatusGenerated {
		t.Fatalf("first cut status = %q, want %q", recipe.Cuts[0].Status, orchestrator.CutStatusGenerated)
	}
	if recipe.Cuts[1].Status == orchestrator.CutStatusGenerated {
		t.Fatalf("second cut should not be generated in the same invocation")
	}
	if len(queue.tasks) != 1 {
		t.Fatalf("enqueued tasks = %d, want 1", len(queue.tasks))
	}
	if queue.tasks[0].Command != domain.CommandVideoGenContinuation {
		t.Fatalf("continuation command = %q, want %q", queue.tasks[0].Command, domain.CommandVideoGenContinuation)
	}
	if queue.tasks[0].VideoRecipe.Cuts[0].VideoID == "" {
		t.Fatalf("continuation task did not include generated cut state")
	}
}

// TestVideoGenerationFilterPrefersDirectRunnerWhenWorkflowExists verifies the production
// path keeps using per-cut continuation even when orchestrator workflows are configured.
func TestVideoGenerationFilterPrefersDirectRunnerWhenWorkflowExists(t *testing.T) {
	recipe := &orchestrator.VideoRecipe{
		MusicRecipe: orchestrator.MusicRecipe{Title: "test"},
		Cuts: []orchestrator.Cut{
			{CutIndex: 1, DurationSec: 8, VisualAnchor: "first"},
			{CutIndex: 2, DurationSec: 8, VisualAnchor: "second"},
		},
	}
	task := &domain.Task{
		JobID:       "job-1",
		Command:     domain.CommandMVFromKeyframeVideoRecipe,
		VideoRecipe: recipe,
	}
	queue := &captureQueue{}
	workflow := &recordingVideoWorkflow{}
	flt := VideoGenerationFilter{Runner: sequenceRunner{}}

	err := flt.Execute(context.Background(), &Context{
		Task:        task,
		VideoRecipe: recipe,
		TaskQueue:   queue,
		Workflows:   &orchestrator.Workflows{Video: workflow},
	})
	if !errors.Is(err, ErrPipelineDeferred) {
		t.Fatalf("Execute() error = %v, want ErrPipelineDeferred", err)
	}
	if workflow.runCalled {
		t.Fatal("workflow video runner was called; want direct per-cut runner")
	}
	if len(queue.tasks) != 1 {
		t.Fatalf("enqueued tasks = %d, want 1", len(queue.tasks))
	}
}

// TestVideoGenerationFilterContinuationFromShortSectionUsesContinuationCommand verifies that
// deferring a short_video_from_section task does not re-trigger section_select on resume
// (which would reset already-generated cuts back to pending) or cut_keyframe_gen/zip_upload
// (which would discard the keyframes reused from the original job).
func TestVideoGenerationFilterContinuationFromShortSectionUsesContinuationCommand(t *testing.T) {
	sectionIndex := 0
	recipe := &orchestrator.VideoRecipe{
		MusicRecipe: orchestrator.MusicRecipe{Title: "test"},
		Cuts: []orchestrator.Cut{
			{CutIndex: 1, DurationSec: 8, VisualAnchor: "first"},
			{CutIndex: 2, DurationSec: 8, VisualAnchor: "second"},
		},
	}
	task := &domain.Task{
		JobID:        "short-1",
		Command:      domain.CommandShortVideoFromSection,
		SectionIndex: &sectionIndex,
		VideoRecipe:  recipe,
	}
	queue := &captureQueue{}
	flt := VideoGenerationFilter{Runner: sequenceRunner{}}

	err := flt.Execute(context.Background(), &Context{
		Task:        task,
		VideoRecipe: recipe,
		TaskQueue:   queue,
	})
	if !errors.Is(err, ErrPipelineDeferred) {
		t.Fatalf("Execute() error = %v, want ErrPipelineDeferred", err)
	}
	if len(queue.tasks) != 1 {
		t.Fatalf("enqueued tasks = %d, want 1", len(queue.tasks))
	}
	if queue.tasks[0].Command != domain.CommandVideoGenContinuation {
		t.Fatalf("continuation command = %q, want %q", queue.tasks[0].Command, domain.CommandVideoGenContinuation)
	}
}

// TestVideoGenerationFilterAddsOutputPathToContext verifies that video generation receives the output path through context.
func TestVideoGenerationFilterAddsOutputPathToContext(t *testing.T) {
	recipe := &orchestrator.VideoRecipe{
		MusicRecipe: orchestrator.MusicRecipe{Title: "test"},
		Cuts: []orchestrator.Cut{
			{CutIndex: 1, DurationSec: 8, VisualAnchor: "first"},
		},
	}
	task := &domain.Task{
		JobID:       "job-1",
		Command:     domain.CommandMVFromKeyframeVideoRecipe,
		VideoRecipe: recipe,
	}
	runner := &contextCaptureRunner{}
	flt := VideoGenerationFilter{Runner: runner}

	err := flt.Execute(context.Background(), &Context{
		Task:        task,
		VideoRecipe: recipe,
		OutputPath:  "gs://bucket/ap-mv/veo/jobs/job-1/",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if runner.baseURI != "gs://bucket/ap-mv/veo/jobs/job-1/" {
		t.Fatalf("video output base URI = %q", runner.baseURI)
	}
}

// TestVideoGenerationFilterExpandsUnsupportedDurations verifies that cuts longer than Veo's
// supported durations are split into sub-cuts before generation in the full MV flow.
func TestVideoGenerationFilterExpandsUnsupportedDurations(t *testing.T) {
	recipe := &orchestrator.VideoRecipe{
		MusicRecipe: orchestrator.MusicRecipe{Title: "test"},
		Cuts: []orchestrator.Cut{
			{CutIndex: 1, DurationSec: 10, VisualAnchor: "long cut", KeyframeReference: "gs://bucket/jobs/job-1/images/cut_1.png"},
		},
	}
	task := &domain.Task{
		JobID:       "job-1",
		Command:     domain.CommandMVFromKeyframeVideoRecipe,
		VideoRecipe: recipe,
	}
	flt := VideoGenerationFilter{Runner: sequenceRunner{}}

	err := flt.Execute(context.Background(), &Context{
		Task:        task,
		VideoRecipe: recipe,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(recipe.Cuts) != 2 {
		t.Fatalf("cuts = %d, want 2 (10s cut split into 8s + 4s)", len(recipe.Cuts))
	}
	for i, wantDuration := range []float64{8, 4} {
		if recipe.Cuts[i].DurationSec != wantDuration {
			t.Errorf("cut[%d] duration = %v, want %v", i, recipe.Cuts[i].DurationSec, wantDuration)
		}
		if recipe.Cuts[i].Status != orchestrator.CutStatusGenerated {
			t.Errorf("cut[%d] status = %q, want generated", i, recipe.Cuts[i].Status)
		}
	}
}

// TestRunDirectAppliesChainResetKeyframeAndMarksIsChainStart verifies that, when a chain
// resets (mirroring expandCutsToSupportedDurations' behavior for a section needing more than
// Veo's ~30s video_extension continuation limit), the reset cut is marked IsChainStart, its
// KeyframeReference is overridden with the previous chain's extracted last frame, and
// ExtractLastFrame is NOT called for the very first chain (there is no previous chain yet).
func TestRunDirectAppliesChainResetKeyframeAndMarksIsChainStart(t *testing.T) {
	recipe := &orchestrator.VideoRecipe{
		MusicRecipe: orchestrator.MusicRecipe{Title: "test"},
		Cuts: []orchestrator.Cut{
			{CutIndex: 1, DurationSec: 8, VisualAnchor: "a", KeyframeReference: "gs://bucket/static.png"},
			{CutIndex: 2, DurationSec: 8, VisualAnchor: "a", KeyframeReference: "gs://bucket/static.png"},
			{CutIndex: 3, DurationSec: 8, VisualAnchor: "a", KeyframeReference: "gs://bucket/static.png"},
			{CutIndex: 4, DurationSec: 8, VisualAnchor: "a", KeyframeReference: "gs://bucket/static.png"},
			{CutIndex: 5, DurationSec: 8, VisualAnchor: "a", KeyframeReference: "gs://bucket/static.png"},
		},
	}
	task := &domain.Task{JobID: "job-1", Command: domain.CommandMVFromKeyframeVideoRecipe, VideoRecipe: recipe}
	vp := &recordingVideoProcessor{}
	flt := VideoGenerationFilter{Runner: indexedURLRunner{}, UsePreviousVideo: true, VideoProcessor: vp}

	err := flt.Execute(context.Background(), &Context{
		Task:        task,
		VideoRecipe: recipe,
		OutputPath:  "gs://bucket/jobs/job-1/",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// 8,7,7,7,8(reset): cumulative 8->15->22->29, then 29+7>29 so cut5 resets.
	// veoContinuationMaxDurationSec=29 caps each chain at 3 video_extension continuations,
	// matching Veo's real ~30s API ceiling (color drift is handled separately by
	// colorCorrectExtensionCut, not by resetting more often).
	wantChainStart := []bool{true, false, false, false, true}
	for i, want := range wantChainStart {
		if recipe.Cuts[i].IsChainStart != want {
			t.Errorf("cut[%d] IsChainStart = %v, want %v", i, recipe.Cuts[i].IsChainStart, want)
		}
	}

	if len(vp.extractCalls) != 1 {
		t.Fatalf("ExtractLastFrame calls = %d, want 1 (only for the mid-job reset)", len(vp.extractCalls))
	}
	// cut4 is a video_extension continuation, so it goes through colorCorrectExtensionCut
	// before becoming lastVideoID — the reset's frame extraction must read from that
	// corrected video, not the raw (possibly color-drifted) Veo output.
	if vp.extractCalls[0].videoURI != "gs://bucket/jobs/job-1/videos/cut_04_color_corrected.mp4" {
		t.Errorf("ExtractLastFrame videoURI = %q, want previous cut's color-corrected video URL", vp.extractCalls[0].videoURI)
	}
	if vp.extractCalls[0].destURI != "gs://bucket/jobs/job-1/images/chain_frame_cut_05.png" {
		t.Errorf("ExtractLastFrame destURI = %q", vp.extractCalls[0].destURI)
	}
	if recipe.Cuts[4].KeyframeReference != vp.extractCalls[0].destURI {
		t.Errorf("cut[4] KeyframeReference = %q, want extracted frame URI %q", recipe.Cuts[4].KeyframeReference, vp.extractCalls[0].destURI)
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
			{CutIndex: 1, DurationSec: 8, VisualAnchor: "a", CharacterID: "tsumugi", KeyframeReference: "gs://bucket/keyframes/scene.png"},
			{CutIndex: 2, DurationSec: 8, VisualAnchor: "a", CharacterID: "tsumugi", KeyframeReference: "gs://bucket/keyframes/scene.png"},
			{CutIndex: 3, DurationSec: 8, VisualAnchor: "a", CharacterID: "tsumugi", KeyframeReference: "gs://bucket/keyframes/scene.png"},
		},
	}
	task := &domain.Task{JobID: "job-1", Command: domain.CommandMVFromKeyframeVideoRecipe, VideoRecipe: recipe}
	vp := &recordingVideoProcessor{}
	flt := VideoGenerationFilter{Runner: indexedURLRunner{}, UsePreviousVideo: true, VideoProcessor: vp}

	err := flt.Execute(context.Background(), &Context{
		Task:        task,
		VideoRecipe: recipe,
		OutputPath:  "gs://bucket/jobs/job-1/",
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
			{CutIndex: 1, StartSec: 0, DurationSec: 8, VisualAnchor: "verse", KeyframeReference: "gs://bucket/verse.png"},
			{CutIndex: 2, StartSec: 8, DurationSec: 8, VisualAnchor: "verse", KeyframeReference: "gs://bucket/verse.png"},
			{CutIndex: 3, StartSec: 16, DurationSec: 8, VisualAnchor: "chorus", KeyframeReference: "gs://bucket/chorus.png"},
			{CutIndex: 4, StartSec: 24, DurationSec: 8, VisualAnchor: "chorus", KeyframeReference: "gs://bucket/chorus.png"},
		},
	}
	task := &domain.Task{JobID: "job-1", Command: domain.CommandMVFromKeyframeVideoRecipe, VideoRecipe: recipe}
	vp := &recordingVideoProcessor{}
	flt := VideoGenerationFilter{Runner: indexedURLRunner{}, UsePreviousVideo: true, VideoProcessor: vp}

	err := flt.Execute(context.Background(), &Context{
		Task:        task,
		VideoRecipe: recipe,
		OutputPath:  "gs://bucket/jobs/job-1/",
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

// TestVideoPromptAppendsModeGuidance verifies every non-empty video prompt carries the
// mode-specific guidance loaded from assets/prompts/video_gen/ — accurate premises per Veo
// feature (starting keyframe for image_to_video, design-sheet handling for reference_to_video,
// seamless continuation for video_extension, and the shared no-burned-in-text rule) — and that
// an empty anchor+cue still yields an empty prompt so request validation keeps rejecting
// broken recipes. Failing lookups here also catch missing/renamed prompt asset files.
func TestVideoPromptAppendsModeGuidance(t *testing.T) {
	cut := orchestrator.Cut{VisualAnchor: "anchor scene", AudioCue: "bass drop at 0:10"}
	prefix := "anchor scene\nSynchronize motion and camera timing with audio cue: bass drop at 0:10\n"

	tests := []struct {
		mode veoGenerationMode
		want []string
	}{
		{veoModeImageToVideo, []string{"starting keyframe image", "exactly one character", "gradually and continuously", "No text"}},
		{veoModeReferenceToVideo, []string{"reference images", "design sheet", "never depict multiple people", "gradually and continuously", "No text"}},
		{veoModeVideoExtension, []string{"Continue seamlessly", "previous video clip", "no hard cut", "pose, or gesture", "No text"}},
	}
	for _, tt := range tests {
		got := videoPrompt(cut, tt.mode)
		if !strings.HasPrefix(got, prefix) {
			t.Errorf("videoPrompt(%s) = %q, want prefix %q", tt.mode, got, prefix)
		}
		guidance := strings.TrimPrefix(got, prefix)
		if guidance == "" {
			t.Errorf("videoPrompt(%s) has no guidance (prompt asset missing?)", tt.mode)
		}
		for _, want := range tt.want {
			if !strings.Contains(guidance, want) {
				t.Errorf("videoPrompt(%s) guidance missing %q:\n%s", tt.mode, want, guidance)
			}
		}
	}

	if got := videoPrompt(orchestrator.Cut{}, veoModeImageToVideo); got != "" {
		t.Errorf("videoPrompt(empty) = %q, want empty (keeps validation failing on broken recipes)", got)
	}
}

// TestVideoGenModeForMirrorsAdapterBranches verifies mode selection matches
// adapters.VertexVeoRunner.buildGenerateBody: video context (usePreviousVideo + gs:// previous
// video) wins and drops image references; otherwise referenceImages with a supporting runner
// means reference_to_video; everything else (no refs, unsupported model, non-gs:// video ID,
// or a runner without the optional interface) is image_to_video.
func TestVideoGenModeForMirrorsAdapterBranches(t *testing.T) {
	refs := []string{"gs://bucket/char.png", "gs://bucket/keyframe.png"}
	supporting := &durationCaptureRunner{supportsReferenceImages: true}
	nonSupporting := &durationCaptureRunner{supportsReferenceImages: false}
	plain := sequenceRunner{} // does not implement ports.ReferenceImagesSupporter

	tests := []struct {
		name             string
		usePreviousVideo bool
		lastVideoID      string
		refs             []string
		runner           ports.VideoRunner
		want             veoGenerationMode
	}{
		{"video context wins over refs", true, "gs://bucket/prev.mp4", refs, supporting, veoModeVideoExtension},
		{"previous video disabled", false, "gs://bucket/prev.mp4", refs, supporting, veoModeReferenceToVideo},
		{"non-gs previous video id", true, "operation-id-123", refs, supporting, veoModeReferenceToVideo},
		{"refs with supporting runner", false, "", refs, supporting, veoModeReferenceToVideo},
		{"refs with non-supporting model", false, "", refs, nonSupporting, veoModeImageToVideo},
		{"refs with plain runner", false, "", refs, plain, veoModeImageToVideo},
		{"no refs", false, "", nil, supporting, veoModeImageToVideo},
	}
	for _, tt := range tests {
		if got := videoGenModeFor(tt.usePreviousVideo, tt.lastVideoID, tt.refs, tt.runner); got != tt.want {
			t.Errorf("%s: videoGenModeFor() = %s, want %s", tt.name, got, tt.want)
		}
	}
}

// TestVideoSeedUsesCharacterSeed verifies Veo generation always receives the cut's character
// seed (the same value keyframe image generation uses, per go-veo-orchestrator's
// keyframe.Generator), regardless of any legacy recipe-level seed that might still be present
// on an already-decoded recipe. Without any seed, independent chain-start generations drift in
// appearance (costume/eye color), so the character seed is reused as the video seed. Recipe-level
// seed (MusicRecipe.Seed) is a removed legacy migration path (see domain.applyLegacyVideoRecipeFields)
// that this filter deliberately no longer reads, since honoring it caused keyframe generation
// (always character-seeded) and video generation to silently diverge on legacy-migrated recipes.
func TestVideoSeedUsesCharacterSeed(t *testing.T) {
	charSeed := int64(208222065)
	characters := &characterkit.Characters{
		ByID: map[string]*characterkit.Character{
			"tsumugi": {ID: "tsumugi", Seed: &charSeed},
		},
	}

	recipeSeed := int64(777)
	recipe := &orchestrator.VideoRecipe{
		MusicRecipe: orchestrator.MusicRecipe{Title: "test"},
		Cuts: []orchestrator.Cut{
			{CutIndex: 1, DurationSec: 8, VisualAnchor: "a", CharacterID: "tsumugi"},
		},
	}
	// Seed is a field promoted from the embedded lyria.AIModels struct, so it cannot be set
	// in the MusicRecipe struct literal above. Setting it here simulates a recipe carrying a
	// stale legacy seed value that must now be ignored in favor of the character seed.
	recipe.MusicRecipe.Seed = &recipeSeed
	task := &domain.Task{JobID: "job-1", Command: domain.CommandMVFromKeyframeVideoRecipe, VideoRecipe: recipe}
	runner := &seedCaptureRunner{}
	flt := VideoGenerationFilter{Runner: runner}

	err := flt.Execute(context.Background(), &Context{
		Task:        task,
		VideoRecipe: recipe,
		Characters:  characters,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(runner.seeds) != 1 || runner.seeds[0] != charSeed {
		t.Errorf("seeds = %v, want [%d] (character seed, ignoring stale recipe-level seed)", runner.seeds, charSeed)
	}
}

// TestVideoGenerationFilterForcesEightSecondsForReferenceToVideo reproduces job
// short-20260711-225010-d12ddd0479ed's failure: a cut whose character has reference art was
// submitted to Veo's reference_to_video (referenceImages) with a snapped image_to_video duration
// (6s), which Vertex AI rejects with "Unsupported output video duration 6 seconds, supported
// durations are [8] for feature reference_to_video". A cut naturally 6s long (from the recipe's
// section timing) must instead be forced to Veo's only reference_to_video duration, 8s.
func TestVideoGenerationFilterForcesEightSecondsForReferenceToVideo(t *testing.T) {
	characters := &characterkit.Characters{
		ByID: map[string]*characterkit.Character{
			"tsumugi": {ID: "tsumugi", ReferenceURL: "gs://bucket/characters/tsumugi.png"},
		},
	}
	recipe := &orchestrator.VideoRecipe{
		MusicRecipe: orchestrator.MusicRecipe{Title: "test"},
		Cuts: []orchestrator.Cut{
			{CutIndex: 5, StartSec: 30, DurationSec: 6, VisualAnchor: "a", CharacterID: "tsumugi"},
		},
	}
	task := &domain.Task{JobID: "job-1", Command: domain.CommandMVFromKeyframeVideoRecipe, VideoRecipe: recipe}
	runner := &durationCaptureRunner{supportsReferenceImages: true}
	flt := VideoGenerationFilter{Runner: runner}

	if err := flt.Execute(context.Background(), &Context{
		Task:        task,
		VideoRecipe: recipe,
		Characters:  characters,
	}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(runner.durations) != 1 || runner.durations[0] != 8 {
		t.Errorf("durations = %v, want [8] (reference_to_video only supports 8s)", runner.durations)
	}

	// A character without reference art, or a runner that doesn't support referenceImages,
	// must keep the ordinary image_to_video snapping ({4,6,8}) and leave 6s untouched.
	recipe2 := &orchestrator.VideoRecipe{
		MusicRecipe: orchestrator.MusicRecipe{Title: "test"},
		Cuts: []orchestrator.Cut{
			{CutIndex: 5, StartSec: 30, DurationSec: 6, VisualAnchor: "a"},
		},
	}
	task2 := &domain.Task{JobID: "job-2", Command: domain.CommandMVFromKeyframeVideoRecipe, VideoRecipe: recipe2}
	runner2 := &durationCaptureRunner{supportsReferenceImages: true}
	flt2 := VideoGenerationFilter{Runner: runner2}

	if err := flt2.Execute(context.Background(), &Context{
		Task:        task2,
		VideoRecipe: recipe2,
		Characters:  characters,
	}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(runner2.durations) != 1 || runner2.durations[0] != 6 {
		t.Errorf("durations = %v, want [6] (no reference art, image_to_video keeps 6s)", runner2.durations)
	}
}

type durationCaptureRunner struct {
	supportsReferenceImages bool
	durations               []float64
}

// Run records each request's duration and returns a fixed successful response.
func (r *durationCaptureRunner) Run(_ context.Context, req ports.VideoGenerationRequest) (*ports.VideoResponse, error) {
	r.durations = append(r.durations, req.DurationSec)
	return &ports.VideoResponse{
		CloudURL: fmt.Sprintf("gs://bucket/cut_%d.mp4", req.CutIndex),
		VideoID:  fmt.Sprintf("video-%d", req.CutIndex),
		CutIndex: req.CutIndex,
	}, nil
}

// SupportsReferenceImages implements ports.ReferenceImagesSupporter.
func (r *durationCaptureRunner) SupportsReferenceImages() bool {
	return r.supportsReferenceImages
}

type seedCaptureRunner struct {
	seeds []int64
}

// Run records each request's seed and returns a fixed successful response.
func (r *seedCaptureRunner) Run(_ context.Context, req ports.VideoGenerationRequest) (*ports.VideoResponse, error) {
	r.seeds = append(r.seeds, req.Seed)
	return &ports.VideoResponse{
		CloudURL: fmt.Sprintf("gs://bucket/cut_%d.mp4", req.CutIndex),
		VideoID:  fmt.Sprintf("video-%d", req.CutIndex),
		CutIndex: req.CutIndex,
	}, nil
}

type indexedURLRunner struct{}

// Run returns a per-cut-index video URL/ID so tests can assert exactly which cut's video
// was referenced downstream (e.g. as ExtractLastFrame's input).
func (indexedURLRunner) Run(_ context.Context, req ports.VideoGenerationRequest) (*ports.VideoResponse, error) {
	return &ports.VideoResponse{
		CloudURL: fmt.Sprintf("gs://bucket/cut_%d.mp4", req.CutIndex),
		VideoID:  fmt.Sprintf("video-%d", req.CutIndex),
		CutIndex: req.CutIndex,
	}, nil
}

type sequenceRunner struct{}

// Run starts the receiver workflow.
func (sequenceRunner) Run(_ context.Context, req ports.VideoGenerationRequest) (*ports.VideoResponse, error) {
	return &ports.VideoResponse{
		CloudURL: "gs://bucket/cut.mp4",
		VideoID:  "video-id",
		CutIndex: req.CutIndex,
	}, nil
}

type contextCaptureRunner struct {
	baseURI string
}

// Run starts the receiver workflow.
func (r *contextCaptureRunner) Run(ctx context.Context, req ports.VideoGenerationRequest) (*ports.VideoResponse, error) {
	r.baseURI, _ = ports.VideoOutputBaseURIFromContext(ctx)
	return &ports.VideoResponse{
		CloudURL: "gs://bucket/cut.mp4",
		VideoID:  "video-id",
		CutIndex: req.CutIndex,
	}, nil
}

type captureQueue struct {
	tasks []*domain.Task
}

// Enqueue adds a task to the queue.
func (q *captureQueue) Enqueue(_ context.Context, task *domain.Task) error {
	copied := *task
	q.tasks = append(q.tasks, &copied)
	return nil
}

type recordingVideoWorkflow struct {
	runCalled bool
}

func (w *recordingVideoWorkflow) Run(_ context.Context, _ *orchestrator.VideoRecipe) ([]*orchestrator.VideoResponse, error) {
	w.runCalled = true
	return nil, nil
}

func (w *recordingVideoWorkflow) RunAndSave(_ context.Context, _ *orchestrator.VideoRecipe, _ string) (*orchestrator.VideoPlotResponse, error) {
	w.runCalled = true
	return nil, nil
}
