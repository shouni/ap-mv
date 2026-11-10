package step

import (
	"context"
	"fmt"
	"strings"

	"github.com/shouni/go-veo-orchestrator/veo"
	"github.com/shouni/go-veo-orchestrator/video"
)

// SectionSelectStep is the pipeline step that narrows the recipe to only the cuts belonging to the
// section given by sc.Task.SectionIndex. The later VideoGenerationStep turns only the section's
// cuts into video, producing a short.
type SectionSelectStep struct{}

// Name returns the receiver name.
func (SectionSelectStep) Name() string { return "section_select" }

// Execute trims the recipe to the cuts whose StartSec falls inside the selected section.
func (SectionSelectStep) Execute(_ context.Context, sc *Context) error {
	if sc == nil || sc.Task == nil || sc.Task.SectionIndex == nil {
		return fmt.Errorf("section_select requires task with section_index")
	}
	if err := ensureVideoRecipe(sc); err != nil {
		return err
	}
	sc.VideoRecipe.Normalize()
	// Fill in lyric-derived dialogue in case the stored recipe lacks it (old jobs included).
	applyLyricsToVideoRecipeCuts(sc.VideoRecipe)

	sections := sc.VideoRecipe.MusicRecipe.Sections
	sectionIndex := *sc.Task.SectionIndex
	if sectionIndex < 0 || sectionIndex >= len(sections) {
		return fmt.Errorf("section_index %d is out of range (recipe has %d sections)", sectionIndex, len(sections))
	}
	// cut.SectionIndex is 1-based, so add 1 when comparing with the 0-based sectionIndex.
	wantSectionIndex := sectionIndex + 1

	// The stored metadata's keyframe_reference may be relative to the original job, so make it an
	// absolute URI from the original job's root before turning it into video under the new job's output path.
	originalBase := originalJobOutputPath(sc.Task.RecipeURL)

	cuts := make([]video.Cut, 0, len(sc.VideoRecipe.Cuts))
	for _, cut := range sc.VideoRecipe.Cuts {
		if cut.SectionIndex != wantSectionIndex {
			continue
		}
		// A short regenerates even the cuts generated in the original job with the task's model and
		// aspect ratio, so reset their generation state. IsChainStart is a leftover of the chain structure
		// from the original job's generation (or scene_split's advance planning), so clear it here and let
		// the later VideoGenerationStep rebuild chains from cumulative length as usual.
		cut.ResetGeneration(true)
		cut.KeyframeReference = resolveRecipeObjectURI(originalBase, cut.KeyframeReference)
		cuts = append(cuts, cut)
	}
	if len(cuts) == 0 {
		return fmt.Errorf("no cuts found in section %d (%s)", sectionIndex, sections[sectionIndex].Name)
	}
	// Veo accepts cut durations of only 4/6/8 seconds for image_to_video and only 8 seconds for
	// reference_to_video (referenceImages), so long cuts saved at section length (recipes that only
	// generated keyframes and the like) are split into sub-cuts of 8 seconds or less, each rounded to
	// a supported value, before going to video generation. Cuts beyond the YouTube Shorts limit (60
	// seconds) are also trimmed. Normalising to the fixed 7 seconds for video_extension is done last
	// by the later VideoGenerationStep based on UsePreviousVideo, so here it only needs to split and
	// round to the image_to_video/reference_to_video durations.
	sc.VideoRecipe.Cuts = veo.CapCutsTotalDuration(veo.ExpandCutsToSupportedDurations(cuts, false, sc.Characters, referenceImagesSupported(sc.VideoRunner)), youtubeShortMaxDurationSec)

	recipe, err := toDomainRecipe(sc.VideoRecipe)
	if err != nil {
		return err
	}
	sc.Recipe = recipe
	return nil
}

// youtubeShortMaxDurationSec is the maximum length (seconds) of a YouTube Short.
const youtubeShortMaxDurationSec = 60.0

// resolveRecipeObjectURI resolves an object reference relative to the original job into an absolute URI.
// References that already have a scheme, or when base cannot be derived, are returned as-is.
// It joins safely whether or not base has a trailing slash.
func resolveRecipeObjectURI(base, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" || strings.Contains(ref, "://") || base == "" {
		return ref
	}
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(ref, "/")
}
