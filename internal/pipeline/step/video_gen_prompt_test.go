package step

import (
	"context"
	"strings"
	"testing"

	"github.com/shouni/go-veo-orchestrator/video"

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
	cut := video.Cut{VisualAnchor: "anchor scene", AudioCue: "bass drop at 0:10"}
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

	if got := videoPrompt(video.Cut{}, ports.VeoModeImageToVideo); got != "" {
		t.Errorf("videoPrompt(empty) = %q, want empty (keeps validation failing on broken recipes)", got)
	}
}

// TestRunDirectPassesNextKeyframeAsLastFrame verifies the end-to-end wiring: on a runner that
// supports lastFrame but not referenceImages (e.g. veo-3.1-fast), each image-input cut receives
// the next same-section/same-character cut's keyframe as LastFrameReference, and the final cut
// receives none. The prompt must carry the frames_to_video guidance for exactly those requests.
func TestRunDirectPassesNextKeyframeAsLastFrame(t *testing.T) {
	recipe := &video.Recipe{
		MusicRecipe: video.MusicRecipe{Title: "test"},
		Cuts: []video.Cut{
			{CutIndex: 0, CharacterID: "zunda", VisualAnchor: "a", DurationSec: 8, KeyframeReference: "gs://bucket/kf0.png"},
			{CutIndex: 1, CharacterID: "zunda", VisualAnchor: "b", DurationSec: 8, KeyframeReference: "gs://bucket/kf1.png"},
		},
	}
	task := &domain.Task{JobID: "job-1", Command: domain.CommandMVFromKeyframeVideoRecipe, VideoRecipe: recipe}
	runner := &durationCaptureRunner{supportsReferenceImages: false, supportsLastFrame: true}
	st := VideoGenerationStep{Runner: runner}

	if err := st.Execute(context.Background(), &Context{Task: task, VideoRecipe: recipe}); err != nil {
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
