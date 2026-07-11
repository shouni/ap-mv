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
}
