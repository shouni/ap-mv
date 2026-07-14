package filter

import (
	"context"
	"strings"
	"testing"

	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"github.com/shouni/ap-mv/internal/domain"
	"github.com/shouni/ap-mv/internal/ports"
)

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
		mode ports.VeoGenerationMode
		want []string
	}{
		{ports.VeoModeImageToVideo, []string{"starting keyframe image", "exactly one character", "gradually and continuously", "No text"}},
		{ports.VeoModeFramesToVideo, []string{"starting keyframe image", "final frame image", "exactly one character", "gradually and continuously", "No text"}},
		{ports.VeoModeReferenceToVideo, []string{"reference images", "design sheet", "never depict multiple people", "gradually and continuously", "No text"}},
		{ports.VeoModeVideoExtension, []string{"Continue seamlessly", "previous video clip", "no hard cut", "pose, or gesture", "No text"}},
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

	if got := videoPrompt(orchestrator.Cut{}, ports.VeoModeImageToVideo); got != "" {
		t.Errorf("videoPrompt(empty) = %q, want empty (keeps validation failing on broken recipes)", got)
	}
}

// TestNextCutLastFrameReference verifies the guards for choosing the next cut's keyframe as
// the current cut's ending frame: it must exist, be non-empty, stay within the same section
// (a section boundary is an intentional scene change), match the current cut's character
// (interpolating across characters would morph them), and differ from the current cut's own
// keyframe (duration-split cuts share one keyframe; forcing end == start kills motion).
func TestNextCutLastFrameReference(t *testing.T) {
	cuts := []orchestrator.Cut{
		{CutIndex: 0, CharacterID: "zunda", KeyframeReference: "gs://bucket/kf0.png"},
		{CutIndex: 1, CharacterID: "zunda", KeyframeReference: "gs://bucket/kf1.png"},
		{CutIndex: 2, CharacterID: "metan", KeyframeReference: "gs://bucket/kf2.png"},
		{CutIndex: 3, CharacterID: "metan", KeyframeReference: "gs://bucket/kf3.png", IsSectionStart: true},
		{CutIndex: 4, CharacterID: "metan", KeyframeReference: "gs://bucket/kf3.png"},
		{CutIndex: 5, CharacterID: "metan", KeyframeReference: ""},
	}

	tests := []struct {
		name string
		i    int
		want string
	}{
		{"same character and section", 0, "gs://bucket/kf1.png"},
		{"different character", 1, ""},
		{"next is section start", 2, ""},
		{"next shares the same keyframe", 3, ""},
		{"next keyframe empty", 4, ""},
		{"last cut", 5, ""},
	}
	for _, tt := range tests {
		if got := nextCutLastFrameReference(cuts, tt.i); got != tt.want {
			t.Errorf("%s: nextCutLastFrameReference(cuts, %d) = %q, want %q", tt.name, tt.i, got, tt.want)
		}
	}
}

// TestRunDirectPassesNextKeyframeAsLastFrame verifies the end-to-end wiring: on a runner that
// supports lastFrame but not referenceImages (e.g. veo-3.1-fast), each image-input cut receives
// the next same-section/same-character cut's keyframe as LastFrameReference, and the final cut
// receives none. The prompt must carry the frames_to_video guidance for exactly those requests.
func TestRunDirectPassesNextKeyframeAsLastFrame(t *testing.T) {
	recipe := &orchestrator.VideoRecipe{
		MusicRecipe: orchestrator.MusicRecipe{Title: "test"},
		Cuts: []orchestrator.Cut{
			{CutIndex: 0, CharacterID: "zunda", KeyframeReference: "gs://bucket/kf0.png", DurationSec: 8, VisualAnchor: "a"},
			{CutIndex: 1, CharacterID: "zunda", KeyframeReference: "gs://bucket/kf1.png", DurationSec: 8, VisualAnchor: "b"},
		},
	}
	task := &domain.Task{JobID: "job-1", Command: domain.CommandMVFromKeyframeVideoRecipe, VideoRecipe: recipe}
	runner := &durationCaptureRunner{supportsReferenceImages: false, supportsLastFrame: true}
	flt := VideoGenerationFilter{Runner: runner}

	if err := flt.Execute(context.Background(), &Context{State: State{Task: task, VideoRecipe: recipe}}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(runner.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(runner.requests))
	}
	if got := runner.requests[0].LastFrameReference; got != "gs://bucket/kf1.png" {
		t.Errorf("cut 0 LastFrameReference = %q, want next cut's keyframe", got)
	}
	if !strings.Contains(runner.requests[0].Prompt, "final frame image") {
		t.Errorf("cut 0 prompt missing frames_to_video guidance:\n%s", runner.requests[0].Prompt)
	}
	if got := runner.requests[1].LastFrameReference; got != "" {
		t.Errorf("cut 1 LastFrameReference = %q, want empty (no next cut)", got)
	}
	if strings.Contains(runner.requests[1].Prompt, "final frame image") {
		t.Errorf("cut 1 prompt must not carry frames_to_video guidance:\n%s", runner.requests[1].Prompt)
	}
}
