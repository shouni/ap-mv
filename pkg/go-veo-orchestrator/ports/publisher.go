package ports

// PublishOptions はパブリッシュ動作およびメタデータ構築を制御する設定項目です。
type PublishOptions struct {
	OutputDir  string
	ImagePaths []string // 明示的にキーフレーム画像パスを指定する場合に使用。空なら KeyframeReference を使用します。
}

// PublishResult はパブリッシュ処理の結果として生成されたファイルの情報を保持します。
type PublishResult struct {
	MetadataPath string   // 生成された video_music_meta.json のパス
	VideoPath    string   // 最終結合動画のパス（未生成の場合は空）
	ImagePaths   []string // 保存された全キーフレーム画像のパスリスト
}
