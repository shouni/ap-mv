// Package assets provides the HTML templates, static files and prompt templates as embedded
// resources.
package assets

import (
	"embed"

	"github.com/shouni/go-prompt-kit/resource"
)

const (
	// VideoRecipePromptDir is the embedded path of the prompt templates for creating a VideoRecipe.
	VideoRecipePromptDir = "prompts/video_recipe"
	visualModePromptDir  = "prompts/visual_modes"
	videoGenPromptDir    = "prompts/video_gen"
)

var (
	// Templates holds every HTML template.
	//go:embed templates/*.html
	Templates embed.FS

	// StaticFiles holds the static files served to the browser, such as CSS and JavaScript.
	//go:embed static
	StaticFiles embed.FS

	// VideoRecipePrompts holds the prompt templates for creating a VideoRecipe.
	//go:embed prompts/video_recipe/*.md
	VideoRecipePrompts embed.FS

	// visualModeFiles are the prompt templates for visual styles.
	//go:embed prompts/visual_modes/*.md
	visualModeFiles embed.FS

	// videoGenPromptFiles are the prompt guidance per Veo video-generation mode.
	// The file names (without extension) correspond to Veo's generation modes (image_to_video /
	// frames_to_video / reference_to_video / video_extension).
	//go:embed prompts/video_gen/*.md
	videoGenPromptFiles embed.FS

	// DefaultVisualMode represents the default visual mode template key.
	DefaultVisualMode = "default"
)

// LoadVisualModeFiles loads the embedded visual-style prompt files.
func LoadVisualModeFiles() (map[string]string, error) {
	return resource.Load(visualModeFiles, visualModePromptDir)
}

// LoadVideoGenPrompts loads the embedded prompts per Veo video-generation mode.
// The keys are the file names (without extension): image_to_video / frames_to_video /
// reference_to_video / video_extension.
func LoadVideoGenPrompts() (map[string]string, error) {
	return resource.Load(videoGenPromptFiles, videoGenPromptDir)
}
