package filter

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"

	characterkit "github.com/shouni/go-character-kit/character"
	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"github.com/shouni/ap-mv/internal/domain"
	"github.com/shouni/ap-mv/internal/ports"
)

// SceneSplitFilter expands long cuts before keyframe generation so each sub-cut can receive its
// own keyframe and video direction instead of sharing one section-level image.
// UsePreviousVideo should match VideoGenerationFilter.UsePreviousVideo. When true, cuts are
// re-allocated into chain blocks that each become a fresh video-to-video chain base instead of
// generating a new keyframe for every 8-second video cut.
//
// Duration allocation treats the song timeline as the source of truth: Veo only supports a
// discrete set of durations, so each cut's rounding error is carried into the next cut's target
// (error diffusion) instead of being rounded up per cut, and StartSec/EndSec are re-based to the
// concatenated video timeline so cuts never overlap.
type SceneSplitFilter struct {
	UsePreviousVideo bool
	// Runner mirrors VideoGenerationFilter.Runner and must be wired from the same VideoRunner by
	// the planner. Scene splitting decides each cut's chain base from the same reference_to_video /
	// image_to_video classification VideoGenerationFilter uses, so both filters must resolve the
	// runner (and thus its Veo capabilities) identically — see resolvedVideoRunner.
	Runner ports.VideoRunner
}

// Name returns the receiver name.
func (SceneSplitFilter) Name() string { return "scene_split" }

// resolvedVideoRunner returns the VideoRunner that generation will actually use: f.Runner takes
// priority, falling back to fc.VideoRunner. This mirrors VideoGenerationFilter.resolvedVideoRunner
// so the two filters never disagree on whether referenceImages is supported.
func (f SceneSplitFilter) resolvedVideoRunner(fc *Context) ports.VideoRunner {
	if f.Runner != nil {
		return f.Runner
	}
	return fc.VideoRunner
}

// Execute runs the receiver processing step.
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
		fc.VideoRecipe.Cuts = expandCutsForVideoToVideoScenes(fc.VideoRecipe.Cuts, fc.Characters, referenceImagesSupported(f.resolvedVideoRunner(fc)))
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
	videoEnd := 0.0
	if len(cuts) > 0 {
		videoEnd = cuts[0].StartSec
	}
	for _, cut := range cuts {
		cut = resetCutForSceneKeyframe(cut)
		// 誤差拡散: 前カットまでの丸め誤差（videoEnd と楽曲時刻のズレ）を含めて、
		// このカットの楽曲上の終端に映像の累積尺を合わせにいく。StartSec/EndSec は
		// 連結後の映像タイムラインで振り直すため、カット間のオーバーラップは発生しない。
		target := cutMusicalEndSec(cut) - videoEnd
		cut.StartSec = videoEnd
		cut.DurationSec = target
		cut.EndSec = videoEnd + target
		subCuts := splitCutBySupportedDurations(cut, veoSupportedDurationsSec)
		if len(subCuts) > 1 {
			lines := splitDialogueLines(cut.Dialogue)
			for i := range subCuts {
				subCuts[i].AudioCue = sceneAudioCue(cut.AudioCue, i, len(subCuts))
				subCuts[i].VisualAnchor = sceneVisualAnchor(cut.VisualAnchor, i, len(subCuts))
				subCuts[i].Dialogue = domain.DistributeLines(lines, i, len(subCuts))
			}
		}
		expanded = append(expanded, subCuts...)
		videoEnd = expanded[len(expanded)-1].EndSec
	}
	for i := range expanded {
		expanded[i].CutIndex = i + 1
	}
	return expanded
}

// cutMusicalEndSec は、このカットが楽曲上で終わるべき時刻（秒）を返します。
func cutMusicalEndSec(cut orchestrator.Cut) float64 {
	if cut.EndSec > cut.StartSec {
		return cut.EndSec
	}
	return cut.StartSec + cut.DurationSec
}

// expandCutsForVideoToVideoScenes は、UsePreviousVideo（継続チェーン）方式のカット列を
// チェーンブロック列へ再割り当てします。各ブロックは1本の継続チェーン（ベース1カット +
// video_extension の継続カット列、合計尺は videoToVideoChainDurations の候補値）として
// まるごと生成され、ブロックごとに専用のキーフレームが作られます。ブロックには
// IsChainStart を立て、下流の expandCutsToSupportedDurations がこれを見てブロックを
// [ベース, 7秒, ...] の実生成カット列へ分割し、累積尺の判定でも新規チェーン起点として
// 扱います（計画した尺が下流で書き換えられない）。
//
// 尺の割り当ては楽曲タイムラインを正とし、カットごとの丸め誤差（切り上げ・切り下げ両方向）
// は次カットの目標尺へ持ち越して相殺します（誤差拡散）。割り当て後の StartSec/EndSec は
// 連結後の映像タイムラインで振り直すため、カット間のオーバーラップは発生しません。
// AudioCue のテキストに含まれる時刻表現（例 "0:28 to 0:37"）は歌詞由来の楽曲上の目安時刻
// としてそのまま残します。
//
// characters と referenceImagesSupported は、各カットのチェーンベースが reference_to_video
// （8秒固定）と image_to_video（{4,6,8}秒）のどちらで生成されるかの判定に使います
// （expandCutsToSupportedDurations と同じ cutUsesReferenceImages の規則）。
func expandCutsForVideoToVideoScenes(cuts []orchestrator.Cut, characters *characterkit.Characters, referenceImagesSupported bool) []orchestrator.Cut {
	expanded := make([]orchestrator.Cut, 0, len(cuts))
	videoEnd := 0.0
	if len(cuts) > 0 {
		videoEnd = cuts[0].StartSec
	}
	for _, cut := range cuts {
		// IsChainStart が立っているカットは、前回の SceneSplit が割り当て済みのチェーン
		// ブロックです（draft から読み直した場合や、保存済みレシピを渡された
		// mv_from_keyframe_video_recipe）。そのブロックは下の割り当てで自分自身へ
		// 再割り当てされて必ず単独ブロック（i == 0）になるため、シーンリセットを
		// 添字（i > 0）からは復元できません。ここで拾って引き継ぎます。
		sceneReset := cut.IsChainStart && cut.IsSectionStart
		cut = resetCutForSceneKeyframe(cut)
		// 誤差拡散: 前カットまでの丸め誤差（videoEnd と楽曲時刻のズレ）を含めて、
		// このカットの楽曲上の終端に映像の累積尺を合わせにいく。
		target := cutMusicalEndSec(cut) - videoEnd
		bases := veoSupportedDurationsSec
		if cutUsesReferenceImages(cut, characters, referenceImagesSupported) {
			bases = veoReferenceToVideoDurationsSec
		}
		durations := allocateChainDurations(target, videoToVideoChainDurations(bases))
		lines := splitDialogueLines(cut.Dialogue)
		for i, d := range durations {
			sub := cut
			sub.StartSec = videoEnd
			sub.DurationSec = d
			sub.EndSec = videoEnd + d
			sub.IsChainStart = true
			if len(durations) > 1 {
				sub.AudioCue = sceneAudioCue(cut.AudioCue, i, len(durations))
				sub.VisualAnchor = sceneVisualAnchor(cut.VisualAnchor, i, len(durations))
				sub.Dialogue = domain.DistributeLines(lines, i, len(durations))
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
			// recipe, so re-splitting is idempotent (TestSceneSplitFilterIsIdempotent).
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

// allocateChainDurations は、目標尺 target（秒）をチェーン尺候補 candidates
// （videoToVideoChainDurations の返り値、昇順・整数秒前提）の組み合わせへ割り当てます。
// 優先順位は (1) 合計と目標の差の絶対値が最小（切り上げ・切り下げの両方を許す）、
// (2) ブロック数が少ない（キーフレーム生成とチェーン数の節約）、(3) 合計が短い、です。
// 過不足分は呼び出し元が誤差拡散で次カットの目標へ持ち越すため、ここではこのカット単体の
// 誤差だけを最小化すればよい。返り値は昇順（短いブロックが先）です。
func allocateChainDurations(target float64, candidates []float64) []float64 {
	smallest := candidates[0]
	if target <= smallest {
		return []float64{smallest}
	}
	largest := candidates[len(candidates)-1]
	limit := int(math.Ceil(target + largest))
	const unreachable = math.MaxInt32
	// blockCounts[s] = 合計 s 秒を作る最小ブロック数、choice[s] = そのとき最後に使う候補。
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

// resetCutGenerationState clears a cut's per-generation state so it will be (re)generated:
// Status returns to pending and VideoID/VideoURL/IsChainStart are cleared. When keepKeyframe is
// false it additionally clears KeyframeReference and IsSectionStart (a fresh scene keyframe will
// be generated for this cut); when true both are preserved because the caller reuses the cut's
// stored keyframe reference (e.g. SectionSelectFilter re-generating an existing job's cuts).
func resetCutGenerationState(cut *orchestrator.Cut, keepKeyframe bool) {
	cut.Status = orchestrator.CutStatusPending
	cut.VideoID = ""
	cut.VideoURL = ""
	cut.IsChainStart = false
	if !keepKeyframe {
		cut.KeyframeReference = ""
		cut.IsSectionStart = false
	}
}

func resetCutForSceneKeyframe(cut orchestrator.Cut) orchestrator.Cut {
	resetCutGenerationState(&cut, false)
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
