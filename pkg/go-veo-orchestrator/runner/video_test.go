package runner

import (
	"context"
	"fmt"
	"strings"
	"testing"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	"github.com/shouni/go-veo-orchestrator/ports"
)

type mockCutKeyframeRunner struct {
	images []*imagePorts.ImageResponse
}

func (m *mockCutKeyframeRunner) Run(ctx context.Context, recipe *ports.VideoRecipe) ([]*imagePorts.ImageResponse, error) {
	return m.images, nil
}

func (m *mockCutKeyframeRunner) RunAndSave(ctx context.Context, recipe *ports.VideoRecipe, outputPath string) (*ports.VideoRecipe, error) {
	return recipe, nil
}

type mockVideoRunner struct {
	requests []ports.VideoGenerationRequest
}

func (m *mockVideoRunner) Run(ctx context.Context, req ports.VideoGenerationRequest) (*ports.VideoResponse, error) {
	m.requests = append(m.requests, req)
	return &ports.VideoResponse{
		CloudURL: fmt.Sprintf("gs://videos/cut_%d.mp4", req.CutIndex),
		VideoID:  fmt.Sprintf("video-%d", req.CutIndex),
		CutIndex: req.CutIndex,
	}, nil
}

func TestVideoTimelineRunner_RunChainsPreviousVideoID(t *testing.T) {
	ctx := context.Background()
	recipe := &ports.VideoRecipe{
		ProjectTitle: "test",
		MusicRecipe:  ports.MusicRecipe{Style: "symphonic rock"},
		Cuts: []ports.Cut{
			{
				CutIndex:          1,
				DurationSec:       5,
				AudioCue:          "intro pad",
				VisualAnchor:      "slow dolly in",
				KeyframeReference: "gs://images/cut_1.png",
				AudioReference:    "gs://audio/seg_1.mp3",
			},
			{
				CutIndex:          2,
				DurationSec:       5,
				AudioCue:          "heavy chorus",
				VisualAnchor:      "fast orbit camera",
				KeyframeReference: "gs://images/cut_2.png",
			},
		},
	}
	keyframes := &mockCutKeyframeRunner{
		images: []*imagePorts.ImageResponse{
			{Data: []byte("image-1"), MimeType: "image/png", UsedSeed: 101},
			{Data: []byte("image-2"), MimeType: "image/png", UsedSeed: 102},
		},
	}
	video := &mockVideoRunner{}
	runner := NewVideoTimelineRunner(keyframes, video, nil)

	res, err := runner.Run(ctx, recipe)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("Expected 2 video responses, got %d", len(res))
	}
	if len(video.requests) != 2 {
		t.Fatalf("Expected 2 video requests, got %d", len(video.requests))
	}
	if video.requests[0].PreviousVideoID != "" {
		t.Errorf("Expected first request to have no previous video ID, got %q", video.requests[0].PreviousVideoID)
	}
	if video.requests[1].PreviousVideoID != "video-1" {
		t.Errorf("Expected second request to chain video-1, got %q", video.requests[1].PreviousVideoID)
	}
	if string(video.requests[0].InputImage) != "image-1" {
		t.Errorf("Expected first input image data, got %q", string(video.requests[0].InputImage))
	}
	if video.requests[0].ImageReference != "gs://images/cut_1.png" {
		t.Errorf("Expected first image reference, got %q", video.requests[0].ImageReference)
	}
	if video.requests[0].AudioReference != "gs://audio/seg_1.mp3" {
		t.Errorf("Expected first audio reference, got %q", video.requests[0].AudioReference)
	}
	if video.requests[0].Seed != 101 {
		t.Errorf("Expected seed from keyframe, got %d", video.requests[0].Seed)
	}
	if !strings.Contains(video.requests[1].Prompt, "heavy chorus") {
		t.Errorf("Expected audio cue in prompt, got %q", video.requests[1].Prompt)
	}
	if recipe.Cuts[1].VideoURL != "gs://videos/cut_2.mp4" {
		t.Errorf("Expected recipe to be updated with video URL, got %q", recipe.Cuts[1].VideoURL)
	}
	if recipe.Cuts[1].VideoID != "video-2" {
		t.Errorf("Expected recipe to be updated with video ID, got %q", recipe.Cuts[1].VideoID)
	}
}

func TestVideoTimelineRunner_RunSkipsGeneratedCutAndChainsItsVideoID(t *testing.T) {
	ctx := context.Background()
	recipe := &ports.VideoRecipe{
		ProjectTitle: "resume",
		Cuts: []ports.Cut{
			{
				CutIndex:     1,
				DurationSec:  5,
				VisualAnchor: "resume from existing context",
				VideoID:      "existing-video-1",
				VideoURL:     "gs://videos/cut_1.mp4",
				Status:       ports.CutStatusGenerated,
			},
			{
				CutIndex:     2,
				DurationSec:  5,
				VisualAnchor: "continue from existing context",
			},
		},
	}
	keyframes := &mockCutKeyframeRunner{
		images: []*imagePorts.ImageResponse{
			{Data: []byte("image-1"), MimeType: "image/png", UsedSeed: 101},
			{Data: []byte("image-2"), MimeType: "image/png", UsedSeed: 102},
		},
	}
	video := &mockVideoRunner{}
	runner := NewVideoTimelineRunner(keyframes, video, nil)

	_, err := runner.Run(ctx, recipe)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if len(video.requests) != 1 {
		t.Fatalf("Expected only pending cut to be requested, got %d", len(video.requests))
	}
	if video.requests[0].PreviousVideoID != "existing-video-1" {
		t.Errorf("Expected generated cut video ID as previous context, got %q", video.requests[0].PreviousVideoID)
	}
}
