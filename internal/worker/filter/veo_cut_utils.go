package filter

import (
	"math"
	"strings"

	characterkit "github.com/shouni/go-character-kit/character"
	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"github.com/shouni/ap-mv/internal/domain"
)

// veoSupportedDurationsSec は Veo image_to_video が受け付けるカット尺（秒）の昇順リストです。
var veoSupportedDurationsSec = []float64{4, 6, 8}

// veoReferenceToVideoDurationsSec は Veo reference_to_video（referenceImages）が受け付ける
// 唯一のカット尺（秒）です。image_to_video の {4,6,8} とは異なるサポート値のため、
// 個別に定義しています。
var veoReferenceToVideoDurationsSec = []float64{8}

// veoMaxCutDurationSec は Veo image_to_video の最大カット尺（秒）です。
const veoMaxCutDurationSec = 8.0

// veoVideoExtensionDurationSec は Veo の video_extension（video-to-video、前カット動画を
// PreviousVideoID として引き継ぐ生成）が受け付ける唯一のカット尺（秒）です。
// image_to_video の {4,6,8} とは異なるサポート値のため、個別に定義しています。
const veoVideoExtensionDurationSec = 7.0

// veoContinuationMaxDurationSec は継続チェーンの累積尺（秒）がこの値に達する手前で
// 新しいチェーンへリセットする閾値です（動画を打ち切るのではなく、そのカットを
// PreviousVideoIDなしの新規ベースとして生成し直す）。
//
// Veo の video_extension が「前の動画」として受け付けられる累積尺の実際の上限は30秒
// （実運用で確認済み: 累積29秒(cut4)までの動画をPreviousVideoIDとして渡す継続生成は成功するが、
// 累積36秒(cut5)の動画を渡すと "Video duration 36 seconds exceeds the maximum duration 30
// seconds" (code=3) で失敗する）。しかし video_extension は前回の生成結果を条件入力として
// 再利用する性質上、継続を重ねるたびに彩度・コントラストがドリフトして蓄積する
// （実ジョブで確認: 継続1回目で彩度+20%、以降のラウンドでもコントラストが単調に増加し続けた）。
// そのためAPI上限の30秒ではなく、より低いこの値で早めにリセットし、1チェーンあたりの
// 継続回数（＝ドリフトの蓄積量）を抑える。
const veoContinuationMaxDurationSec = 24.0

// expandCutsToSupportedDurations は各カットの尺を Veo のサポート値へ正規化します。
// 8 秒を超えるカットは同じキーフレーム・プロンプトを引き継いだサブカット列へ分割し、
// 歌詞（Dialogue）は行単位でサブカットへ均等配分します。分割後は CutIndex を 1 から振り直します。
//
// usePreviousVideo が true の場合、原則として先頭カット以降（PreviousVideoID を伴い
// video_extension で生成される想定のカット）は image_to_video 用の {4,6,8} ではなく 7 秒固定へ
// 揃えます。ただし video_extension は「前の動画」として渡せる累積尺に上限
// (veoContinuationMaxDurationSec) があり、これを超えると Veo 側が
// "Video duration N seconds exceeds the maximum duration 30 seconds" (code=3) で拒否するため、
// 累積尺が上限に達する手前でチェーンをリセットします。リセットされたカットは
// PreviousVideoID を使わない新規ベース（image_to_video、{4,6,8}秒）として扱われ、
// そこから新しい継続チェーンが始まります（runDirect の lastVideoID リセット処理と対）。
// 生成済みカットは実動画の尺と metadata がずれないよう変更しませんが、累積尺の計算には
// 含めます（再開時にチェーン状態を正しく引き継ぐため）。
//
// sections が与えられている場合、曲のセクションが変わる境目でも（技術的な累積尺上限に
// 達していなくても）チェーンをリセットします。技術的リセットとの違いは IsSectionStart
// フラグで示され、runDirect はこのフラグが立っているカットについて直前チェーンの最終
// フレーム引き継ぎ（applyChainResetKeyframe）をスキップします（セクションが変わる以上、
// 直前セクションの絵をそのまま引き継ぐべきではないため、そのカット自身に割り当てられた
// キーフレーム参照をそのまま使う）。
//
// characters と referenceImagesSupported は、各カットが reference_to_video（referenceImages、
// 8秒固定）と image_to_video（{4,6,8}秒）のどちらで生成されるかを判定するために使います。
// 判定は 3_video_gen.go の buildReferenceImages と同じ規則（キャラクターに参照アートがある、
// またはカットにキーフレーム参照がある）に、使用モデルが referenceImages に対応しているかを
// 掛け合わせたものです（詳細は cutUsesReferenceImages）。
//
// SectionSelectFilter（ショート動画）と VideoGenerationFilter（フルMV）の両方から使われます。
func expandCutsToSupportedDurations(cuts []orchestrator.Cut, usePreviousVideo bool, sections []orchestrator.Section, characters *characterkit.Characters, referenceImagesSupported bool) []orchestrator.Cut {
	expanded := make([]orchestrator.Cut, 0, len(cuts))
	// sectionAt[i] は expanded[i] の元になった分割前カットの所属セクション index です。
	// 1つの長いカットが複数のサブカットへ分割されても、分割自体はセクション境界とは
	// 見なしません（サブカット群はすべて同じ元カットのセクションを引き継ぎます）。
	sectionAt := make([]int, 0, len(cuts))
	for _, cut := range cuts {
		subCuts := splitCutBySupportedDurations(cut, allowedDurationsFor(cut, characters, referenceImagesSupported))
		sIdx := sectionIndexForStartSec(sections, cut.StartSec)
		for range subCuts {
			sectionAt = append(sectionAt, sIdx)
		}
		expanded = append(expanded, subCuts...)
	}
	cumulative := 0.0
	for i := range expanded {
		expanded[i].CutIndex = i + 1
		if !usePreviousVideo {
			continue
		}
		if expanded[i].IsGenerated() {
			// 生成済みカットがそれ自体チェーンの起点（IsChainStart、先頭カットまたは
			// 過去のリセット）だった場合、累積尺はそのカット自身の尺から数え直す。
			// 常に加算するだけだと、一度リセットが起きた後もそれ以前のチェーンの
			// 累積尺を引きずり続けてしまい、以降のカットが（実際には累積尺に余裕が
			// あるのに）毎回誤ってリセット扱いになる（再開のたびに1カットずつ
			// 処理される runDirect の性質上、この関数は毎回全カットを再計算する）。
			if i == 0 || expanded[i].IsChainStart {
				cumulative = expanded[i].DurationSec
			} else {
				cumulative += expanded[i].DurationSec
			}
			continue
		}
		isSectionStart := expanded[i].IsSectionStart || (i > 0 && sectionAt[i] >= 0 && sectionAt[i] != sectionAt[i-1])
		if isSectionStart {
			cumulative = 0
		}
		if cumulative == 0 || cumulative+veoVideoExtensionDurationSec > veoContinuationMaxDurationSec {
			// 新規チェーンの先頭（曲頭、セクション境界、またはリセット直後）。
			// splitCutBySupportedDurations が既に割り当てた尺（image_to_videoなら{4,6,8}秒、
			// reference_to_videoなら8秒固定）をそのまま使う。
			cumulative = expanded[i].DurationSec
			if isSectionStart {
				expanded[i].IsSectionStart = true
			}
			continue
		}
		expanded[i].DurationSec = veoVideoExtensionDurationSec
		expanded[i].EndSec = expanded[i].StartSec + veoVideoExtensionDurationSec
		cumulative += veoVideoExtensionDurationSec
	}
	return expanded
}

// sectionIndexForStartSec は startSec が属するセクションの index を返します。
// 各セクションの StartSeconds のうち startSec 以下で最大のものを採用するため、
// duration正規化による数秒のズレ（EndSecondsとの間の隙間）があっても頑健に判定できます。
// sections の並び順（StartSeconds昇順であるはず、という暗黙の前提）には依存せず、
// 常にStartSeconds自体の大小で判定します。一致するセクションが無い場合は -1 を返します。
func sectionIndexForStartSec(sections []orchestrator.Section, startSec float64) int {
	bestIndex := -1
	bestStart := -1.0
	for i, s := range sections {
		start := float64(s.StartSeconds)
		if start <= startSec && start >= bestStart {
			bestIndex = i
			bestStart = start
		}
	}
	return bestIndex
}

// allowedDurationsFor は、指定されたカットが reference_to_video（referenceImages）と
// image_to_video のどちらで生成されるかに応じて、Veo がそのカットに対して受け付ける尺
// （秒）の候補リストを返します。判定規則は 3_video_gen.go の buildReferenceImages と
// 揃えています（referenceImagesSupported が false の場合、モデルが referenceImages に
// 対応していないため image_to_video 用の {4,6,8} を返します）。
func allowedDurationsFor(cut orchestrator.Cut, characters *characterkit.Characters, referenceImagesSupported bool) []float64 {
	if cutUsesReferenceImages(cut, characters, referenceImagesSupported) {
		return veoReferenceToVideoDurationsSec
	}
	return veoSupportedDurationsSec
}

// cutUsesReferenceImages は、このカットが Veo の referenceImages（reference_to_video）で
// 生成されるかを返します。3_video_gen.go の buildReferenceImages と同じ規則（キャラクターに
// 参照アートがある、またはカット自体にキーフレーム参照がある）を使い、それに加えて使用
// モデルが referenceImages に対応しているかを掛け合わせます（Fast モデル等、非対応の場合は
// image_to_video へフォールバックするため、常に image_to_video 用の {4,6,8} を使ってよい）。
func cutUsesReferenceImages(cut orchestrator.Cut, characters *characterkit.Characters, referenceImagesSupported bool) bool {
	if !referenceImagesSupported {
		return false
	}
	if characters != nil {
		if char := characters.GetCharacter(strings.TrimSpace(cut.CharacterID)); char != nil && strings.TrimSpace(char.ReferenceURL) != "" {
			return true
		}
	}
	return strings.TrimSpace(cut.KeyframeReference) != ""
}

// splitCutBySupportedDurations は1カットをサポート尺のサブカット列へ分割します。
// 生成済みカットは実動画の尺と metadata がずれないよう、そのまま返します。
// allowedDurations は使用する尺の候補リストで、allowedDurationsFor が呼び出し元で
// 事前に決定します（reference_to_videoなら{8}、image_to_videoなら{4,6,8}）。
func splitCutBySupportedDurations(cut orchestrator.Cut, allowedDurations []float64) []orchestrator.Cut {
	if cut.IsGenerated() {
		return []orchestrator.Cut{cut}
	}
	duration := cut.DurationSec
	if duration <= 0 {
		duration = cut.EndSec - cut.StartSec
	}
	if duration <= veoMaxCutDurationSec {
		cut.DurationSec = snapToSupportedDuration(duration, allowedDurations)
		cut.EndSec = cut.StartSec + cut.DurationSec
		return []orchestrator.Cut{cut}
	}

	var subCuts []orchestrator.Cut
	offset := 0.0
	for remaining := duration; remaining > 0; {
		d := veoMaxCutDurationSec
		if remaining < veoMaxCutDurationSec {
			d = snapToSupportedDuration(remaining, allowedDurations)
		}
		sub := cut
		if len(subCuts) > 0 {
			sub.IsChainStart = false
			sub.IsSectionStart = false
		}
		sub.StartSec = cut.StartSec + offset
		sub.DurationSec = d
		// Note: d が切り上げ方向へ丸められた場合、sub.EndSec は元の cut.EndSec を超過し、
		// 後続カットとタイムライン上でオーバーラップする可能性がありますが、
		// Veo の個別カット生成としては問題ないため仕様として許容しています。
		sub.EndSec = sub.StartSec + d
		subCuts = append(subCuts, sub)
		offset += d
		remaining -= d
	}

	lines := splitDialogueLines(cut.Dialogue)
	for i := range subCuts {
		subCuts[i].Dialogue = domain.DistributeLines(lines, i, len(subCuts))
	}
	return subCuts
}

// snapToSupportedDuration は尺を candidates のうち最も近いものへ丸めます（同距離なら長い方）。
func snapToSupportedDuration(duration float64, candidates []float64) float64 {
	best := candidates[0]
	for _, candidate := range candidates {
		if math.Abs(duration-candidate) <= math.Abs(duration-best) {
			best = candidate
		}
	}
	return best
}

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
