package adapters

import (
	"context"
	"testing"

	"ap-mv/internal/ports"
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
