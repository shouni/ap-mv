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
	VisualOptions    VisualModeOptions
}

type PageData struct {
	Title               string
	CSRFToken           string
	JobID               string
	Status              string
	Message             string
	CSS                 []string
	JS                  []string
	GeminiModels        []string
	ImageModels         []string
	Characters          []CharacterOption
	VisualModes         []VisualModeOption
	SelectedGeminiModel string
	SelectedImageModel  string
	SelectedCharacterID string
	SelectedVisualMode  string
}

// NewHandler constructs a handler with default character options.
func NewHandler(assets fs.FS, queue ports.TaskQueue, modelOptions ...ModelOptions) (*Handler, error) {
	return NewHandlerWithOptions(assets, queue, firstModelOptions(modelOptions), CharacterOptions{})
}

// NewHandlerWithOptions constructs a handler with explicit model and character options.
func NewHandlerWithOptions(assets fs.FS, queue ports.TaskQueue, modelOptions ModelOptions, characterOptions CharacterOptions, visualOptions ...VisualModeOptions) (*Handler, error) {
	templates := make(map[string]*template.Template)
	for _, name := range []string{
		"index.html",
		"compose.html",
		"recipe.html",
		"history.html",
		"queued.html",
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
	selectedVisualOptions := firstVisualModeOptions(visualOptions)
	selectedVisualOptions.normalize()
	return &Handler{
		Queue:            queue,
		Templates:        templates,
		ModelOptions:     options,
		CharacterOptions: characterOptions,
		VisualOptions:    selectedVisualOptions,
	}, nil
}

// Home renders the home page.
func (h *Handler) Home(w http.ResponseWriter, r *http.Request) {
	h.renderPage(w, PageData{Title: "Home"}, "index.html")
}

// VideoRecipeCreateForm renders the video recipe creation form.
func (h *Handler) VideoRecipeCreateForm(w http.ResponseWriter, r *http.Request) {
	h.renderPage(w, h.withModelOptions(PageData{
		Title:     "Video Recipe Create",
		CSRFToken: csrfTokenFromContext(r.Context()),
	}), "compose.html")
}

// PostVideoRecipeCreate handles video recipe creation form submissions.
func (h *Handler) PostVideoRecipeCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	if !validCSRFToken(r) {
		http.Error(w, "invalid csrf token", http.StatusForbidden)
		return
	}
	jobID, err := domain.NewJobID("video-recipe")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	task := &domain.Task{
		JobID:       jobID,
		Command:     domain.CommandVideoRecipeCreate,
		AIModels:    h.aiModelsFromForm(r),
		SourceURL:   firstNonEmptyFormValue(r, "music_recipe_url", "url"),
		Text:        strings.TrimSpace(r.FormValue("text")),
		ImageURL:    strings.TrimSpace(r.FormValue("image_url")),
		CharacterID: h.characterIDFromForm(r),
		VisualMode:  h.visualModeFromForm(r),
		CreatedAt:   time.Now().UTC(),
	}
	h.enqueue(w, r, task)
}

func firstNonEmptyFormValue(r *http.Request, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(r.FormValue(name)); value != "" {
			return value
		}
	}
	return ""
}

// withModelOptions adds model and character selections to page data.
func (h *Handler) withModelOptions(data PageData) PageData {
	data = h.ModelOptions.applyToPageData(data)
	data = h.CharacterOptions.applyToPageData(data)
	return h.VisualOptions.applyToPageData(data)
}

// RecipeForm renders the recipe form.
func (h *Handler) RecipeForm(w http.ResponseWriter, r *http.Request) {
	h.renderPage(w, h.withModelOptions(PageData{Title: "MV From Keyframe Video Recipe", CSRFToken: csrfTokenFromContext(r.Context())}), "recipe.html")
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
	var videoRecipe *domain.VideoRecipe
	recipeJSON := strings.TrimSpace(r.FormValue("recipe_json"))
	if recipeJSON != "" {
		parsedRecipe, parsedVideoRecipe, err := domain.UnmarshalRecipeOrVideoRecipe([]byte(recipeJSON))
		if err != nil {
			http.Error(w, "invalid recipe json: "+err.Error(), http.StatusBadRequest)
			return
		}
		recipe = parsedRecipe
		videoRecipe = parsedVideoRecipe
	}
	jobID, err := domain.NewJobID("recipe")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	task := &domain.Task{
		JobID:       jobID,
		Command:     domain.CommandMVFromKeyframeVideoRecipe,
		RecipeURL:   strings.TrimSpace(r.FormValue("recipe_url")),
		CharacterID: h.characterIDFromForm(r),
		AudioURL:    strings.TrimSpace(r.FormValue("audio_url")),
		Recipe:      recipe,
		VideoRecipe: videoRecipe,
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
	if !wantsJSON(r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusAccepted)
		h.renderPage(w, PageData{
			Title:  "Queued",
			JobID:  task.JobID,
			Status: "queued",
		}, "queued.html")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"job_id": task.JobID, "status": "queued"})
}

func wantsJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "application/json")
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
