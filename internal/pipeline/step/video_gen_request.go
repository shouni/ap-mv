package step

import (
	characterkit "github.com/shouni/go-character-kit/character"
	"github.com/shouni/go-veo-orchestrator/runner"
	"github.com/shouni/go-veo-orchestrator/veo"
	"github.com/shouni/go-veo-orchestrator/video"

	"github.com/shouni/ap-mv/internal/ports"
)

// videoRequestBuilder satisfies runner.VideoRequestBuilder and builds Veo requests carrying this
// app's prompt.
//
// The library's default builder (runner.NewVideoRequestBuilderWithCharacters) is not used because
// of the prompt. The default just lines up VisualAnchor / AudioCue / Mood / Timeline, while this app
// adds guidance per generation mode (assets/prompts/video_gen). The seed differs from the default
// too: it uses the character's seed rather than the keyframe's UsedSeed (to match keyframe.Generator,
// which always draws with char.Seed; see videoSeed).
type videoRequestBuilder struct {
	characters *characterkit.Characters
	// usePreviousVideo is the VEO_USE_PREVIOUS_VIDEO setting. It is the value passed to classification,
	// distinct from whether this cut actually has a video to carry over (in.PreviousVideoURI).
	usePreviousVideo bool
}

// Build builds the Veo request for one cut.
func (b videoRequestBuilder) Build(in runner.BuildInput) video.GenerationRequest {
	cut := in.Cut
	req := video.GenerationRequest{
		CutIndex:           cut.CutIndex,
		DurationSec:        cut.DurationSec,
		Seed:               characterSeed(b.characters, cut),
		PreviousVideoURI:   in.PreviousVideoURI,
		ImageReference:     cut.KeyframeReference,
		LastFrameReference: in.LastFrameReference,
		ReferenceImages:    video.CutReferenceImages(cut, b.characters),
		AudioReference:     cut.AudioReference,
	}
	mode := veo.ClassifyRequest(req, b.usePreviousVideo, in.Capabilities)
	// References this mode does not actually use are not left in the request, for two reasons.
	//
	// The request contents in logs and reproductions should match what the adapter sends. And leaving
	// a PreviousVideoURI we decided not to carry over makes those who re-classify from the request
	// itself (the runner's duration check, the adapter's assembly) disagree with the decision here.
	// In fact, the previous cut's URI kept riding along even with VEO_USE_PREVIOUS_VIDEO disabled.
	if mode != veo.ModeFramesToVideo {
		req.LastFrameReference = ""
	}
	if mode != veo.ModeVideoExtension {
		req.PreviousVideoURI = ""
	}
	req.Prompt = videoPrompt(cut, ports.VeoGenerationMode(mode))
	return req
}

// characterSeed returns the seed tied to the cut's character.
//
// It is the same value as keyframe image generation (keyframe.Generator always uses only char.Seed).
// Independent generation with no seed (0 = omitted from the Veo request) varies the look randomly
// at every chain start, so always passing a seed fixed at least per character improves character
// consistency within and across jobs. It returns 0 only when the character has no seed.
func characterSeed(characters *characterkit.Characters, cut video.Cut) int64 {
	if characters != nil {
		if char := characters.GetCharacter(cut.CharacterID); char != nil && char.Seed != nil {
			return *char.Seed
		}
	}
	return 0
}
