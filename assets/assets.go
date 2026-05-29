package assets

import "embed"

const (
	PromptDir = "prompts"
)

var (
	// Templates は、すべてのHTMLテンプレートを保持します。
	//go:embed templates/*.html
	Templates embed.FS
)
