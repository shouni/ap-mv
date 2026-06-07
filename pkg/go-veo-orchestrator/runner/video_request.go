package runner

import (
	"fmt"
	"strings"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	"github.com/shouni/go-veo-orchestrator/ports"
)

type VideoRequestBuilder interface {
	Build(recipe *ports.VideoRecipe, cut ports.Cut, keyframe *imagePorts.ImageResponse, previousVideoID string) ports.VideoGenerationRequest
}

type DefaultVideoRequestBuilder struct{}

func NewVideoRequestBuilder() *DefaultVideoRequestBuilder {
	return &DefaultVideoRequestBuilder{}
}

func (b *DefaultVideoRequestBuilder) Build(recipe *ports.VideoRecipe, cut ports.Cut, keyframe *imagePorts.ImageResponse, previousVideoID string) ports.VideoGenerationRequest {
	var imageData []byte
	var seed int64
	if keyframe != nil {
		imageData = keyframe.Data
		seed = keyframe.UsedSeed
	}
	if seed == 0 {
		seed = recipe.Seed
	}

	return ports.VideoGenerationRequest{
		Prompt:          b.buildPrompt(recipe, cut),
		ImageReference:  cut.KeyframeReference,
		AudioReference:  cut.AudioReference,
		InputImage:      imageData,
		PreviousVideoID: previousVideoID,
		Seed:            seed,
		CutIndex:        cut.CutIndex,
		DurationSec:     cut.DurationSec,
	}
}

func (b *DefaultVideoRequestBuilder) buildPrompt(recipe *ports.VideoRecipe, cut ports.Cut) string {
	parts := []string{
		strings.TrimSpace(cut.VisualAnchor),
	}
	if cut.AudioCue != "" {
		parts = append(parts, fmt.Sprintf("Synchronize motion and camera timing with audio cue: %s", cut.AudioCue))
	}
	if recipe.MusicRecipe.Style != "" {
		parts = append(parts, "Music style: "+recipe.MusicRecipe.Style)
	}
	if cut.StartSec != 0 || cut.EndSec != 0 {
		parts = append(parts, fmt.Sprintf("Timeline: %.2fs to %.2fs.", cut.StartSec, cut.EndSec))
	}

	nonEmpty := parts[:0]
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			nonEmpty = append(nonEmpty, part)
		}
	}
	return strings.Join(nonEmpty, "\n")
}
