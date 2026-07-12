package filter

import (
	"context"
	"fmt"
	"math"
	"strings"

	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"github.com/shouni/ap-mv/internal/domain"
)

// SceneSplitFilter expands long cuts before keyframe generation so each sub-cut can receive its
// own keyframe and video direction instead of sharing one section-level image.
// UsePreviousVideo should match VideoGenerationFilter.UsePreviousVideo. When true, long cuts are
// split into balanced scene blocks that each become a fresh video-to-video chain base instead of
// generating a new keyframe for every 8-second video cut.
type SceneSplitFilter struct {
	UsePreviousVideo bool
}

func (SceneSplitFilter) Name() string { return "scene_split" }

func (f SceneSplitFilter) Execute(_ context.Context, fc *Context) error {
	if fc == nil {
		return fmt.Errorf("scene split requires recipe")
	}
	if err := ensureVideoRecipe(fc); err != nil {
		return err
	}
	fc.VideoRecipe.Normalize()
	applyLyricsToVideoRecipeCuts(fc.VideoRecipe)
	if f.UsePreviousVideo {
		fc.VideoRecipe.Cuts = expandCutsForVideoToVideoScenes(fc.VideoRecipe.Cuts)
	} else {
		fc.VideoRecipe.Cuts = expandCutsForKeyframeScenes(fc.VideoRecipe.Cuts)
	}

	recipe, err := toDomainRecipe(fc.VideoRecipe)
	if err != nil {
		return err
	}
	fc.Recipe = recipe
	return nil
}

func expandCutsForKeyframeScenes(cuts []orchestrator.Cut) []orchestrator.Cut {
	expanded := make([]orchestrator.Cut, 0, len(cuts))
	for _, cut := range cuts {
		cut = resetCutForSceneKeyframe(cut)
		subCuts := splitCutBySupportedDurations(cut, veoSupportedDurationsSec)
		if len(subCuts) == 1 {
			expanded = append(expanded, subCuts[0])
			continue
		}
		lines := splitDialogueLines(cut.Dialogue)
		for i := range subCuts {
			subCuts[i].AudioCue = sceneAudioCue(cut.AudioCue, i, len(subCuts))
			subCuts[i].VisualAnchor = sceneVisualAnchor(cut.VisualAnchor, i, len(subCuts))
			subCuts[i].Dialogue = domain.DistributeLines(lines, i, len(subCuts))
		}
		expanded = append(expanded, subCuts...)
	}
	for i := range expanded {
		expanded[i].CutIndex = i + 1
	}
	return expanded
}

func expandCutsForVideoToVideoScenes(cuts []orchestrator.Cut) []orchestrator.Cut {
	expanded := make([]orchestrator.Cut, 0, len(cuts))
	for _, cut := range cuts {
		cut = resetCutForSceneKeyframe(cut)
		duration := cut.DurationSec
		if duration <= 0 {
			duration = cut.EndSec - cut.StartSec
		}
		durations := balancedVideoToVideoSceneDurations(duration)
		if len(durations) == 1 {
			cut.DurationSec = durations[0]
			cut.EndSec = cut.StartSec + cut.DurationSec
			expanded = append(expanded, cut)
			continue
		}
		lines := splitDialogueLines(cut.Dialogue)
		offset := 0.0
		for i, d := range durations {
			sub := cut
			sub.StartSec = cut.StartSec + offset
			sub.DurationSec = d
			sub.EndSec = sub.StartSec + d
			sub.AudioCue = sceneAudioCue(cut.AudioCue, i, len(durations))
			sub.VisualAnchor = sceneVisualAnchor(cut.VisualAnchor, i, len(durations))
			sub.Dialogue = domain.DistributeLines(lines, i, len(durations))
			if i > 0 {
				sub.IsSectionStart = true
			}
			expanded = append(expanded, sub)
			offset += d
		}
	}
	for i := range expanded {
		expanded[i].CutIndex = i + 1
	}
	return expanded
}

func balancedVideoToVideoSceneDurations(duration float64) []float64 {
	if duration <= 0 {
		return []float64{veoMaxCutDurationSec}
	}
	if duration <= veoMaxCutDurationSec {
		return []float64{snapToSupportedDuration(duration, veoSupportedDurationsSec)}
	}

	candidates := []float64{8, 15, 22}
	count := int(math.Ceil(duration / 22))
	if count < 1 {
		count = 1
	}

	best := make([]float64, count)
	bestOverage := math.Inf(1)
	bestImbalance := math.Inf(1)
	current := make([]float64, count)
	var search func(pos int)
	search = func(pos int) {
		if pos == count {
			sum, minValue, maxValue := 0.0, current[0], current[0]
			for _, value := range current {
				sum += value
				if value < minValue {
					minValue = value
				}
				if value > maxValue {
					maxValue = value
				}
			}
			if sum < duration {
				return
			}
			overage := sum - duration
			imbalance := maxValue - minValue
			if overage < bestOverage || (overage == bestOverage && imbalance < bestImbalance) {
				copy(best, current)
				bestOverage = overage
				bestImbalance = imbalance
			}
			return
		}
		for _, candidate := range candidates {
			current[pos] = candidate
			search(pos + 1)
		}
	}
	search(0)
	return best
}

func resetCutForSceneKeyframe(cut orchestrator.Cut) orchestrator.Cut {
	cut.Status = orchestrator.CutStatusPending
	cut.KeyframeReference = ""
	cut.VideoID = ""
	cut.VideoURL = ""
	cut.IsChainStart = false
	cut.IsSectionStart = false
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
	switch {
	case index == 0:
		return fmt.Sprintf("scene beat %d/%d: establish this section's emotion and motion", index+1, total)
	case index == total-1:
		return fmt.Sprintf("scene beat %d/%d: resolve the section and prepare the next visual transition", index+1, total)
	default:
		return fmt.Sprintf("scene beat %d/%d: escalate the movement and emotional intensity", index+1, total)
	}
}

func sceneStage(index, total int) string {
	switch {
	case index == 0:
		return fmt.Sprintf("Scene beat %d/%d: establishing keyframe, wider cinematic framing, clear environment, the protagonist's pose begins the motion.", index+1, total)
	case index == total-1:
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
