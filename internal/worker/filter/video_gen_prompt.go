package filter

import (
	"strings"
	"sync"

	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"github.com/shouni/ap-mv/assets"
	"github.com/shouni/ap-mv/internal/ports"
)

// このファイルは動画生成プロンプトの組み立てと、frames_to_video 補間に使う lastFrame
// （次カットのキーフレーム）の選択を集めています。生成ループ本体は video_gen.go を
// 参照してください。

// videoGenGuidance は Veo 生成モード別のプロンプトガイダンスを保持します。
// 埋め込みアセット（コンパイル時に存在が保証される）のため実行時の読み込み失敗は
// 事実上起こらないが、万一欠けてもガイダンス無しで生成を続行する（テストで全モードの
// 存在を検証しているため、欠落はCIで検出される）。
var videoGenGuidance = sync.OnceValue(func() map[string]string {
	prompts, err := assets.LoadVideoGenPrompts()
	if err != nil {
		return map[string]string{}
	}
	return prompts
})

// nextCutLastFrameReference は、cuts[i] の終了フレーム（frames_to_video 補間の lastFrame）
// として使う「次カットのキーフレーム参照」を返します。次カットの開始フレームでこのカットを
// 終えることで、カット境界の繋ぎ（構図・キャラの見た目）を滑らかにします。以下の場合は
// 空文字を返し、従来どおり終了フレーム指定なしで生成します:
//   - 次カットが無い、または次カットにキーフレームが無い
//   - 次カットがセクション境界（意図的な場面転換なので、現カットの絵を次セクションの構図へ
//     寄せない。applyChainResetKeyframe が境界で絵を引き継がないのと同じ判断）
//   - キャラクターが異なる（補間中にキャラ同士がモーフィングしてしまう）
//   - 現カットと同一のキーフレーム（尺分割で同じキーフレームを共有するカット等。
//     終了フレーム = 開始フレームを強制すると動きが殺される）
func nextCutLastFrameReference(cuts []orchestrator.Cut, i int) string {
	if i+1 >= len(cuts) {
		return ""
	}
	cur := cuts[i]
	next := cuts[i+1]
	ref := strings.TrimSpace(next.KeyframeReference)
	if ref == "" || next.IsSectionStart {
		return ""
	}
	if !strings.EqualFold(strings.TrimSpace(cur.CharacterID), strings.TrimSpace(next.CharacterID)) {
		return ""
	}
	if ref == strings.TrimSpace(cur.KeyframeReference) {
		return ""
	}
	return ref
}

// videoPrompt builds the prompt used for video generation, appending the guidance that matches
// how Veo will actually interpret the request (ports.ClassifyVeoRequest の判定結果)。
// 生成モードごとに正しい前提（開始フレームあり・参照画像あり・前クリップ継続）を伝える
// ことで、存在しない入力への言及（例: video_extension で「参照画像に合わせろ」）を
// 避けます。VisualAnchor と AudioCue が両方空の場合は従来通り空文字を返し、
// validateVertexVeoRequest の「prompt is required」検証で壊れたレシピを検出できるままにします。
func videoPrompt(cut orchestrator.Cut, mode ports.VeoGenerationMode) string {
	anchor := strings.TrimSpace(cut.VisualAnchor)
	cue := strings.TrimSpace(cut.AudioCue)
	if anchor == "" && cue == "" {
		return ""
	}
	parts := make([]string, 0, 3)
	if anchor != "" {
		parts = append(parts, anchor)
	}
	if cue != "" {
		parts = append(parts, "Synchronize motion and camera timing with audio cue: "+cue)
	}
	if guidance := strings.TrimSpace(videoGenGuidance()[string(mode)]); guidance != "" {
		parts = append(parts, guidance)
	}
	return strings.Join(parts, "\n")
}
