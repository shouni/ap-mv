package step

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"

	characterkit "github.com/shouni/go-character-kit/character"
	"github.com/shouni/go-veo-orchestrator/veo"
	"github.com/shouni/go-veo-orchestrator/video"

	"github.com/shouni/ap-mv/internal/ports"
)

// SceneSplitStep expands long cuts before keyframe generation so each sub-cut can receive its
// own keyframe and video direction instead of sharing one section-level image.
// UsePreviousVideo should match VideoGenerationStep.UsePreviousVideo. When true, cuts are
// re-allocated into chain blocks that each become a fresh video-to-video chain base instead of
// generating a new keyframe for every 8-second video cut.
//
// Duration allocation treats the song timeline as the source of truth: Veo only supports a
// discrete set of durations, so each cut's rounding error is carried into the next cut's target
// (error diffusion) instead of being rounded up per cut, and StartSec/EndSec are re-based to the
// concatenated video timeline so cuts never overlap.
type SceneSplitStep struct {
	UsePreviousVideo bool
	// Runner mirrors VideoGenerationStep.Runner and must be wired from the same VideoRunner by
	// the planner. Scene splitting decides each cut's chain base from the same reference_to_video /
	// image_to_video classification VideoGenerationStep uses, so both steps must resolve the
	// runner (and thus its Veo capabilities) identically — see resolvedVideoRunner.
	Runner ports.VideoRunner
}

// Name returns the receiver name.
func (SceneSplitStep) Name() string { return "scene_split" }

// resolvedVideoRunner returns the VideoRunner that generation will actually use: f.Runner takes
// priority, falling back to sc.VideoRunner. This mirrors VideoGenerationStep.resolvedVideoRunner
// so the two steps never disagree on whether referenceImages is supported.
func (f SceneSplitStep) resolvedVideoRunner(sc *Context) ports.VideoRunner {
	if f.Runner != nil {
		return f.Runner
	}
	return sc.VideoRunner
}

// Execute runs the receiver processing step.
func (f SceneSplitStep) Execute(_ context.Context, sc *Context) error {
	if sc == nil {
		return fmt.Errorf("scene split requires recipe")
	}
	if err := ensureVideoRecipe(sc); err != nil {
		return err
	}
	sc.VideoRecipe.Normalize()
	applyLyricsToVideoRecipeCuts(sc.VideoRecipe)
	if f.UsePreviousVideo {
		sc.VideoRecipe.Cuts = expandCutsForVideoToVideoScenes(sc.VideoRecipe.Cuts, sc.Characters, referenceImagesSupported(f.resolvedVideoRunner(sc)))
	} else {
		sc.VideoRecipe.Cuts = expandCutsForKeyframeScenes(sc.VideoRecipe.Cuts)
	}

	recipe, err := toDomainRecipe(sc.VideoRecipe)
	if err != nil {
		return err
	}
	sc.Recipe = recipe
	return nil
}

func expandCutsForKeyframeScenes(cuts []video.Cut) []video.Cut {
	expanded := make([]video.Cut, 0, len(cuts))
	videoEnd := 0.0
	if len(cuts) > 0 {
		videoEnd = cuts[0].StartSec
	}
	for _, cut := range cuts {
		priorKeyframe := cut.KeyframeReference
		cut = resetCutForSceneKeyframe(cut)
		// Error diffusion: including the rounding error up to the previous cut (the gap between
		// videoEnd and the song time), aim the cumulative video length at this cut's end in the song.
		// StartSec/EndSec are reassigned on the joined video timeline, so cuts never overlap.
		target := cutMusicalEndSec(cut) - videoEnd
		cut.StartSec = videoEnd
		cut.DurationSec = target
		cut.EndSec = videoEnd + target
		subCuts := veo.SplitCutBySupportedDurations(cut, veo.ImageToVideoDurationsSec())
		if len(subCuts) > 1 {
			lines := veo.SplitDialogueLines(cut.Dialogue)
			for i := range subCuts {
				subCuts[i].AudioCue = sceneAudioCue(cut.AudioCue, i, len(subCuts))
				subCuts[i].VisualAnchor = sceneVisualAnchor(cut.VisualAnchor, i, len(subCuts))
				subCuts[i].Dialogue = veo.DistributeLines(lines, i, len(subCuts))
			}
		} else {
			subCuts[0].KeyframeReference = priorKeyframe
		}
		expanded = append(expanded, subCuts...)
		videoEnd = expanded[len(expanded)-1].EndSec
	}
	for i := range expanded {
		expanded[i].CutIndex = i + 1
	}
	return expanded
}

// cutMusicalEndSec returns the time (seconds) at which this cut should end in the song.
func cutMusicalEndSec(cut video.Cut) float64 {
	if cut.EndSec > cut.StartSec {
		return cut.EndSec
	}
	return cut.StartSec + cut.DurationSec
}

// expandCutsForVideoToVideoScenes reassigns the cuts of the UsePreviousVideo (continuation chain)
// approach into a sequence of chain blocks. Each block is generated whole as one continuation
// chain (one base cut + a run of video_extension continuation cuts, with a total length from the
// candidates of videoToVideoChainDurations), and each block gets its own keyframe. Blocks are
// marked IsChainStart, and downstream expandCutsToSupportedDurations sees it, splits the block
// into the actually generated cuts [base, 7s, ...] and treats it as a new chain start in the
// cumulative-length check too (so the planned length is not rewritten downstream).
//
// Durations are allocated with the song timeline as the truth, and each cut's rounding error (in
// either direction, up or down) is carried into the next cut's target and cancels out (error
// diffusion). After allocation StartSec/EndSec are reassigned on the joined video timeline, so
// cuts never overlap. Time expressions in AudioCue's text (e.g. "0:28 to 0:37") are kept as-is as
// the lyric-derived approximate times in the song.
//
// characters and referenceImagesSupported decide whether each cut's chain base is generated as
// reference_to_video (fixed at 8 seconds) or image_to_video ({4,6,8} seconds)
// (the same cutUsesReferenceImages rule as expandCutsToSupportedDurations).
func expandCutsForVideoToVideoScenes(cuts []video.Cut, characters *characterkit.Characters, referenceImagesSupported bool) []video.Cut {
	expanded := make([]video.Cut, 0, len(cuts))
	videoEnd := 0.0
	if len(cuts) > 0 {
		videoEnd = cuts[0].StartSec
	}
	for _, cut := range cuts {
		// A cut with IsChainStart set is a chain block a previous SceneSplit already allocated (re-read
		// from a draft, or mv_from_keyframe_video_recipe given a stored recipe). The allocation below
		// reassigns that block to itself, so it always becomes a single block (i == 0), and the scene
		// reset cannot be restored from the index (i > 0). Pick it up here and carry it over.
		sceneReset := cut.IsChainStart && cut.IsSectionStart
		// A cut reassigned to one block keeps its rendered keyframe, which still corresponds to the same
		// cut (it is restored in the len(durations) == 1 branch below). Dropping it would mean
		// re-rendering every image each time "make video from the saved keyframes" runs. When split into
		// several blocks it is not restored, because one picture would correspond to several cuts with
		// different scene beats.
		priorKeyframe := cut.KeyframeReference
		cut = resetCutForSceneKeyframe(cut)
		// Error diffusion: including the rounding error up to the previous cut (the gap between
		// videoEnd and the song time), aim the cumulative video length at this cut's end in the song.
		target := cutMusicalEndSec(cut) - videoEnd
		bases := veo.ImageToVideoDurationsSec()
		if veo.CutUsesReferenceImages(cut, characters, referenceImagesSupported) {
			bases = veo.ReferenceToVideoDurationsSec()
		}
		durations := allocateChainDurations(target, veo.ChainDurations(bases))
		lines := veo.SplitDialogueLines(cut.Dialogue)
		for i, d := range durations {
			sub := cut
			sub.StartSec = videoEnd
			sub.DurationSec = d
			sub.EndSec = videoEnd + d
			sub.IsChainStart = true
			if len(durations) > 1 {
				sub.AudioCue = sceneAudioCue(cut.AudioCue, i, len(durations))
				sub.VisualAnchor = sceneVisualAnchor(cut.VisualAnchor, i, len(durations))
				sub.Dialogue = veo.DistributeLines(lines, i, len(durations))
			} else {
				sub.KeyframeReference = priorKeyframe
			}
			// resetCutForSceneKeyframe already forced IsSectionStart=false above, so this is
			// the only place it gets set: sub-block 2+ is a deliberate in-section scene change
			// ("scene reset", isSceneReset in veo_cut_utils.go's expandCutsToSupportedDurations)
			// that must start from its own keyframe instead of inheriting the previous chain's
			// last frame. Block 1 (the start of a source cut / lyric line) keeps last-frame
			// inheritance for visual continuity. SectionIndex does not change between these
			// sub-blocks; real musical section boundaries are detected downstream from
			// SectionIndex and marked IsSectionStart there.
			// sceneReset carries a scene change planned by an earlier pass over the same
			// recipe, so re-splitting is idempotent (TestSceneSplitStepIsIdempotent).
			sub.IsSectionStart = i > 0 || sceneReset
			expanded = append(expanded, sub)
			videoEnd += d
		}
	}
	for i := range expanded {
		expanded[i].CutIndex = i + 1
	}
	return expanded
}

// allocateChainDurations allocates the target length target (seconds) into a combination of the
// chain length candidates (the return value of videoToVideoChainDurations, assumed ascending
// whole seconds). Priority is (1) the smallest absolute difference between total and target
// (rounding either up or down), (2) fewer blocks (saving keyframe generation and chains),
// (3) a shorter total. The caller carries any excess or shortfall into the next cut's target by
// error diffusion, so only this cut's own error needs minimising here. The result is ascending
// (shorter blocks first).
func allocateChainDurations(target float64, candidates []float64) []float64 {
	smallest := candidates[0]
	if target <= smallest {
		return []float64{smallest}
	}
	largest := candidates[len(candidates)-1]
	limit := int(math.Ceil(target + largest))
	const unreachable = math.MaxInt32
	// blockCounts[s] = the fewest blocks totalling s seconds, choice[s] = the candidate used last for it.
	blockCounts := make([]int, limit+1)
	choice := make([]int, limit+1)
	for s := 1; s <= limit; s++ {
		blockCounts[s] = unreachable
		choice[s] = -1
		for ci, c := range candidates {
			prev := s - int(math.Round(c))
			if prev >= 0 && blockCounts[prev] != unreachable && blockCounts[prev]+1 < blockCounts[s] {
				blockCounts[s] = blockCounts[prev] + 1
				choice[s] = ci
			}
		}
	}
	best := -1
	for s := 1; s <= limit; s++ {
		if blockCounts[s] == unreachable {
			continue
		}
		if best == -1 {
			best = s
			continue
		}
		diff, bestDiff := math.Abs(float64(s)-target), math.Abs(float64(best)-target)
		if diff < bestDiff || (diff == bestDiff && blockCounts[s] < blockCounts[best]) {
			best = s
		}
	}
	var out []float64
	for s := best; s > 0; s -= int(math.Round(candidates[choice[s]])) {
		out = append(out, candidates[choice[s]])
	}
	sort.Float64s(out)
	return out
}

func resetCutForSceneKeyframe(cut video.Cut) video.Cut {
	cut.ResetGeneration(false)
	return cut
}

func sceneAudioCue(cue string, index, total int) string {
	beat := sceneBeatLabel(index, total)
	cue = strings.TrimSpace(cue)
	if cue == "" {
		return beat
	}
	return cue + " / " + beat
}

func sceneVisualAnchor(anchor string, index, total int) string {
	stage := sceneStage(index, total)
	anchor = strings.TrimSpace(anchor)
	if anchor == "" {
		return stage
	}
	return anchor + " " + stage
}

func sceneBeatLabel(index, total int) string {
	switch index {
	case 0:
		return fmt.Sprintf("scene beat %d/%d: establish this section's emotion and motion", index+1, total)
	case total - 1:
		return fmt.Sprintf("scene beat %d/%d: resolve the section and prepare the next visual transition", index+1, total)
	default:
		return fmt.Sprintf("scene beat %d/%d: escalate the movement and emotional intensity", index+1, total)
	}
}

func sceneStage(index, total int) string {
	switch index {
	case 0:
		return fmt.Sprintf("Scene beat %d/%d: establishing keyframe, wider cinematic framing, clear environment, the protagonist's pose begins the motion.", index+1, total)
	case total - 1:
		return fmt.Sprintf("Scene beat %d/%d: transition keyframe, changed camera angle and lighting, the protagonist lands in a pose that can connect to the next section.", index+1, total)
	default:
		variants := []string{
			"closer camera framing, stronger facial emotion, visible hair and costume motion, brighter accent lighting.",
			"dynamic side-angle framing, the protagonist moves through the scene, particles and background motion intensify.",
			"low-angle dramatic framing, peak gesture, sharper contrast and a more energetic camera move.",
		}
		return fmt.Sprintf("Scene beat %d/%d: %s", index+1, total, variants[(index-1)%len(variants)])
	}
}
