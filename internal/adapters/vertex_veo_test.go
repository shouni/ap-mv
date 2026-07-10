package adapters

import (
	"context"
	"testing"

	"github.com/shouni/ap-mv/internal/ports"
)

// TestVertexVeoRunnerBuildGenerateBodyIncludesAudioReference verifies that audio references are included in Veo request bodies.
func TestVertexVeoRunnerBuildGenerateBodyIncludesAudioReference(t *testing.T) {
	runner := &VertexVeoRunner{outputStorageURI: "gs://bucket/out/"}
	body := runner.buildGenerateBody(context.Background(), ports.VideoGenerationRequest{
		Prompt:         "scene",
		CutIndex:       1,
		DurationSec:    8,
		AudioReference: "gs://bucket/music.mp3",
	})

	instances := body["instances"].([]any)
	instance := instances[0].(map[string]any)
	audio := instance["audio"].(map[string]any)
	if got := audio["gcsUri"]; got != "gs://bucket/music.mp3" {
		t.Fatalf("audio.gcsUri = %v", got)
	}
	if got := audio["mimeType"]; got != "audio/mpeg" {
		t.Fatalf("audio.mimeType = %v", got)
	}
}

// TestVertexVeoRunnerBuildGenerateBodyReferenceImages verifies referenceImages handling:
// supported models send asset references, unsupported models fall back to image input,
// and video-to-video context excludes both.
func TestVertexVeoRunnerBuildGenerateBodyReferenceImages(t *testing.T) {
	req := ports.VideoGenerationRequest{
		Prompt:          "scene",
		CutIndex:        1,
		DurationSec:     8,
		ImageReference:  "gs://bucket/kf.png",
		ReferenceImages: []string{"gs://bucket/characters/zundamon.png", "gs://bucket/kf.png"},
		PreviousVideoID: "gs://bucket/prev.mp4",
	}

	// 対応モデル: referenceImages を送り、image は送らない。
	runner := &VertexVeoRunner{model: "veo-3.1-generate-001", outputStorageURI: "gs://bucket/out/"}
	instance := firstInstance(t, runner.buildGenerateBody(context.Background(), req))
	refs, ok := instance["referenceImages"].([]map[string]any)
	if !ok || len(refs) != 2 {
		t.Fatalf("referenceImages = %v, want 2 asset references", instance["referenceImages"])
	}
	if got := refs[0]["referenceType"]; got != "asset" {
		t.Fatalf("referenceType = %v, want asset", got)
	}
	if _, hasImage := instance["image"]; hasImage {
		t.Fatalf("image should not be sent together with referenceImages")
	}

	// 非対応モデル (Fast): image 入力へフォールバック。
	runner = &VertexVeoRunner{model: "veo-3.1-fast-generate-001", outputStorageURI: "gs://bucket/out/"}
	instance = firstInstance(t, runner.buildGenerateBody(context.Background(), req))
	if _, hasRefs := instance["referenceImages"]; hasRefs {
		t.Fatalf("fast model should not send referenceImages")
	}
	image, ok := instance["image"].(map[string]any)
	if !ok || image["gcsUri"] != "gs://bucket/kf.png" {
		t.Fatalf("image = %v, want keyframe fallback", instance["image"])
	}

	// video-to-video 文脈: video のみ送り、referenceImages / image は送らない。
	runner = &VertexVeoRunner{model: "veo-3.1-generate-001", outputStorageURI: "gs://bucket/out/", usePreviousVideo: true}
	instance = firstInstance(t, runner.buildGenerateBody(context.Background(), req))
	if _, hasVideo := instance["video"]; !hasVideo {
		t.Fatalf("video context should be sent when usePreviousVideo is enabled")
	}
	if _, hasRefs := instance["referenceImages"]; hasRefs {
		t.Fatalf("referenceImages must not be sent together with video")
	}
	if _, hasImage := instance["image"]; hasImage {
		t.Fatalf("image must not be sent together with video")
	}
}

func firstInstance(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	instances, ok := body["instances"].([]any)
	if !ok || len(instances) == 0 {
		t.Fatalf("instances missing in body: %v", body)
	}
	instance, ok := instances[0].(map[string]any)
	if !ok {
		t.Fatalf("instance is not a map: %v", instances[0])
	}
	return instance
}

// TestVertexVeoRunnerModelURL verifies regional and global endpoint URL construction.
func TestVertexVeoRunnerModelURL(t *testing.T) {
	regional := &VertexVeoRunner{projectID: "proj", locationID: "us-central1", model: "veo-3.1-generate-001"}
	want := "https://us-central1-aiplatform.googleapis.com/v1/projects/proj/locations/us-central1/publishers/google/models/veo-3.1-generate-001:predictLongRunning"
	if got := regional.modelURL("predictLongRunning"); got != want {
		t.Fatalf("regional modelURL = %q, want %q", got, want)
	}

	global := &VertexVeoRunner{projectID: "proj", locationID: "global", model: "veo-3.1-generate-001"}
	want = "https://aiplatform.googleapis.com/v1/projects/proj/locations/global/publishers/google/models/veo-3.1-generate-001:fetchPredictOperation"
	if got := global.modelURL("fetchPredictOperation"); got != want {
		t.Fatalf("global modelURL = %q, want %q", got, want)
	}
}

// TestVertexVeoRunnerWithVideoOptionsDerivesRunner verifies per-task model/aspect derivation.
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

// TestVertexVeoRunnerBuildGenerateBodyUsesJobScopedCutStorageURI verifies that job-scoped GCS output is used for cut generation.
func TestVertexVeoRunnerBuildGenerateBodyUsesJobScopedCutStorageURI(t *testing.T) {
	runner := &VertexVeoRunner{outputStorageURI: "gs://bucket/ap-mv/veo/"}
	ctx := ports.WithVideoOutputBaseURI(context.Background(), "gs://bucket/ap-mv/veo/jobs/job-1/")

	body := runner.buildGenerateBody(ctx, ports.VideoGenerationRequest{
		Prompt:      "scene",
		CutIndex:    2,
		DurationSec: 8,
	})

	parameters := body["parameters"].(map[string]any)
	if got := parameters["storageUri"]; got != "gs://bucket/ap-mv/veo/jobs/job-1/tmp/videos/cut_02/" {
		t.Fatalf("storageUri = %v", got)
	}
}

// TestVertexVeoRunnerBuildGenerateBodyFallsBackToDefaultStorageURI verifies that Veo output falls back to the runner default GCS URI.
func TestVertexVeoRunnerBuildGenerateBodyFallsBackToDefaultStorageURI(t *testing.T) {
	runner := &VertexVeoRunner{outputStorageURI: "gs://bucket/ap-mv/veo/"}

	body := runner.buildGenerateBody(context.Background(), ports.VideoGenerationRequest{
		Prompt:      "scene",
		CutIndex:    2,
		DurationSec: 8,
	})

	parameters := body["parameters"].(map[string]any)
	if got := parameters["storageUri"]; got != "gs://bucket/ap-mv/veo/" {
		t.Fatalf("storageUri = %v", got)
	}
}

// TestVertexVeoRunnerCanonicalizeGeneratedVideoCopiesToCutFile verifies that generated videos are copied to canonical cut files.
func TestVertexVeoRunnerCanonicalizeGeneratedVideoCopiesToCutFile(t *testing.T) {
	copier := &captureVideoCopier{}
	runner := &VertexVeoRunner{videoCopier: copier}
	ctx := ports.WithVideoOutputBaseURI(context.Background(), "gs://bucket/ap-mv/veo/jobs/job-1/")

	video, err := runner.canonicalizeGeneratedVideo(ctx, ports.VideoGenerationRequest{
		CutIndex: 2,
	}, vertexVideo{
		GCSURI: "gs://bucket/ap-mv/veo/jobs/job-1/tmp/videos/cut_02/sample_0.mp4",
	})
	if err != nil {
		t.Fatalf("canonicalizeGeneratedVideo() error = %v", err)
	}
	if copier.sourceURI != "gs://bucket/ap-mv/veo/jobs/job-1/tmp/videos/cut_02/sample_0.mp4" {
		t.Fatalf("sourceURI = %q", copier.sourceURI)
	}
	if copier.targetURI != "gs://bucket/ap-mv/veo/jobs/job-1/videos/cut_02.mp4" {
		t.Fatalf("targetURI = %q", copier.targetURI)
	}
	if video.GCSURI != "gs://bucket/ap-mv/veo/jobs/job-1/videos/cut_02.mp4" {
		t.Fatalf("video GCSURI = %q", video.GCSURI)
	}
	if video.URI != "gs://bucket/ap-mv/veo/jobs/job-1/videos/cut_02.mp4" {
		t.Fatalf("video URI = %q", video.URI)
	}
	if copier.deletedURI != "gs://bucket/ap-mv/veo/jobs/job-1/tmp/videos/cut_02/sample_0.mp4" {
		t.Fatalf("deletedURI = %q", copier.deletedURI)
	}
}

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
