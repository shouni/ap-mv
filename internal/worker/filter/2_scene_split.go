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
			// resetCutForSceneKeyframe already forced IsSectionStart=false above, so this is
			// the only place it gets set: sub-block 2+ becomes a fresh keyframe/chain base that
			// downstream video-gen must not treat as a video_extension continuation of block 1.
			if i > 0 {
				sub.IsSectionStart = true
			} else {
				sub.IsSectionStart = false
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

	count := max(int(math.Ceil(duration/22)), 1)

	// Only the *count* of each candidate value matters for sum/overage/imbalance, so instead of
	// enumerating all 3^count ordered sequences (which blows up badly past count~15, confirmed by
	// benchmark: ~1.7s at count=17, worse than exponentially beyond that), enumerate the O(count^2)
	// (c8, c15) combinations directly (c22 is derived). Ties are broken by filling ascending
	// (8s, then 15s, then 22s) to match the smallest-first sequence the old DFS would have found
	// first (DFS tried candidates in {8,15,22} order at each position, so the lexicographically
	// smallest, i.e. ascending-sorted, permutation of any winning multiset was always found first).
	best := make([]float64, count)
	for i := range best {
		best[i] = 22
	}
	bestOverage := math.Inf(1)
	bestImbalance := math.Inf(1)
	for c8 := 0; c8 <= count; c8++ {
		for c15 := 0; c15 <= count-c8; c15++ {
			c22 := count - c8 - c15
			sum := float64(c8)*8 + float64(c15)*15 + float64(c22)*22
			if sum < duration {
				continue
			}
			overage := sum - duration
			minValue, maxValue := 22.0, 8.0
			if c8 > 0 {
				minValue, maxValue = math.Min(minValue, 8), math.Max(maxValue, 8)
			}
			if c15 > 0 {
				minValue, maxValue = math.Min(minValue, 15), math.Max(maxValue, 15)
			}
			if c22 > 0 {
				minValue, maxValue = math.Min(minValue, 22), math.Max(maxValue, 22)
			}
			imbalance := maxValue - minValue
			if overage < bestOverage || (overage == bestOverage && imbalance < bestImbalance) {
				bestOverage = overage
				bestImbalance = imbalance
				idx := 0
				for range c8 {
					best[idx] = 8
					idx++
				}
				for range c15 {
					best[idx] = 15
					idx++
				}
				for range c22 {
					best[idx] = 22
					idx++
				}
			}
		}
	}
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
