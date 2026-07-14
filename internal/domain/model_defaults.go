package domain

// フォールバック用のデフォルトモデルID。環境変数（config）にもフォーム入力（handlers）にも
// 有効なモデルが一切ない場合の最後の砦として、config.NormalizeModels と
// handlers.ModelOptions.normalize の両方がこの1組の定数を参照します。
// モデルを更新するときはここだけを変更してください（internal/config/config.go の
// envDefault 構造体タグは文字列リテラルのためこの定数を参照できず、別途更新が必要です —
// README.md の環境変数表も同様）。
const (
	// DefaultGeminiModel は台本などテキスト生成のフォールバックモデルです。
	DefaultGeminiModel = "gemini-3.5-flash"
	// DefaultImageModel はキーフレーム画像生成のフォールバックモデルです。
	DefaultImageModel = "gemini-3-pro-image-preview"
	// DefaultVeoModel は Veo 動画生成のフォールバックモデルです。
	DefaultVeoModel = "veo-3.1-generate-001"
)
