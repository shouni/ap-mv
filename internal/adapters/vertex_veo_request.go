package adapters

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strings"

	"github.com/shouni/genai-kit/gemini"
	"github.com/shouni/genai-kit/veo"

	"github.com/shouni/ap-mv/internal/ports"
)

// buildRequest converts the internal video-generation request into the veo package's request.
// Which generation feature it is built with (video / referenceImages / image+lastFrame / image) is
// unified in ports.ClassifyVeoRequest, so it structurally matches the filter side's prompt and
// duration choice (Veo cannot combine video / referenceImages / image in one request).
func (r *VertexVeoRunner) buildRequest(ctx context.Context, req ports.VideoGenerationRequest) veo.Request {
	out := veo.Request{
		Prompt:        strings.TrimSpace(req.Prompt),
		DurationSec:   int(math.Round(req.DurationSec)),
		AspectRatio:   r.aspectRatio,
		GenerateAudio: &r.generateAudio,
		OutputGCSURI:  r.outputStorageURIFor(ctx, req),
	}
	if req.Seed != 0 {
		seed := req.Seed
		out.Seed = &seed
	}

	switch ports.ClassifyVeoRequest(req, r.usePreviousVideo, r.capabilities()) {
	case ports.VeoModeVideoExtension:
		out.Video = previousVideoMedia(req.PreviousVideoURI)
	case ports.VeoModeReferenceToVideo:
		out.References = referenceImagesMedia(req)
	case ports.VeoModeFramesToVideo:
		out.Image = imageMedia(req)
		out.LastFrame = lastFrameMedia(req)
	case ports.VeoModeImageToVideo:
		// A request with no image reference at all (text only) is also classified as image_to_video,
		// so image is set only when it exists.
		out.Image = imageMedia(req)
	}

	if audio := audioMedia(req); audio != nil {
		out.ModifyRequestBody = injectAudioInstance(*audio)
	}
	return out
}

// injectAudioInstance returns a hook that inserts the audio input into instances[0] of the
// assembled request body.
//
// The SDK has no type for video generation's audio input, so the body is rewritten directly.
// ExtraBody cannot reach this position (merging recurses only between maps, and instances is an
// array, so it would be replaced wholesale and the prompt and image inputs would vanish).
//
// Whether Veo actually interprets this audio input is unverified. The official SDK has no matching
// field either, so it may be ignored. Compare one cut with and without it, and if it has no
// effect, remove this whole path.
func injectAudioInstance(audio map[string]any) func(map[string]any) map[string]any {
	return func(body map[string]any) map[string]any {
		instances, ok := body["instances"].([]any)
		if !ok || len(instances) == 0 {
			return body
		}
		instance, ok := instances[0].(map[string]any)
		if !ok {
			return body
		}
		instance["audio"] = audio
		return body
	}
}

// capabilities returns the model's support for Veo's optional features as ports.VeoCapabilities
// (the input to ports.ClassifyVeoRequest). ports.VeoModelCapabilities is the single definition of
// the model name → feature rule (prefix matching on strings used to be re-derived here, breaking
// the principle that "the library owns the rule").
func (r *VertexVeoRunner) capabilities() ports.VeoCapabilities {
	return ports.VeoModelCapabilities(r.model)
}

// SupportsLastFrame satisfies ports.LastFrameSupporter.
// It is the entrance through which the video_gen filter asks whether to pass the next cut's keyframe
// as lastFrame (frames_to_video interpolation) without duplicating the model-detection logic.
func (r *VertexVeoRunner) SupportsLastFrame() bool {
	return r.capabilities().LastFrame
}

// SupportsReferenceImages satisfies ports.ReferenceImagesSupporter. It is the entrance through
// which the caller (normalising cut durations to Veo's supported values) asks whether this Runner
// uses reference_to_video (referenceImages, fixed at 8 seconds) or image_to_video ({4,6,8}
// seconds) without duplicating the model-detection logic.
func (r *VertexVeoRunner) SupportsReferenceImages() bool {
	return r.capabilities().ReferenceImages
}

// validateVertexVeoRequest validates the request fields the Veo API adapter needs.
func validateVertexVeoRequest(req ports.VideoGenerationRequest) error {
	if strings.TrimSpace(req.Prompt) == "" {
		return fmt.Errorf("prompt is required")
	}
	if req.CutIndex < 0 {
		return fmt.Errorf("cut_index must be non-negative")
	}
	if req.DurationSec <= 0 {
		return fmt.Errorf("duration_sec must be positive")
	}
	if req.Seed < 0 || req.Seed > math.MaxInt32 {
		return fmt.Errorf("seed must be between 0 and %d", math.MaxInt32)
	}
	return nil
}

// referenceImagesMedia builds Veo's reference image list from ReferenceImages.
// Entries with an empty URI are excluded, and it returns nil when nothing remains.
func referenceImagesMedia(req ports.VideoGenerationRequest) []veo.Reference {
	var result []veo.Reference
	for _, uri := range req.ReferenceImages {
		if uri = strings.TrimSpace(uri); uri == "" {
			continue
		}
		result = append(result, veo.Reference{
			Image: veo.Media{URI: uri, MIMEType: mimeTypeFromURI(uri, "image/png")},
			Type:  gemini.VideoReferenceAsset,
		})
	}
	return result
}

// imageMedia builds Veo's image input from a GCS image reference or inline image bytes.
func imageMedia(req ports.VideoGenerationRequest) *veo.Media {
	if ref := strings.TrimSpace(req.ImageReference); ref != "" {
		return &veo.Media{URI: ref, MIMEType: mimeTypeFromURI(ref, "image/png")}
	}
	if len(req.InputImage) == 0 {
		return nil
	}
	return &veo.Media{Data: req.InputImage, MIMEType: detectedImageMimeType(req.InputImage)}
}

// lastFrameMedia builds Veo's end-frame input from LastFrameReference.
func lastFrameMedia(req ports.VideoGenerationRequest) *veo.Media {
	ref := strings.TrimSpace(req.LastFrameReference)
	if ref == "" {
		return nil
	}
	return &veo.Media{URI: ref, MIMEType: mimeTypeFromURI(ref, "image/png")}
}

// previousVideoMedia builds Veo's video-continuation input from the GCS URI of the previously generated video.
func previousVideoMedia(previousVideoID string) *veo.Media {
	ref := strings.TrimSpace(previousVideoID)
	if !strings.HasPrefix(ref, "gs://") {
		return nil
	}
	return &veo.Media{URI: ref, MIMEType: mimeTypeFromURI(ref, "video/mp4")}
}

// audioMedia builds Veo's audio input payload from a GCS audio reference or inline audio bytes.
// The SDK has no type for it, so it is handled as a raw map.
func audioMedia(req ports.VideoGenerationRequest) *map[string]any {
	if ref := strings.TrimSpace(req.AudioReference); ref != "" {
		return &map[string]any{
			"gcsUri":   ref,
			"mimeType": mimeTypeFromURI(ref, "audio/mpeg"),
		}
	}
	if len(req.InputAudio) == 0 {
		return nil
	}
	return &map[string]any{
		"bytesBase64Encoded": req.InputAudio,
		"mimeType":           detectedAudioMimeType(req.InputAudio),
	}
}

// detectedAudioMimeType detects a supported inline audio MIME type and returns audio/mpeg when unknown.
func detectedAudioMimeType(data []byte) string {
	if len(data) == 0 {
		return "audio/mpeg"
	}
	limit := min(len(data), 512)
	mimeType := http.DetectContentType(data[:limit])
	switch mimeType {
	case "audio/mpeg", "audio/wav", "audio/ogg", "audio/flac", "audio/mp4":
		return mimeType
	default:
		return "audio/mpeg"
	}
}

// detectedImageMimeType detects a supported inline image MIME type and returns image/png when unknown.
func detectedImageMimeType(data []byte) string {
	mimeType := http.DetectContentType(data)
	switch mimeType {
	case "image/jpeg", "image/png", "image/webp":
		return mimeType
	default:
		return "image/png"
	}
}

// mimeTypeFromURI guesses the media MIME type from the URI's extension and returns fallback when unknown.
func mimeTypeFromURI(uri, fallback string) string {
	lower := strings.ToLower(uri)
	switch {
	case strings.HasSuffix(lower, ".jpg"), strings.HasSuffix(lower, ".jpeg"):
		return "image/jpeg"
	case strings.HasSuffix(lower, ".png"):
		return "image/png"
	case strings.HasSuffix(lower, ".webp"):
		return "image/webp"
	case strings.HasSuffix(lower, ".mov"):
		return "video/mov"
	case strings.HasSuffix(lower, ".mpeg"):
		return "video/mpeg"
	case strings.HasSuffix(lower, ".mp4"):
		return "video/mp4"
	case strings.HasSuffix(lower, ".mpg"):
		return "video/mpg"
	case strings.HasSuffix(lower, ".avi"):
		return "video/avi"
	case strings.HasSuffix(lower, ".wmv"):
		return "video/wmv"
	case strings.HasSuffix(lower, ".flv"):
		return "video/flv"
	default:
		return fallback
	}
}
