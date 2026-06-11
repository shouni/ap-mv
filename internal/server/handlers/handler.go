package handlers

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"ap-mv/internal/domain"
	"ap-mv/internal/ports"
)

type Handler struct {
	Queue            ports.TaskQueue
	Templates        map[string]*template.Template
	ModelOptions     ModelOptions
	CharacterOptions CharacterOptions
}

type ModelOptions struct {
	GeminiModels       []string
	ImageModels        []string
	DefaultGeminiModel string
	DefaultImageModel  string
}

type CharacterOption struct {
	ID        string
	Name      string
	IsDefault bool
}

type CharacterOptions struct {
	Characters         []CharacterOption
	DefaultCharacterID string
}

// normalize normalizes the provided values.
func (o *ModelOptions) normalize() {
	o.GeminiModels = normalizeModelOptions(o.GeminiModels, o.DefaultGeminiModel, "gemini-3.5-flash")
	o.ImageModels = normalizeModelOptions(o.ImageModels, o.DefaultImageModel, "gemini-3-pro-image-preview")
	o.DefaultGeminiModel = normalizeSelectedModel(o.DefaultGeminiModel, o.GeminiModels)
	o.DefaultImageModel = normalizeSelectedModel(o.DefaultImageModel, o.ImageModels)
}

// normalize normalizes the provided values.
func (o *CharacterOptions) normalize() {
	seen := make(map[string]bool, len(o.Characters))
	normalized := make([]CharacterOption, 0, len(o.Characters))
	for _, char := range o.Characters {
		char.ID = strings.TrimSpace(char.ID)
		char.Name = strings.TrimSpace(char.Name)
		if char.ID == "" || seen[strings.ToLower(char.ID)] {
			continue
		}
		if char.Name == "" {
			char.Name = char.ID
		}
		if char.IsDefault && strings.TrimSpace(o.DefaultCharacterID) == "" {
			o.DefaultCharacterID = char.ID
		}
		seen[strings.ToLower(char.ID)] = true
		normalized = append(normalized, char)
	}
	o.Characters = normalized
	o.DefaultCharacterID = strings.TrimSpace(o.DefaultCharacterID)
	if o.DefaultCharacterID == "" && len(o.Characters) > 0 {
		o.DefaultCharacterID = o.Characters[0].ID
	}
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

type PageData struct {
	Title               string
	CSRFToken           string
	JobID               string
	Message             string
	CSS                 []string
	JS                  []string
	GeminiModels        []string
	ImageModels         []string
	Characters          []CharacterOption
	SelectedGeminiModel string
	SelectedImageModel  string
	SelectedCharacterID string
}

// NewHandler constructs a handler with default character options.
func NewHandler(assets fs.FS, queue ports.TaskQueue, modelOptions ...ModelOptions) (*Handler, error) {
	return NewHandlerWithOptions(assets, queue, firstModelOptions(modelOptions), CharacterOptions{})
}

// NewHandlerWithOptions constructs a handler with explicit model and character options.
func NewHandlerWithOptions(assets fs.FS, queue ports.TaskQueue, modelOptions ModelOptions, characterOptions CharacterOptions) (*Handler, error) {
	templates := make(map[string]*template.Template)
	for _, name := range []string{
		"index.html",
		"compose.html",
		"recipe.html",
		"history.html",
	} {
		tmpl, err := template.ParseFS(
			assets,
			"templates/layout.html",
			"templates/"+name,
		)
		if err != nil {
			return nil, fmt.Errorf("parse template %s: %w", name, err)
		}
		templates[name] = tmpl
	}
	options := modelOptions
	options.normalize()
	characterOptions.normalize()
	return &Handler{Queue: queue, Templates: templates, ModelOptions: options, CharacterOptions: characterOptions}, nil
}

// firstModelOptions returns the first matching model options.
func firstModelOptions(options []ModelOptions) ModelOptions {
	if len(options) == 0 {
		return ModelOptions{}
	}
	return options[0]
}

// Home renders the home page.
func (h *Handler) Home(w http.ResponseWriter, r *http.Request) {
	h.renderPage(w, PageData{Title: "Home"}, "index.html")
}

// ComposeForm renders the compose form.
func (h *Handler) ComposeForm(w http.ResponseWriter, r *http.Request) {
	h.renderPage(w, h.withModelOptions(PageData{
		Title:     "Compose",
		CSRFToken: csrfTokenFromContext(r.Context()),
	}), "compose.html")
}

// PostCompose handles compose form submissions.
func (h *Handler) PostCompose(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	if !validCSRFToken(r) {
		http.Error(w, "invalid csrf token", http.StatusForbidden)
		return
	}
	jobID, err := domain.NewJobID("compose")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	command := composeCommandFromRunMode(r.FormValue("run_mode"))
	task := &domain.Task{
		JobID:       jobID,
		Command:     command,
		AIModels:    h.aiModelsFromForm(r),
		SourceURL:   strings.TrimSpace(r.FormValue("url")),
		Text:        strings.TrimSpace(r.FormValue("text")),
		ImageURL:    strings.TrimSpace(r.FormValue("image_url")),
		CharacterID: h.characterIDFromForm(r),
		AudioURL:    strings.TrimSpace(r.FormValue("audio_url")),
		CreatedAt:   time.Now().UTC(),
	}
	h.enqueue(w, r, task)
}

// composeCommandFromRunMode returns the task command for a compose run mode.
func composeCommandFromRunMode(runMode string) domain.TaskCommand {
	switch strings.TrimSpace(runMode) {
	case "keyframe":
		return domain.CommandComposeToKeyframe
	default:
		return domain.CommandCompose
	}
}

// withModelOptions adds model and character selections to page data.
func (h *Handler) withModelOptions(data PageData) PageData {
	options := h.ModelOptions
	options.normalize()
	data.GeminiModels = options.GeminiModels
	data.ImageModels = options.ImageModels
	data.SelectedGeminiModel = options.DefaultGeminiModel
	data.SelectedImageModel = options.DefaultImageModel
	characters := h.CharacterOptions
	characters.normalize()
	data.Characters = characters.Characters
	data.SelectedCharacterID = characters.DefaultCharacterID
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

// characterIDFromForm reads the character selection from a request form.
func (h *Handler) characterIDFromForm(r *http.Request) string {
	value := strings.TrimSpace(r.FormValue("character_id"))
	options := h.CharacterOptions
	options.normalize()
	if value == "" {
		return options.DefaultCharacterID
	}
	return value
}

// RecipeForm renders the recipe form.
func (h *Handler) RecipeForm(w http.ResponseWriter, r *http.Request) {
	h.renderPage(w, h.withModelOptions(PageData{Title: "Generate From Recipe", CSRFToken: csrfTokenFromContext(r.Context())}), "recipe.html")
}

// PostRecipe handles recipe form submissions.
func (h *Handler) PostRecipe(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	if !validCSRFToken(r) {
		http.Error(w, "invalid csrf token", http.StatusForbidden)
		return
	}
	var recipe *domain.MusicRecipe
	recipeJSON := strings.TrimSpace(r.FormValue("recipe_json"))
	if recipeJSON != "" {
		var parsed domain.MusicRecipe
		if err := json.Unmarshal([]byte(recipeJSON), &parsed); err != nil {
			http.Error(w, "invalid recipe json: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := parsed.Normalize(); err != nil {
			http.Error(w, "invalid recipe: "+err.Error(), http.StatusBadRequest)
			return
		}
		recipe = &parsed
	}
	jobID, err := domain.NewJobID("recipe")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	task := &domain.Task{
		JobID:       jobID,
		Command:     domain.CommandGenerateFromRecipe,
		RecipeURL:   strings.TrimSpace(r.FormValue("recipe_url")),
		CharacterID: h.characterIDFromForm(r),
		AudioURL:    strings.TrimSpace(r.FormValue("audio_url")),
		Recipe:      recipe,
		CreatedAt:   time.Now().UTC(),
	}
	h.enqueue(w, r, task)
}

// History renders the history page.
func (h *Handler) History(w http.ResponseWriter, _ *http.Request) {
	h.renderPage(w, PageData{Title: "History", Message: "history storage adapter is not configured yet"}, "history.html")
}

// DeleteHistory handles history deletion requests.
func (h *Handler) DeleteHistory(w http.ResponseWriter, r *http.Request) {
	jobID := strings.TrimSpace(chi.URLParam(r, "jobID"))
	if err := domain.ValidateJobID(jobID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"job_id": jobID, "status": "delete adapter is not configured yet"})
}

// enqueue validates and submits a task to the configured queue.
func (h *Handler) enqueue(w http.ResponseWriter, r *http.Request, task *domain.Task) {
	if err := task.Validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if h.Queue != nil {
		if err := h.Queue.Enqueue(r.Context(), task); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"job_id": task.JobID, "status": "queued"})
}

// renderPage renders a named HTML template.
func (h *Handler) renderPage(w http.ResponseWriter, data PageData, templateName string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl, ok := h.Templates[templateName]
	if !ok {
		http.Error(w, "Template Not Found", http.StatusInternalServerError)
		return
	}
	if err := tmpl.ExecuteTemplate(w, "layout.html", data); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// validCSRFToken reports whether the request contains a valid CSRF token.
func validCSRFToken(r *http.Request) bool {
	expected := csrfTokenFromContext(r.Context())
	submitted := strings.TrimSpace(r.FormValue("csrf_token"))
	if submitted == "" {
		submitted = strings.TrimSpace(r.Header.Get("X-CSRF-Token"))
	}
	if expected == "" || submitted == "" || len(expected) != len(submitted) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(submitted)) == 1
}

// writeJSON writes a JSON response with the given status.
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
