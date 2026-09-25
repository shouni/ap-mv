package step

import (
	characterkit "github.com/shouni/go-character-kit/character"
	"github.com/shouni/go-veo-orchestrator/runner"
	"github.com/shouni/go-veo-orchestrator/veo"
	"github.com/shouni/go-veo-orchestrator/video"

	"github.com/shouni/ap-mv/internal/ports"
)

// videoRequestBuilder は runner.VideoRequestBuilder を満たし、このアプリのプロンプトを
// 載せた Veo リクエストを組み立てます。
//
// ライブラリの既定ビルダー（runner.NewVideoRequestBuilderWithCharacters）を使わないのは
// プロンプトのためです。既定は VisualAnchor / AudioCue / Mood / Timeline を素直に並べる
// だけで、このアプリは生成モード別のガイダンス（assets/prompts/video_gen）を足します。
// シードも既定とは違い、キーフレームの UsedSeed ではなくキャラクターのシードを使います
// （keyframe.Generator が常に char.Seed で描くのと揃えるため。videoSeed 参照）。
type videoRequestBuilder struct {
	characters *characterkit.Characters
	// usePreviousVideo は VEO_USE_PREVIOUS_VIDEO 設定です。分類に渡す値であって、
	// このカットに実際に引き継ぐ動画があるかどうか（in.PreviousVideoURI）とは別物です。
	usePreviousVideo bool
}

// Build はカット 1 件分の Veo リクエストを組み立てます。
func (b videoRequestBuilder) Build(in runner.BuildInput) video.GenerationRequest {
	cut := in.Cut
	req := video.GenerationRequest{
		CutIndex:           cut.CutIndex,
		DurationSec:        cut.DurationSec,
		Seed:               characterSeed(b.characters, cut),
		PreviousVideoURI:   in.PreviousVideoURI,
		ImageReference:     cut.KeyframeReference,
		LastFrameReference: in.LastFrameReference,
		ReferenceImages:    video.CutReferenceImages(cut, b.characters),
		AudioReference:     cut.AudioReference,
	}
	mode := veo.ClassifyRequest(req, b.usePreviousVideo, in.Capabilities)
	// このモードで実際には使われない参照はリクエストに残さない。理由は 2 つあります。
	//
	// ログ・再現時のリクエスト内容が adapter の送る内容と一致すること。そして、
	// 引き継がないと決めた PreviousVideoURI を残すと、リクエスト自身から分類し直す側
	// （runner の尺検証、adapter の組み立て）がここでの判断と食い違うことです。
	// 実際、VEO_USE_PREVIOUS_VIDEO が無効なのに前カットの URI が載り続けていました。
	if mode != veo.ModeFramesToVideo {
		req.LastFrameReference = ""
	}
	if mode != veo.ModeVideoExtension {
		req.PreviousVideoURI = ""
	}
	req.Prompt = videoPrompt(cut, ports.VeoGenerationMode(mode))
	return req
}

// characterSeed はカットのキャラクターに紐づくシードを返します。
//
// キーフレーム画像生成と同じ値です（keyframe.Generator が常に char.Seed のみを使います）。
// シード無し（0 = Veo リクエストから省略）の独立生成はチェーン起点ごとに見た目が確率的に
// ブレるため、少なくともキャラクター単位で固定したシードを常に渡すことで、同一ジョブ内・
// ジョブ間のキャラ一貫性を高めます。キャラクターにシードが無い場合のみ 0 を返します。
func characterSeed(characters *characterkit.Characters, cut video.Cut) int64 {
	if characters != nil {
		if char := characters.GetCharacter(cut.CharacterID); char != nil && char.Seed != nil {
			return *char.Seed
		}
	}
	return 0
}
