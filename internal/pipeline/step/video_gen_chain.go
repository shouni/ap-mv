package step

import (
	"context"
	"fmt"
	"strings"

	"github.com/shouni/go-veo-orchestrator/video"
)

// このファイルは video_extension（video-to-video 継続）チェーンの品質維持処理を集めています:
// チェーンリセット時の最終フレーム引き継ぎと、継続生成で蓄積する彩度ドリフトの補正です。
// 生成ループ本体は video_gen.go、プロンプト・lastFrame 選択は video_gen_prompt.go を
// 参照してください。

// applyChainResetKeyframe は、チェーンリセット後の新規ベースカットの参照画像を、静的な
// 立ち絵ではなく直前チェーンの実際の生成結果の最終フレームへ差し替えます。VideoProcessor
// が未設定、または直前動画のURLが空の場合は何もしません（従来通り静的な立ち絵のまま生成）。
func (f VideoGenerationStep) applyChainResetKeyframe(ctx context.Context, sc *Context, cut *video.Cut, previousVideoURL string) error {
	if f.VideoProcessor == nil || strings.TrimSpace(previousVideoURL) == "" {
		return nil
	}
	destURI := chainFrameDestURI(sc.OutputPath, cut.CutIndex)
	if destURI == "" {
		return nil
	}
	frameURI, err := f.VideoProcessor.ExtractLastFrame(ctx, previousVideoURL, destURI)
	if err != nil {
		return fmt.Errorf("extract last frame for chain reset at cut %d: %w", cut.CutIndex, err)
	}
	cut.KeyframeReference = frameURI
	return nil
}

// chainFrameDestURI はチェーンリセット時に抽出する最終フレーム画像の保存先URIを組み立てます。
func chainFrameDestURI(outputPath string, cutIndex int) string {
	outputPath = strings.TrimRight(strings.TrimSpace(outputPath), "/")
	if outputPath == "" {
		return ""
	}
	return fmt.Sprintf("%s/images/chain_frame_cut_%02d.png", outputPath, cutIndex)
}

// colorCorrectExtensionCut は、video_extension（video-to-video継続）で生成された直後のカットの
// 彩度を、そのカットのシーン用キーフレーム画像（cut.KeyframeReference）へ引き戻します。
// Veo の video_extension は直前の生成結果を条件入力として再利用するため、継続を重ねるたびに
// 彩度がドリフトして蓄積します（実運用で確認済み: 継続1回目で彩度+20%、以降のラウンドでも
// コントラストが単調に増加し続けた）。補正後のVideoURL/VideoIDを次カットのPreviousVideoURIとして
// 使うことで、ドリフトが次世代へ複利的に蓄積するのを防ぎます。
//
// 補正の基準には char.ReferenceURL（キャラクター立ち絵）を使わないこと。あれは白背景に
// 3方向ターンアラウンドを並べたデザインシートで、背景の白が画像全体の平均彩度を大きく
// 引き下げてしまう（実測: SATAVG=5.2、実際のシーンキーフレームは28.6前後）。これを基準にすると
// 動画側が不自然に彩度不足（色褪せた見た目）になる回帰が実際に発生した。cut.KeyframeReference
// は「Cinematic anime style」で描かれた実際のシーン画像で、image_to_video/reference_to_video
// 生成時に使われたのと同じ色味の基準を持つため、こちらを使う。
//
// VideoProcessor未設定、またはカットにキーフレーム参照が無い場合は何もしません
// （従来通り無補正のまま）。
func (f VideoGenerationStep) colorCorrectExtensionCut(ctx context.Context, sc *Context, cut *video.Cut) error {
	if f.VideoProcessor == nil {
		return nil
	}
	referenceURL := strings.TrimSpace(cut.KeyframeReference)
	if referenceURL == "" {
		return nil
	}
	destURI := colorCorrectedVideoDestURI(sc.OutputPath, cut.CutIndex)
	if destURI == "" {
		return nil
	}
	correctedURI, err := f.VideoProcessor.ColorMatchSaturation(ctx, cut.VideoURL, referenceURL, destURI)
	if err != nil {
		return fmt.Errorf("color match saturation for cut %d: %w", cut.CutIndex, err)
	}
	cut.VideoURL = correctedURI
	cut.VideoID = correctedURI
	return nil
}

// colorCorrectedVideoDestURI は彩度補正済み動画の保存先URIを組み立てます。
func colorCorrectedVideoDestURI(outputPath string, cutIndex int) string {
	outputPath = strings.TrimRight(strings.TrimSpace(outputPath), "/")
	if outputPath == "" {
		return ""
	}
	return fmt.Sprintf("%s/videos/cut_%02d_color_corrected.mp4", outputPath, cutIndex)
}
