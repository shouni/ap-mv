package filter

import (
	"math"
	"strings"

	characterkit "github.com/shouni/go-character-kit/character"
	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"github.com/shouni/ap-mv/internal/domain"
	"github.com/shouni/ap-mv/internal/ports"
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
// カットの所属セクションは cut.SectionIndex（go-veo-orchestrator v1.7.0 で追加、
// VideoRecipe.Normalize が StartSec から自動補完）をそのまま使います。曲のセクションが
// 変わる境目では（技術的な累積尺上限に達していなくても）チェーンをリセットします。
// 技術的リセットとの違いは IsSectionStart フラグで示され、runDirect はこのフラグが立っている
// カットについて直前チェーンの最終フレーム引き継ぎ（applyChainResetKeyframe）をスキップします
// （セクションが変わる以上、直前セクションの絵をそのまま引き継ぐべきではないため、そのカット
// 自身に割り当てられたキーフレーム参照をそのまま使う）。
//
// characters と referenceImagesSupported は、各カットが reference_to_video（referenceImages、
// 8秒固定）と image_to_video（{4,6,8}秒）のどちらで生成されるかを判定するために使います。
// 判定は video_gen.go の buildReferenceImages と同じ規則（キャラクターに参照アートがある、
// またはカットにキーフレーム参照がある）に、使用モデルが referenceImages に対応しているかを
// 掛け合わせたものです（詳細は cutUsesReferenceImages）。
//
// SectionSelectFilter（ショート動画）と VideoGenerationFilter（フルMV）の両方から使われます。
func expandCutsToSupportedDurations(cuts []orchestrator.Cut, usePreviousVideo bool, characters *characterkit.Characters, referenceImagesSupported bool) []orchestrator.Cut {
	expanded := make([]orchestrator.Cut, 0, len(cuts))
	for _, cut := range cuts {
		subCuts := splitCutBySupportedDurations(cut, allowedDurationsFor(cut, characters, referenceImagesSupported))
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
		// SectionIndex は cut.SectionIndex（1始まり、0は未割り当て）。分割前カットの
		// SectionIndex はサブカットへコピーされるため（splitCutBySupportedDurations の
		// sub := cut）、分割自体はセクション境界とは見なされません。
		isSectionStart := expanded[i].IsSectionStart ||
			(i > 0 && expanded[i].SectionIndex != 0 && expanded[i].SectionIndex != expanded[i-1].SectionIndex)
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

// allowedDurationsFor は、指定されたカットが reference_to_video（referenceImages）と
// image_to_video のどちらで生成されるかに応じて、Veo がそのカットに対して受け付ける尺
// （秒）の候補リストを返します（referenceImagesSupported が false の場合、モデルが
// referenceImages に対応していないため image_to_video 用の {4,6,8} を返します）。
func allowedDurationsFor(cut orchestrator.Cut, characters *characterkit.Characters, referenceImagesSupported bool) []float64 {
	if cutUsesReferenceImages(cut, characters, referenceImagesSupported) {
		return veoReferenceToVideoDurationsSec
	}
	return veoSupportedDurationsSec
}

// cutUsesReferenceImages は、このカットが Veo の referenceImages（reference_to_video）で
// 生成されるかを返します。generateCut が実際に組むのと同じ参照画像リスト
// （cutReferenceImages）でリクエストを分類する（ports.ClassifyVeoRequest）ため、
// 尺の正規化と実際の生成が使う判定は構造的に一致します。
func cutUsesReferenceImages(cut orchestrator.Cut, characters *characterkit.Characters, referenceImagesSupported bool) bool {
	req := ports.VideoGenerationRequest{
		ImageReference:  cut.KeyframeReference,
		ReferenceImages: cutReferenceImages(cut, characters),
	}
	caps := ports.VeoCapabilities{ReferenceImages: referenceImagesSupported}
	return ports.ClassifyVeoRequest(req, false, caps) == ports.VeoModeReferenceToVideo
}

// cutReferenceImages はキャラクター立ち絵とキーフレームから referenceImages 用 URI リストを
// 組み立てます。generateCut（動画生成リクエスト）と cutUsesReferenceImages（尺の正規化）の
// 両方がこの1つの規則を共有します。
func cutReferenceImages(cut orchestrator.Cut, characters *characterkit.Characters) []string {
	var refs []string
	if characters != nil {
		if char := characters.GetCharacter(strings.TrimSpace(cut.CharacterID)); char != nil {
			if ref := strings.TrimSpace(char.ReferenceURL); ref != "" {
				refs = append(refs, ref)
			}
		}
	}
	if ref := strings.TrimSpace(cut.KeyframeReference); ref != "" {
		refs = append(refs, ref)
	}
	return refs
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
