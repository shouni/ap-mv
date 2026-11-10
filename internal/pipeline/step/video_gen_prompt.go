package step

import (
	"strings"
	"sync"

	"github.com/shouni/go-veo-orchestrator/video"

	"github.com/shouni/ap-mv/assets"
	"github.com/shouni/ap-mv/internal/ports"
)

// This file gathers building the video-generation prompt and choosing the lastFrame (the next
// cut's keyframe) used for frames_to_video interpolation. For the generation loop itself see
// video_gen.go.

// videoGenGuidance holds the prompt guidance per Veo generation mode.
// They are embedded assets (guaranteed to exist at compile time), so a load failure at run time
// practically cannot happen, but if one were missing generation would continue without guidance
// (a test checks every mode exists, so a gap is caught in CI).
var videoGenGuidance = sync.OnceValue(func() map[string]string {
	prompts, err := assets.LoadVideoGenPrompts()
	if err != nil {
		return map[string]string{}
	}
	return prompts
})

// videoPrompt builds the prompt used for video generation, appending the guidance that matches
// how Veo will actually interpret the request (the result of ports.ClassifyVeoRequest).
// Telling each generation mode its correct premise (a start frame, reference images, or continuing
// the previous clip) avoids mentioning inputs that do not exist (e.g. "match the reference image"
// under video_extension). When VisualAnchor and AudioCue are both empty it returns an empty string
// as before, so validateVertexVeoRequest's "prompt is required" check still catches broken recipes.
func videoPrompt(cut video.Cut, mode ports.VeoGenerationMode) string {
	anchor := strings.TrimSpace(cut.VisualAnchor)
	cue := strings.TrimSpace(cut.AudioCue)
	if anchor == "" && cue == "" {
		return ""
	}
	parts := make([]string, 0, 3)
	if anchor != "" {
		parts = append(parts, anchor)
	}
	if cue != "" {
		parts = append(parts, "Synchronize motion and camera timing with audio cue: "+cue)
	}
	if guidance := strings.TrimSpace(videoGenGuidance()[string(mode)]); guidance != "" {
		parts = append(parts, guidance)
	}
	return strings.Join(parts, "\n")
}
