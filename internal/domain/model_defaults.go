package domain

// フォールバック用のデフォルトモデルID。環境変数（config）にもフォーム入力（handlers）にも
// 有効なモデルが一切ない場合の最後の砦として、config.NormalizeModels と
// handlers.ModelOptions.normalize の両方がこの1組の定数を参照します。
//
// 値は internal/config/config.go の envDefault 構造体タグと**同一に保ってください**。
// 別の値にすると「リストが空のときだけ違うモデルに落ちる」という追いにくい挙動になります
// （実際に Image だけ pro/flash で食い違っていました）。この定数を参照できない
// 以下の箇所も同時に更新が必要です（文字列リテラルのため、変更漏れをコンパイラが
// 検出できません）。
//
//   - internal/config/config.go の envDefault 構造体タグ
//   - internal/config/config_test.go のデフォルト値アサーション
//   - README.md の環境変数表
const (
	// DefaultGeminiModel は台本などテキスト生成のフォールバックモデルです。
	DefaultGeminiModel = "gemini-3.6-flash"
	// DefaultImageModel はキーフレーム画像生成のフォールバックモデルです。
	DefaultImageModel = "gemini-3.1-flash-image"
	// DefaultVeoModel は Veo 動画生成のフォールバックモデルです。
	DefaultVeoModel = "veo-3.1-generate-001"
)
