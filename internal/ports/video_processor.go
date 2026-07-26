package ports

import "context"

// VideoProcessor は、生成済み動画に対するローカル動画処理（フレーム抽出・結合）の境界です。
// ap-mv は本来「Veo APIを叩いて動画を生成するサービス」であり、この境界だけが例外的に
// 動画バイナリ処理を持ちます（継続チェーンのリセット時の視覚的連続性確保と、
// 複数チェーンの最終的な1本化のため）。
type VideoProcessor interface {
	// ExtractLastFrame は videoURI の最終フレームを画像として抽出し destURI へアップロードします。
	// 成功時、実際にアップロードされた URI（通常は destURI と同じ）を返します。
	ExtractLastFrame(ctx context.Context, videoURI, destURI string) (string, error)
	// ConcatHardCut は videoURIs を渡された順にトランジション無し（ハードカット）で結合し、
	// destURI へアップロードします。videoURIs が1件の場合は結合処理を行わずそのまま
	// destURI へコピーします。成功時、実際にアップロードされた URI を返します。
	ConcatHardCut(ctx context.Context, videoURIs []string, destURI string) (string, error)
	// ColorMatchSaturation は videoURI 全体の彩度を referenceImageURI（キャラクター参照アート）の
	// 平均彩度に合わせて補正し、destURI へアップロードします。Veo の video_extension は
	// 直前の生成結果を条件入力として再利用するため、継続を重ねるたびに彩度がドリフトして
	// 蓄積します（実運用で確認済み）。各世代の出力を都度キャラクター基準へ引き戻すことで、
	// ドリフトが次世代へ複利的に蓄積するのを防ぎます。成功時、実際にアップロードされた
	// URI を返します。
	ColorMatchSaturation(ctx context.Context, videoURI, referenceImageURI, destURI string) (string, error)
	// Probe は videoURI を解析し、実尺と音声トラックの有無を返します。
	// 結合結果が台本どおりに仕上がったかを、生成物を再生せずに確かめるために使います。
	Probe(ctx context.Context, videoURI string) (VideoStats, error)
}

// VideoStats は、生成された動画から実測した値です。
//
// 台本には各カットの尺が入っていますが、Veo が返すクリップの実尺がそれと一致する保証は
// なく、結合結果を測るまで誰も確かめていませんでした。
type VideoStats struct {
	// DurationSeconds は動画全体の実尺です。
	DurationSeconds float64
	// HasAudio は音声トラックを持つかどうかです。ffmpeg に -c:a を指定しても、
	// 入力に音声が無ければ出力は無音のまま成功します。
	HasAudio bool
}
