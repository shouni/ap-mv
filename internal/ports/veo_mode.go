package ports

import (
	veoports "github.com/shouni/go-veo-orchestrator/ports"
)

// このファイルは Veo リクエスト分類を go-veo-orchestrator へ委譲します。
//
// 以前はここに ClassifyVeoRequest の実装をそのまま持っていましたが、同じ判定が
// ライブラリの ports/veo_mode.go にもあり、ロジックが1行も違わない複製になっていました。
// 分類結果はライブラリ側の runner/video.go が送信前の尺検証にも使うため（不一致は
// ErrUnsupportedCutDuration になる）、片方だけ変えると壊れます。定義はライブラリに1つ、
// ap-mv 側は呼び出し名を保つための別名だけ、という形にしています。
// 尺の定数・候補（ImageToVideoDurationsSec / ChainDurations 等）も同じ理由で
// veoports をそのまま使います（internal/worker/filter/veo_cut_utils.go）。

// VeoGenerationMode は、1つの動画生成リクエストが Veo のどの生成機能で解釈されるかを
// 表します。値は assets/prompts/video_gen/ 配下のプロンプトファイル名（拡張子なし）と
// 一致します。
type VeoGenerationMode = veoports.VeoGenerationMode

const (
	// VeoModeImageToVideo はキーフレーム画像を image 入力とする image_to_video です。
	VeoModeImageToVideo = veoports.VeoModeImageToVideo
	// VeoModeFramesToVideo は開始フレームを image 入力、終了フレームを lastFrame 入力と
	// する first/last frame 補間です（Veo 2 / Veo 3.1 系のみ、Fast も対応）。
	VeoModeFramesToVideo = veoports.VeoModeFramesToVideo
	// VeoModeReferenceToVideo は [キャラ立ち絵, キーフレーム] を referenceImages とする
	// reference_to_video です（Veo 3 系の非Fastモデルのみ、8秒固定）。
	VeoModeReferenceToVideo = veoports.VeoModeReferenceToVideo
	// VeoModeVideoExtension は前カット動画を video 入力とする video_extension
	// （video-to-video継続）です。このモードでは画像参照は一切送られません。
	VeoModeVideoExtension = veoports.VeoModeVideoExtension
)

// VeoCapabilities は、実際に使われる Runner／モデルが Veo のオプション機能に対応して
// いるかを表します。ClassifyVeoRequest の入力で、adapter はモデル名から、filter は
// Runner のオプションインターフェースから導出します。
type VeoCapabilities = veoports.VeoCapabilities

// RunnerCapabilities は Runner のオプションインターフェース
// （ReferenceImagesSupporter / LastFrameSupporter）から VeoCapabilities を導出します。
// インターフェースを実装しない Runner（テストダブル等）は各機能とも false になり、
// image_to_video 側へ倒れます。
func RunnerCapabilities(runner VideoRunner) VeoCapabilities {
	return veoports.RunnerCapabilities(runner)
}

// ClassifyVeoRequest は、このリクエストが Veo のどの生成機能で解釈されるかを判定します。
// adapter のリクエスト本文構築と filter のプロンプト・尺選択が同じ判定を共有するための
// 唯一の分岐点で、優先順位は次のとおりです:
//
//  1. video_extension — usePreviousVideo が有効で、PreviousVideoID が gs:// 参照のとき。
//     Veo は video と referenceImages / image を併用できないため、以降の画像参照は
//     すべて無視されます。
//  2. reference_to_video — 参照画像 URI が1つ以上あり、モデルが referenceImages に
//     対応しているとき。
//  3. frames_to_video — 開始フレーム（ImageReference または InputImage）と
//     LastFrameReference が両方あり、モデルが lastFrame に対応しているとき
//     （Veo の lastFrame は image とセットでのみ有効）。
//  4. image_to_video — それ以外すべて。
func ClassifyVeoRequest(req VideoGenerationRequest, usePreviousVideo bool, caps VeoCapabilities) VeoGenerationMode {
	return veoports.ClassifyVeoRequest(req, usePreviousVideo, caps)
}
