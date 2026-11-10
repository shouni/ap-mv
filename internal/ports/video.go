package ports

import (
	"context"
	"strings"

	veoports "github.com/shouni/go-veo-orchestrator/ports"
	"github.com/shouni/go-veo-orchestrator/video"
)

// VideoRunner is the Veo adapter boundary defined by go-veo-orchestrator.
type VideoRunner = veoports.VideoRunner

// VideoGenerationRequest is go-veo-orchestrator's video-generation request type.
type VideoGenerationRequest = video.GenerationRequest

// VideoResponse is go-veo-orchestrator's video-generation response type.
type VideoResponse = video.Response

// VideoRunnerConfigurator is an optional VideoRunner interface that can return a derived Runner
// with the Veo model or aspect ratio replaced per task.
type VideoRunnerConfigurator interface {
	WithVideoOptions(model, aspectRatio string) VideoRunner
}

// ReferenceImagesSupporter is an optional VideoRunner interface that can be asked whether
// reference_to_video (referenceImages; Veo 3 non-Fast models only, fixed at 8 seconds) is usable.
// Normalising cut durations to Veo's supported values uses it to decide "will this cut be fixed at
// 8 seconds" without duplicating the model-detection logic.
type ReferenceImagesSupporter interface {
	SupportsReferenceImages() bool
}

// LastFrameSupporter is an optional VideoRunner interface that can be asked whether
// frames_to_video (first/last frame interpolation with image + lastFrame; Veo 2 / Veo 3.1 only)
// is usable. The video_gen filter uses it to decide whether to pass the next cut's keyframe as
// lastFrame without duplicating the model-detection logic.
type LastFrameSupporter interface {
	SupportsLastFrame() bool
}

// DeriveVideoRunner returns a derived Runner with the model and aspect ratio replaced when runner
// implements VideoRunnerConfigurator. When both are empty, or runner does not support replacing,
// it returns runner as-is.
func DeriveVideoRunner(runner VideoRunner, model, aspectRatio string) VideoRunner {
	if runner == nil {
		return nil
	}
	model = strings.TrimSpace(model)
	aspectRatio = strings.TrimSpace(aspectRatio)
	if model == "" && aspectRatio == "" {
		return runner
	}
	configurator, ok := runner.(VideoRunnerConfigurator)
	if !ok {
		return runner
	}
	return configurator.WithVideoOptions(model, aspectRatio)
}

type videoOutputBaseURIKey struct{}

// WithVideoOutputBaseURI stores the job-scoped output base URI for video runners.
func WithVideoOutputBaseURI(ctx context.Context, baseURI string) context.Context {
	baseURI = strings.TrimSpace(baseURI)
	if baseURI == "" {
		return ctx
	}
	return context.WithValue(ctx, videoOutputBaseURIKey{}, baseURI)
}

// VideoOutputBaseURIFromContext returns the job-scoped output base URI, if set.
func VideoOutputBaseURIFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	baseURI, ok := ctx.Value(videoOutputBaseURIKey{}).(string)
	baseURI = strings.TrimSpace(baseURI)
	return baseURI, ok && baseURI != ""
}
