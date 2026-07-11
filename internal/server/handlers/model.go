package handlers

import (
	"net/http"
	"strings"

	"github.com/shouni/ap-mv/internal/domain"
)

// ModelOptions は、フォームで選択可能なGemini/画像/Veo動画生成モデルの一覧とデフォルト値を保持します。
type ModelOptions struct {
	GeminiModels       []string
	ImageModels        []string
	VeoModels          []string
	DefaultGeminiModel string
	DefaultImageModel  string
	DefaultVeoModel    string
}

// normalize normalizes the provided values.
func (o *ModelOptions) normalize() {
	o.GeminiModels = domain.NormalizeModelList(o.GeminiModels, o.DefaultGeminiModel, "gemini-3.5-flash")
	o.ImageModels = domain.NormalizeModelList(o.ImageModels, o.DefaultImageModel, "gemini-3-pro-image-preview")
	o.VeoModels = domain.NormalizeModelList(o.VeoModels, o.DefaultVeoModel, "veo-3.1-generate-001")
	o.DefaultGeminiModel = domain.NormalizeDefaultModel(o.DefaultGeminiModel, o.GeminiModels, "")
	o.DefaultImageModel = domain.NormalizeDefaultModel(o.DefaultImageModel, o.ImageModels, "")
	o.DefaultVeoModel = domain.NormalizeDefaultModel(o.DefaultVeoModel, o.VeoModels, "")
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
	data.VeoModels = o.VeoModels
	data.SelectedGeminiModel = o.DefaultGeminiModel
	data.SelectedImageModel = o.DefaultImageModel
	data.SelectedVeoModel = o.DefaultVeoModel
	return data
}

// veoModelFromForm reads the Veo model selection from a request form.
func (h *Handler) veoModelFromForm(r *http.Request) string {
	options := h.ModelOptions
	options.normalize()
	model := strings.TrimSpace(r.FormValue("veo_model"))
	if model == "" {
		model = options.DefaultVeoModel
	}
	return model
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
