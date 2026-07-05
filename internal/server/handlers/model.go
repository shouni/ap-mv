package handlers

import (
	"net/http"
	"strings"

	"github.com/shouni/ap-mv/internal/domain"
)

// ModelOptions は、フォームで選択可能なGemini/画像生成モデルの一覧とデフォルト値を保持します。
type ModelOptions struct {
	GeminiModels       []string
	ImageModels        []string
	DefaultGeminiModel string
	DefaultImageModel  string
}

// normalize normalizes the provided values.
func (o *ModelOptions) normalize() {
	o.GeminiModels = normalizeModelOptions(o.GeminiModels, o.DefaultGeminiModel, "gemini-3.5-flash")
	o.ImageModels = normalizeModelOptions(o.ImageModels, o.DefaultImageModel, "gemini-3-pro-image-preview")
	o.DefaultGeminiModel = normalizeSelectedModel(o.DefaultGeminiModel, o.GeminiModels)
	o.DefaultImageModel = normalizeSelectedModel(o.DefaultImageModel, o.ImageModels)
}

// normalizeModelOptions normalizes available model options.
func normalizeModelOptions(values []string, preferred, fallback string) []string {
	seen := make(map[string]bool, len(values)+1)
	result := make([]string, 0, len(values)+1)
	if preferred = strings.TrimSpace(preferred); preferred != "" {
		result = append(result, preferred)
		seen[preferred] = true
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			result = append(result, value)
			seen[value] = true
		}
	}
	if len(result) == 0 {
		result = append(result, fallback)
	}
	return result
}

// normalizeSelectedModel normalizes the selected model value.
func normalizeSelectedModel(value string, values []string) string {
	value = strings.TrimSpace(value)
	if value != "" {
		return value
	}
	if len(values) > 0 {
		return values[0]
	}
	return ""
}

// firstModelOptions returns the first matching model options.
func firstModelOptions(options []ModelOptions) ModelOptions {
	if len(options) == 0 {
		return ModelOptions{}
	}
	return options[0]
}

// applyToPageData adds model selections to page data.
func (o ModelOptions) applyToPageData(data PageData) PageData {
	o.normalize()
	data.GeminiModels = o.GeminiModels
	data.ImageModels = o.ImageModels
	data.SelectedGeminiModel = o.DefaultGeminiModel
	data.SelectedImageModel = o.DefaultImageModel
	return data
}

// aiModelsFromForm reads AI model selections from a request form.
func (h *Handler) aiModelsFromForm(r *http.Request) domain.AIModels {
	options := h.ModelOptions
	options.normalize()
	textModel := strings.TrimSpace(r.FormValue("text_model"))
	if textModel == "" {
		textModel = options.DefaultGeminiModel
	}
	imageModel := strings.TrimSpace(r.FormValue("image_model"))
	if imageModel == "" {
		imageModel = options.DefaultImageModel
	}
	return domain.AIModels{
		TextModel:  textModel,
		ImageModel: imageModel,
	}
}
