package adapters

import (
	"context"
	"testing"

	"github.com/shouni/go-gemini-client/gemini"

	"github.com/shouni/ap-mv/internal/ports"
)

func baseRequest() ports.VideoGenerationRequest {
	return ports.VideoGenerationRequest{Prompt: "scene", CutIndex: 1, DurationSec: 8}
}

// TestBuildRequestIncludesAudioReference verifies that an audio reference reaches
// instances[0].audio in the assembled request body.
//
// The SDK has no typed field for a Veo audio input, so it is injected through
// ModifyRequestBody. This test runs the hook against a body shaped like the one the SDK
// builds, because that injection is the whole mechanism — if it silently no-ops, the audio
// just never gets sent and nothing else fails.
func TestBuildRequestIncludesAudioReference(t *testing.T) {
	runner := &VertexVeoRunner{outputStorageURI: "gs://bucket/out/"}
	req := baseRequest()
	req.AudioReference = "gs://bucket/music.mp3"

	built := runner.buildRequest(context.Background(), req)
	if built.ModifyRequestBody == nil {
		t.Fatal("ModifyRequestBody = nil, want the audio injection hook")
	}

	body := built.ModifyRequestBody(map[string]any{
		"instances": []any{map[string]any{"prompt": "scene"}},
	})
	instance := body["instances"].([]any)[0].(map[string]any)
	audio, ok := instance["audio"].(map[string]any)
	if !ok {
		t.Fatalf("instances[0].audio = %v, want a map", instance["audio"])
	}
	if got := audio["gcsUri"]; got != "gs://bucket/music.mp3" {
		t.Fatalf("audio.gcsUri = %v", got)
	}
	if got := audio["mimeType"]; got != "audio/mpeg" {
		t.Fatalf("audio.mimeType = %v", got)
	}
	// 元のフィールドを壊していないこと（ExtraBody を使うと instances ごと消える）。
	if got := instance["prompt"]; got != "scene" {
		t.Fatalf("instances[0].prompt = %v, want the SDK-built value preserved", got)
	}
}

// TestBuildRequestOmitsAudioHookWhenUnset verifies that requests without audio carry no
// body-rewriting hook at all, so the assembled body is exactly what the SDK produced.
func TestBuildRequestOmitsAudioHookWhenUnset(t *testing.T) {
	runner := &VertexVeoRunner{outputStorageURI: "gs://bucket/out/"}
	if built := runner.buildRequest(context.Background(), baseRequest()); built.ModifyRequestBody != nil {
		t.Fatal("ModifyRequestBody should be nil when the request has no audio")
	}
}

// TestBuildRequestReferenceImages verifies referenceImages handling: supported models send
// asset references, unsupported models fall back to the image input, and video-to-video
// context excludes both.
func TestBuildRequestReferenceImages(t *testing.T) {
	req := baseRequest()
	req.ImageReference = "gs://bucket/kf.png"
	req.ReferenceImages = []string{"gs://bucket/characters/zundamon.png", "gs://bucket/kf.png"}
	req.PreviousVideoID = "gs://bucket/prev.mp4"

	// 対応モデル: referenceImages を送り、image は送らない。
	runner := &VertexVeoRunner{model: "veo-3.1-generate-001", outputStorageURI: "gs://bucket/out/"}
	built := runner.buildRequest(context.Background(), req)
	if len(built.References) != 2 {
		t.Fatalf("References = %v, want 2 asset references", built.References)
	}
	if built.References[0].Type != gemini.VideoReferenceAsset {
		t.Fatalf("References[0].Type = %v, want asset", built.References[0].Type)
	}
	if built.References[0].Image.URI != "gs://bucket/characters/zundamon.png" {
		t.Fatalf("References[0].Image.URI = %q", built.References[0].Image.URI)
	}
	if built.Image != nil {
		t.Fatalf("Image = %+v, must not be sent together with References", built.Image)
	}

	// 非対応モデル (Fast): image 入力へフォールバック。
	runner = &VertexVeoRunner{model: "veo-3.1-fast-generate-001", outputStorageURI: "gs://bucket/out/"}
	built = runner.buildRequest(context.Background(), req)
	if len(built.References) != 0 {
		t.Fatalf("References = %v, want none for a model without support", built.References)
	}
	if built.Image == nil || built.Image.URI != "gs://bucket/kf.png" {
		t.Fatalf("Image = %+v, want the keyframe", built.Image)
	}

	// video-to-video 文脈: 画像入力は一切送らない。
	runner = &VertexVeoRunner{model: "veo-3.1-generate-001", outputStorageURI: "gs://bucket/out/", usePreviousVideo: true}
	built = runner.buildRequest(context.Background(), req)
	if built.Video == nil || built.Video.URI != "gs://bucket/prev.mp4" {
		t.Fatalf("Video = %+v, want the previous clip", built.Video)
	}
	if built.Image != nil || len(built.References) != 0 {
		t.Fatalf("image inputs = %+v / %v, want none for video extension", built.Image, built.References)
	}
}

// TestBuildRequestLastFrame verifies that a last-frame reference is only paired with the
// start image on models that support the interpolation.
func TestBuildRequestLastFrame(t *testing.T) {
	req := baseRequest()
	req.ImageReference = "gs://bucket/cut_01.png"
	req.LastFrameReference = "gs://bucket/cut_02.png"

	// 対応モデル: image と lastFrame を両方送る。
	runner := &VertexVeoRunner{model: "veo-3.1-generate-001", outputStorageURI: "gs://bucket/out/"}
	built := runner.buildRequest(context.Background(), req)
	if built.Image == nil || built.Image.URI != "gs://bucket/cut_01.png" {
		t.Fatalf("Image = %+v", built.Image)
	}
	if built.LastFrame == nil || built.LastFrame.URI != "gs://bucket/cut_02.png" {
		t.Fatalf("LastFrame = %+v", built.LastFrame)
	}

	// 非対応モデル (Veo 3.0): lastFrame は送らない。
	runner = &VertexVeoRunner{model: "veo-3.0-generate-001", outputStorageURI: "gs://bucket/out/"}
	built = runner.buildRequest(context.Background(), req)
	if built.LastFrame != nil {
		t.Fatalf("LastFrame = %+v, want none for a model without support", built.LastFrame)
	}
	if built.Image == nil {
		t.Fatal("Image should still be sent for image_to_video")
	}
}

// TestBuildRequestGenerationParameters verifies that the per-cut generation settings reach
// the request: duration is rounded to whole seconds, the seed is passed only when set, and
// the aspect ratio and audio flag come from the runner.
func TestBuildRequestGenerationParameters(t *testing.T) {
	runner := &VertexVeoRunner{model: "veo-3.1-generate-001", aspectRatio: "9:16", generateAudio: true, outputStorageURI: "gs://bucket/out/"}
	req := baseRequest()
	req.DurationSec = 7.6
	req.Seed = 4242

	built := runner.buildRequest(context.Background(), req)
	if built.DurationSec != 8 {
		t.Errorf("DurationSec = %d, want 8 (rounded)", built.DurationSec)
	}
	if built.Seed == nil || *built.Seed != 4242 {
		t.Errorf("Seed = %v, want 4242", built.Seed)
	}
	if built.AspectRatio != "9:16" {
		t.Errorf("AspectRatio = %q", built.AspectRatio)
	}
	if built.GenerateAudio == nil || !*built.GenerateAudio {
		t.Errorf("GenerateAudio = %v, want true", built.GenerateAudio)
	}

	// シード未指定は「省略」であって 0 ではない。
	req.Seed = 0
	if built := runner.buildRequest(context.Background(), req); built.Seed != nil {
		t.Errorf("Seed = %v, want nil when unset", built.Seed)
	}
}

func TestVertexVeoRunnerSupportsCapabilities(t *testing.T) {
	tests := []struct {
		model         string
		wantRefImages bool
		wantLastFrame bool
	}{
		{"veo-3.1-generate-001", true, true},
		{"veo-3.1-fast-generate-001", false, true},
		{"veo-3.0-generate-001", true, false},
		{"veo-2.0-generate-001", false, true},
	}
	for _, tt := range tests {
		runner := &VertexVeoRunner{model: tt.model}
		if got := runner.SupportsReferenceImages(); got != tt.wantRefImages {
			t.Errorf("%s: SupportsReferenceImages() = %v, want %v", tt.model, got, tt.wantRefImages)
		}
		if got := runner.SupportsLastFrame(); got != tt.wantLastFrame {
			t.Errorf("%s: SupportsLastFrame() = %v, want %v", tt.model, got, tt.wantLastFrame)
		}
	}
}

func TestVertexVeoRunnerWithVideoOptionsDerivesRunner(t *testing.T) {
	base := &VertexVeoRunner{model: "veo-3.1-generate-001", aspectRatio: "16:9"}

	if got := base.WithVideoOptions("", ""); got != ports.VideoRunner(base) {
		t.Fatalf("WithVideoOptions with empty values should return the same runner")
	}
	if got := base.WithVideoOptions("veo-3.1-generate-001", "16:9"); got != ports.VideoRunner(base) {
		t.Fatalf("WithVideoOptions with identical values should return the same runner")
	}

	derived, ok := base.WithVideoOptions("veo-3.1-fast-generate-001", "9:16").(*VertexVeoRunner)
	if !ok {
		t.Fatalf("derived runner is not a *VertexVeoRunner")
	}
	if derived.model != "veo-3.1-fast-generate-001" || derived.aspectRatio != "9:16" {
		t.Fatalf("derived = %q/%q, want overrides applied", derived.model, derived.aspectRatio)
	}
	if base.model != "veo-3.1-generate-001" || base.aspectRatio != "16:9" {
		t.Fatalf("base runner was mutated: %q/%q", base.model, base.aspectRatio)
	}

	// 片方だけの指定は、もう片方の元設定を維持する。
	partial, _ := base.WithVideoOptions("", "9:16").(*VertexVeoRunner)
	if partial.model != "veo-3.1-generate-001" || partial.aspectRatio != "9:16" {
		t.Fatalf("partial = %q/%q, want model kept and aspect overridden", partial.model, partial.aspectRatio)
	}
}

// TestBuildRequestUsesJobScopedCutStorageURI verifies that job-scoped GCS output is used for
// cut generation, so the generated file can later be copied to a stable canonical path.
func TestBuildRequestUsesJobScopedCutStorageURI(t *testing.T) {
	runner := &VertexVeoRunner{outputStorageURI: "gs://bucket/ap-mv/veo/"}
	ctx := ports.WithVideoOutputBaseURI(context.Background(), "gs://bucket/ap-mv/veo/jobs/job-1/")
	req := baseRequest()
	req.CutIndex = 2

	built := runner.buildRequest(ctx, req)
	if built.OutputGCSURI != "gs://bucket/ap-mv/veo/jobs/job-1/tmp/videos/cut_02/" {
		t.Fatalf("OutputGCSURI = %q", built.OutputGCSURI)
	}
}

// TestBuildRequestFallsBackToDefaultStorageURI verifies that Veo output falls back to the
// runner default GCS URI when the context carries no job-scoped base.
func TestBuildRequestFallsBackToDefaultStorageURI(t *testing.T) {
	runner := &VertexVeoRunner{outputStorageURI: "gs://bucket/ap-mv/veo/"}
	req := baseRequest()
	req.CutIndex = 2

	built := runner.buildRequest(context.Background(), req)
	if built.OutputGCSURI != "gs://bucket/ap-mv/veo/" {
		t.Fatalf("OutputGCSURI = %q", built.OutputGCSURI)
	}
}

func TestValidateVertexVeoRequest(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*ports.VideoGenerationRequest)
		wantErr bool
	}{
		{"valid", func(*ports.VideoGenerationRequest) {}, false},
		{"empty prompt", func(r *ports.VideoGenerationRequest) { r.Prompt = "  " }, true},
		{"negative cut index", func(r *ports.VideoGenerationRequest) { r.CutIndex = -1 }, true},
		{"zero duration", func(r *ports.VideoGenerationRequest) { r.DurationSec = 0 }, true},
		{"negative seed", func(r *ports.VideoGenerationRequest) { r.Seed = -1 }, true},
		// SDK のシードは int32 なので、それを超える値は送信前に弾く。
		{"seed beyond int32", func(r *ports.VideoGenerationRequest) { r.Seed = 1 << 40 }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := baseRequest()
			tt.mutate(&req)
			err := validateVertexVeoRequest(req)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateVertexVeoRequest() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestVertexVeoRunnerCanonicalizeGeneratedVideoCopiesToCutFile verifies that generated videos
// are copied from Veo's per-cut temporary directory to a stable canonical file, and that the
// temporary copy is cleaned up.
func TestVertexVeoRunnerCanonicalizeGeneratedVideoCopiesToCutFile(t *testing.T) {
	copier := &captureVideoCopier{}
	runner := &VertexVeoRunner{videoCopier: copier}
	ctx := ports.WithVideoOutputBaseURI(context.Background(), "gs://bucket/ap-mv/veo/jobs/job-1/")
	const source = "gs://bucket/ap-mv/veo/jobs/job-1/tmp/videos/cut_02/sample_0.mp4"
	const canonical = "gs://bucket/ap-mv/veo/jobs/job-1/videos/cut_02.mp4"

	got, err := runner.canonicalizeGeneratedVideo(ctx, ports.VideoGenerationRequest{CutIndex: 2}, source)
	if err != nil {
		t.Fatalf("canonicalizeGeneratedVideo() error = %v", err)
	}
	if got != canonical {
		t.Fatalf("uri = %q, want %q", got, canonical)
	}
	if copier.sourceURI != source || copier.targetURI != canonical {
		t.Fatalf("copy %q -> %q", copier.sourceURI, copier.targetURI)
	}
	if copier.deletedURI != source {
		t.Fatalf("deletedURI = %q", copier.deletedURI)
	}
}

// TestVertexVeoRunnerCanonicalizeGeneratedVideoWithoutJobScope verifies that generation
// outside a job scope keeps Veo's own output URI instead of copying anywhere.
func TestVertexVeoRunnerCanonicalizeGeneratedVideoWithoutJobScope(t *testing.T) {
	copier := &captureVideoCopier{}
	runner := &VertexVeoRunner{videoCopier: copier}
	const source = "gs://bucket/ap-mv/veo/sample_0.mp4"

	got, err := runner.canonicalizeGeneratedVideo(context.Background(), ports.VideoGenerationRequest{CutIndex: 2}, source)
	if err != nil {
		t.Fatalf("canonicalizeGeneratedVideo() error = %v", err)
	}
	if got != source {
		t.Fatalf("uri = %q, want the source unchanged", got)
	}
	if copier.sourceURI != "" {
		t.Fatalf("copier was called with %q, want no copy", copier.sourceURI)
	}
}

// compile-time assertion: the runner still satisfies the orchestrator's optional interfaces
// that the scene-split and video-generation filters query for model capabilities.
var (
	_ ports.VideoRunner              = (*VertexVeoRunner)(nil)
	_ ports.ReferenceImagesSupporter = (*VertexVeoRunner)(nil)
	_ ports.LastFrameSupporter       = (*VertexVeoRunner)(nil)
	_ ports.VideoRunnerConfigurator  = (*VertexVeoRunner)(nil)
)

type captureVideoCopier struct {
	sourceURI  string
	targetURI  string
	deletedURI string
}

func (c *captureVideoCopier) Copy(_ context.Context, sourceURI, targetURI string) error {
	c.sourceURI = sourceURI
	c.targetURI = targetURI
	return nil
}

func (c *captureVideoCopier) Delete(_ context.Context, uri string) error {
	c.deletedURI = uri
	return nil
}
