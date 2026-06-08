package assets

import "embed"

const (
	PromptDir = "prompts"
)

var (
	// Templates は、すべてのHTMLテンプレートを保持します。
	//go:embed templates/*.html
	Templates embed.FS

	// StaticFiles は、ブラウザへ配信するCSS/JavaScriptなどの静的ファイルを保持します。
	//go:embed static
	StaticFiles embed.FS

	// Prompts は、AI生成用のプロンプトテンプレートを保持します。
	//go:embed prompts/*.md
	Prompts embed.FS
)
