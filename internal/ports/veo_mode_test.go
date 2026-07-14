package ports

import (
	"context"
	"testing"
)

// TestClassifyVeoRequest verifies the single source of truth for Veo request classification:
// video context (usePreviousVideo + gs:// previous video) wins and drops image references;
// otherwise referenceImages with a supporting model means reference_to_video; an image-input
// request with a last-frame reference and a supporting model means frames_to_video (Veo's
// lastFrame is only valid alongside the image start frame); everything else (no refs,
// unsupported model, non-gs:// video ID, missing start image) is image_to_video.
func TestClassifyVeoRequest(t *testing.T) {
	refs := []string{"gs://bucket/char.png", "gs://bucket/keyframe.png"}
	full := VeoCapabilities{ReferenceImages: true, LastFrame: true}
	lastFrameOnly := VeoCapabilities{LastFrame: true}
	none := VeoCapabilities{}

	tests := []struct {
		name             string
		usePreviousVideo bool
		req              VideoGenerationRequest
		caps             VeoCapabilities
		want             VeoGenerationMode
	}{
		{"video context wins over refs", true, VideoGenerationRequest{PreviousVideoID: "gs://bucket/prev.mp4", ReferenceImages: refs}, full, VeoModeVideoExtension},
		{"video context wins over last frame", true, VideoGenerationRequest{PreviousVideoID: "gs://bucket/prev.mp4", ImageReference: "gs://bucket/kf.png", LastFrameReference: "gs://bucket/next.png"}, full, VeoModeVideoExtension},
		{"previous video disabled", false, VideoGenerationRequest{PreviousVideoID: "gs://bucket/prev.mp4", ReferenceImages: refs}, full, VeoModeReferenceToVideo},
		{"non-gs previous video id", true, VideoGenerationRequest{PreviousVideoID: "operation-id-123", ReferenceImages: refs}, full, VeoModeReferenceToVideo},
		{"refs with supporting model", false, VideoGenerationRequest{ReferenceImages: refs}, full, VeoModeReferenceToVideo},
		{"refs win over last frame", false, VideoGenerationRequest{ReferenceImages: refs, ImageReference: "gs://bucket/kf.png", LastFrameReference: "gs://bucket/next.png"}, full, VeoModeReferenceToVideo},
		{"blank-only refs ignored", false, VideoGenerationRequest{ReferenceImages: []string{" ", ""}}, full, VeoModeImageToVideo},
		{"refs without model support", false, VideoGenerationRequest{ReferenceImages: refs}, none, VeoModeImageToVideo},
		{"no refs", false, VideoGenerationRequest{}, full, VeoModeImageToVideo},
		{"last frame with image reference", false, VideoGenerationRequest{ImageReference: "gs://bucket/kf.png", LastFrameReference: "gs://bucket/next.png"}, lastFrameOnly, VeoModeFramesToVideo},
		{"last frame with inline image", false, VideoGenerationRequest{InputImage: []byte{0x89}, LastFrameReference: "gs://bucket/next.png"}, lastFrameOnly, VeoModeFramesToVideo},
		{"last frame on ref fallback", false, VideoGenerationRequest{ReferenceImages: refs, ImageReference: "gs://bucket/kf.png", LastFrameReference: "gs://bucket/next.png"}, lastFrameOnly, VeoModeFramesToVideo},
		{"last frame without start image", false, VideoGenerationRequest{LastFrameReference: "gs://bucket/next.png"}, lastFrameOnly, VeoModeImageToVideo},
		{"last frame without model support", false, VideoGenerationRequest{ImageReference: "gs://bucket/kf.png", LastFrameReference: "gs://bucket/next.png"}, none, VeoModeImageToVideo},
	}
	for _, tt := range tests {
		if got := ClassifyVeoRequest(tt.req, tt.usePreviousVideo, tt.caps); got != tt.want {
			t.Errorf("%s: ClassifyVeoRequest() = %s, want %s", tt.name, got, tt.want)
		}
	}
}

type capsFakeRunner struct {
	refs      bool
	lastFrame bool
}

func (r capsFakeRunner) Run(context.Context, VideoGenerationRequest) (*VideoResponse, error) {
	return nil, nil
}
func (r capsFakeRunner) SupportsReferenceImages() bool { return r.refs }
func (r capsFakeRunner) SupportsLastFrame() bool       { return r.lastFrame }

type plainFakeRunner struct{}

func (plainFakeRunner) Run(context.Context, VideoGenerationRequest) (*VideoResponse, error) {
	return nil, nil
}

// TestRunnerCapabilities verifies capability derivation from a runner's optional interfaces,
// and that runners without them (test doubles) report no optional support.
func TestRunnerCapabilities(t *testing.T) {
	got := RunnerCapabilities(capsFakeRunner{refs: true, lastFrame: false})
	if !got.ReferenceImages || got.LastFrame {
		t.Errorf("RunnerCapabilities(refs only) = %+v", got)
	}
	got = RunnerCapabilities(capsFakeRunner{refs: false, lastFrame: true})
	if got.ReferenceImages || !got.LastFrame {
		t.Errorf("RunnerCapabilities(lastFrame only) = %+v", got)
	}
	got = RunnerCapabilities(plainFakeRunner{})
	if got.ReferenceImages || got.LastFrame {
		t.Errorf("RunnerCapabilities(plain) = %+v, want none", got)
	}
}
