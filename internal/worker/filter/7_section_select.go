package filter

import (
	"context"
	"fmt"
	"math"
	"strings"

	orchestrator "github.com/shouni/go-veo-orchestrator/ports"
)

// SectionSelectFilter は、レシピを fc.Task.SectionIndex で指定されたセクションに属する
// カット群だけへ絞り込むパイプラインステップです。後続の VideoGenerationFilter が
// セクション内カットのみを動画化することで、ショート動画を生成します。
type SectionSelectFilter struct{}

// Name returns the receiver name.
func (SectionSelectFilter) Name() string { return "section_select" }

// Execute trims the recipe to the cuts whose StartSec falls inside the selected section.
func (SectionSelectFilter) Execute(_ context.Context, fc *Context) error {
	if fc == nil || fc.Task == nil || fc.Task.SectionIndex == nil {
		return fmt.Errorf("section_select requires task with section_index")
	}
	if err := ensureVideoRecipe(fc); err != nil {
		return err
	}
	fc.VideoRecipe.Normalize()
	// 保存済みレシピに歌詞由来の dialogue が未設定の場合（旧ジョブ含む）に備えて補完する。
	applyLyricsToVideoRecipeCuts(fc.VideoRecipe)

	sections := fc.VideoRecipe.MusicRecipe.Sections
	sectionIndex := *fc.Task.SectionIndex
	if sectionIndex < 0 || sectionIndex >= len(sections) {
		return fmt.Errorf("section_index %d is out of range (recipe has %d sections)", sectionIndex, len(sections))
	}
	start, end := sectionTimeRange(sections, sectionIndex)

	// 保存済みメタデータの keyframe_reference は元ジョブ相対パスの場合があるため、
	// 新ジョブの出力パスで動画化する前に元ジョブのルートで絶対URI化する。
	originalBase := originalJobOutputPath(fc.Task.RecipeURL)

	cuts := make([]orchestrator.Cut, 0, len(fc.VideoRecipe.Cuts))
	for _, cut := range fc.VideoRecipe.Cuts {
		if cut.StartSec < start || cut.StartSec >= end {
			continue
		}
		// 元ジョブで生成済みのカットも、ショート動画はタスク指定のモデル・アスペクト比で
		// 生成し直すため、生成状態を初期化する。
		cut.Status = orchestrator.CutStatusPending
		cut.VideoID = ""
		cut.VideoURL = ""
		cut.KeyframeReference = resolveRecipeObjectURI(originalBase, cut.KeyframeReference)
		cuts = append(cuts, cut)
	}
	if len(cuts) == 0 {
		return fmt.Errorf("no cuts found in section %d (%s)", sectionIndex, sections[sectionIndex].Name)
	}
	// Veo の image_to_video はカット尺 4/6/8 秒しか受け付けないため、セクション尺のまま
	// 保存された長いカット（キーフレームのみ生成したレシピ等）は 8 秒以下のサブカットへ
	// 分割し、各尺をサポート値に丸めてから動画生成へ渡す。さらに YouTube ショートの
	// 上限（60秒）に収まるよう、超過分のカットは切り詰める。
	fc.VideoRecipe.Cuts = capCutsTotalDuration(expandCutsToSupportedDurations(cuts), youtubeShortMaxDurationSec)

	recipe, err := toDomainRecipe(fc.VideoRecipe)
	if err != nil {
		return err
	}
	fc.Recipe = recipe
	return nil
}

// sectionTimeRange はセクションの正規化済み時間範囲 [start, end) を返します。
// EndSeconds が未設定のセクションは Duration から補完します
// （domain.ApplyLyricsToVideoRecipeCuts のカット割り当てと同じ規則）。
func sectionTimeRange(sections []orchestrator.Section, index int) (float64, float64) {
	sec := sections[index]
	start := float64(sec.StartSeconds)
	end := float64(sec.EndSeconds)
	if end <= start && sec.Duration > 0 {
		end = start + float64(sec.Duration)
	}
	return start, end
}

// veoSupportedDurationsSec は Veo image_to_video が受け付けるカット尺（秒）の昇順リストです。
var veoSupportedDurationsSec = []float64{4, 6, 8}

// veoMaxCutDurationSec は Veo image_to_video の最大カット尺（秒）です。
const veoMaxCutDurationSec = 8.0

// youtubeShortMaxDurationSec は YouTube ショート動画の最大尺（秒）です。
const youtubeShortMaxDurationSec = 60.0

// capCutsTotalDuration は合計尺が maxSec を超えないよう、超過するカット以降を切り詰めます。
// 少なくとも先頭の1カットは残します。
func capCutsTotalDuration(cuts []orchestrator.Cut, maxSec float64) []orchestrator.Cut {
	total := 0.0
	for i, cut := range cuts {
		if i > 0 && total+cut.DurationSec > maxSec {
			return cuts[:i]
		}
		total += cut.DurationSec
	}
	return cuts
}

// expandCutsToSupportedDurations は各カットの尺を Veo のサポート値へ正規化します。
// 8 秒を超えるカットは同じキーフレーム・プロンプトを引き継いだサブカット列へ分割し、
// 歌詞（Dialogue）は行単位でサブカットへ均等配分します。分割後は CutIndex を 1 から振り直します。
func expandCutsToSupportedDurations(cuts []orchestrator.Cut) []orchestrator.Cut {
	expanded := make([]orchestrator.Cut, 0, len(cuts))
	for _, cut := range cuts {
		expanded = append(expanded, splitCutBySupportedDurations(cut)...)
	}
	for i := range expanded {
		expanded[i].CutIndex = i + 1
	}
	return expanded
}

// splitCutBySupportedDurations は1カットをサポート尺のサブカット列へ分割します。
// 生成済みカットは実動画の尺と metadata がずれないよう、そのまま返します。
func splitCutBySupportedDurations(cut orchestrator.Cut) []orchestrator.Cut {
	if cut.IsGenerated() {
		return []orchestrator.Cut{cut}
	}
	duration := cut.DurationSec
	if duration <= 0 {
		duration = cut.EndSec - cut.StartSec
	}
	if duration <= veoMaxCutDurationSec {
		cut.DurationSec = snapToSupportedDuration(duration)
		cut.EndSec = cut.StartSec + cut.DurationSec
		return []orchestrator.Cut{cut}
	}

	var subCuts []orchestrator.Cut
	offset := 0.0
	for remaining := duration; remaining > 0; {
		d := veoMaxCutDurationSec
		if remaining < veoMaxCutDurationSec {
			d = snapToSupportedDuration(remaining)
		}
		sub := cut
		sub.StartSec = cut.StartSec + offset
		sub.DurationSec = d
		sub.EndSec = sub.StartSec + d
		subCuts = append(subCuts, sub)
		offset += d
		remaining -= d
	}

	lines := splitDialogueLines(cut.Dialogue)
	for i := range subCuts {
		subCuts[i].Dialogue = dialogueForSubCut(lines, i, len(subCuts))
	}
	return subCuts
}

// snapToSupportedDuration は尺を Veo のサポート値のうち最も近いものへ丸めます（同距離なら長い方）。
func snapToSupportedDuration(duration float64) float64 {
	best := veoSupportedDurationsSec[0]
	for _, candidate := range veoSupportedDurationsSec {
		if math.Abs(duration-candidate) <= math.Abs(duration-best) {
			best = candidate
		}
	}
	return best
}

// splitDialogueLines は歌詞テキストを空行を除いた行スライスへ分解します。
func splitDialogueLines(dialogue string) []string {
	var lines []string
	for _, line := range strings.Split(dialogue, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// dialogueForSubCut は歌詞行をサブカット数で均等配分し、pos 番目の担当行を返します。
func dialogueForSubCut(lines []string, pos, total int) string {
	if len(lines) == 0 {
		return ""
	}
	if total <= 1 {
		return strings.Join(lines, "\n")
	}
	n := len(lines)
	start := pos * n / total
	end := (pos + 1) * n / total
	if start >= end {
		return ""
	}
	return strings.Join(lines[start:end], "\n")
}

// resolveRecipeObjectURI は元ジョブ相対のオブジェクト参照を絶対URIへ解決します。
// すでにスキーム付きの参照、または base が導出できない場合はそのまま返します。
// base の末尾スラッシュ有無に依存せず安全に連結します。
func resolveRecipeObjectURI(base, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" || strings.Contains(ref, "://") || base == "" {
		return ref
	}
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(ref, "/")
}
