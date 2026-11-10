package ports

import (
	"github.com/shouni/go-veo-orchestrator/veo"
)

// This file delegates Veo request classification to go-veo-orchestrator.
//
// ClassifyVeoRequest's implementation used to live here as-is, but the same decision existed in
// the library's ports/veo_mode.go, a duplicate that did not differ by a single line. The
// library's runner/video.go also uses the classification for the pre-send duration check (a
// mismatch becomes ErrUnsupportedCutDuration), so changing only one breaks things. The definition
// lives once in the library, and ap-mv keeps only aliases to preserve the call names.
// The duration constants and candidates and the duration planner (ImageToVideoDurationsSec /
// ChainDurations / ExpandCutsToSupportedDurations and so on) use the library's ports directly for the same reason.

// VeoGenerationMode is which Veo generation feature interprets one video-generation request.
// The values match the prompt file names (without extension) under assets/prompts/video_gen/.
type VeoGenerationMode = veo.GenerationMode

const (
	// VeoModeImageToVideo is image_to_video, with the keyframe image as the image input.
	VeoModeImageToVideo = veo.ModeImageToVideo
	// VeoModeFramesToVideo is first/last frame interpolation, with the start frame as the image input
	// and the end frame as the lastFrame input (Veo 2 / Veo 3.1 only, Fast included).
	VeoModeFramesToVideo = veo.ModeFramesToVideo
	// VeoModeReferenceToVideo is reference_to_video, with [character art, keyframe] as referenceImages
	// (Veo 3 non-Fast models only, fixed at 8 seconds).
	VeoModeReferenceToVideo = veo.ModeReferenceToVideo
	// VeoModeVideoExtension is video_extension (video-to-video continuation), with the previous cut's
	// video as the video input. No image references at all are sent in this mode.
	VeoModeVideoExtension = veo.ModeVideoExtension
)

// VeoCapabilities is whether the Runner/model actually used supports Veo's optional features.
// It is ClassifyVeoRequest's input; the adapter derives it from the model name, the filter from
// the Runner's optional interfaces.
type VeoCapabilities = veo.Capabilities

// RunnerCapabilities derives VeoCapabilities from the Runner's optional interfaces
// (ReferenceImagesSupporter / LastFrameSupporter). A Runner that implements none (test doubles and
// the like) gets false for every feature and falls to the image_to_video side.
func RunnerCapabilities(runner VideoRunner) VeoCapabilities {
	return veo.RunnerCapabilities(runner)
}

// ClassifyVeoRequest decides which Veo generation feature interprets this request.
// It is the single branch point shared by the adapter's request-body construction and the
// filter's prompt and duration choice, with this priority:
//
//  1. video_extension — when usePreviousVideo is on and PreviousVideoURI is a gs:// reference.
//     Veo cannot combine video with referenceImages / image, so every later image reference is
//     ignored.
//  2. reference_to_video — when there is at least one reference image URI and the model supports
//     referenceImages.
//  3. frames_to_video — when both a start frame (ImageReference or InputImage) and
//     LastFrameReference exist and the model supports lastFrame
//     (Veo's lastFrame is valid only together with image).
//  4. image_to_video — everything else.
func ClassifyVeoRequest(req VideoGenerationRequest, usePreviousVideo bool, caps VeoCapabilities) VeoGenerationMode {
	return veo.ClassifyRequest(req, usePreviousVideo, caps)
}

// VeoModelCapabilities derives support for optional features from a Veo model name.
// The library is the single definition of the model name → feature rule.
func VeoModelCapabilities(model string) VeoCapabilities {
	return veo.ModelCapabilities(model)
}
