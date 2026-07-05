// Package assets は、HTMLテンプレート・静的ファイル・プロンプトテンプレートを
// 埋め込みリソースとして提供します。
package assets

import (
	"embed"

	"github.com/shouni/go-prompt-kit/resource"
)

const (
	// VideoRecipePromptDir は、VideoRecipe作成用プロンプトテンプレートの埋め込みパスです。
	VideoRecipePromptDir = "prompts/video_recipe"
	visualModePromptDir  = "prompts/visual_modes"
)

var (
	// Templates は、すべてのHTMLテンプレートを保持します。
	//go:embed templates/*.html
	Templates embed.FS

	// StaticFiles は、ブラウザへ配信するCSS/JavaScriptなどの静的ファイルを保持します。
	//go:embed static
	StaticFiles embed.FS

	// VideoRecipePrompts は、VideoRecipe 作成用のプロンプトテンプレートを保持します。
	//go:embed prompts/video_recipe/*.md
	VideoRecipePrompts embed.FS

	// visualModeFiles は映像スタイル用プロンプトテンプレートです。
	//go:embed prompts/visual_modes/*.md
	visualModeFiles embed.FS

	// DefaultVisualMode represents the default visual mode template key.
	DefaultVisualMode = "default"
)

// LoadVisualModeFiles は埋め込まれた映像スタイル用プロンプトファイルを読み込みます。
func LoadVisualModeFiles() (map[string]string, error) {
	return resource.Load(visualModeFiles, visualModePromptDir, "")
}
