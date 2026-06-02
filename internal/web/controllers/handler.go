package controllers

import (
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
	"ap-mv/internal/worker/event"
)

type Handler struct {
	Queue      ports.TaskQueue
	Dispatcher event.Dispatcher
	Index      *template.Template
}

type PageData struct {
	Title     string
	CSRFToken string
	JobID     string
	Message   string
}

func NewHandler(assets fs.FS, queue ports.TaskQueue, dispatcher event.Dispatcher) (*Handler, error) {
	index, err := template.ParseFS(assets, "assets/templates/index.html")
	if err != nil {
		return nil, fmt.Errorf("parse index template: %w", err)
	}
	return &Handler{Queue: queue, Dispatcher: dispatcher, Index: index}, nil
}

func (h *Handler) Home(w http.ResponseWriter, r *http.Request) {
	h.renderIndex(w, r, PageData{Title: "Home"})
}

func (h *Handler) ComposeForm(w http.ResponseWriter, r *http.Request) {
	h.renderSimplePage(w, PageData{Title: "Compose", CSRFToken: csrfTokenFromContext(r.Context())}, composeFormHTML)
}

func (h *Handler) PostCompose(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	jobID, err := domain.NewJobID("compose")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	task := &domain.Task{
		JobID:     jobID,
		Command:   domain.CommandCompose,
		SourceURL: strings.TrimSpace(r.FormValue("url")),
		Text:      strings.TrimSpace(r.FormValue("text")),
		ImageURL:  strings.TrimSpace(r.FormValue("image_url")),
		CreatedAt: time.Now().UTC(),
	}
	h.enqueue(w, r, task)
}

func (h *Handler) RecipeForm(w http.ResponseWriter, r *http.Request) {
	h.renderSimplePage(w, PageData{Title: "Generate From Recipe", CSRFToken: csrfTokenFromContext(r.Context())}, recipeFormHTML)
}

func (h *Handler) PostRecipe(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	var recipe domain.MusicRecipe
	if err := json.Unmarshal([]byte(r.FormValue("recipe_json")), &recipe); err != nil {
		http.Error(w, "invalid recipe json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := recipe.Normalize(); err != nil {
		http.Error(w, "invalid recipe: "+err.Error(), http.StatusBadRequest)
		return
	}
	jobID, err := domain.NewJobID("recipe")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	task := &domain.Task{JobID: jobID, Command: domain.CommandGenerateFromRecipe, Recipe: &recipe, CreatedAt: time.Now().UTC()}
	h.enqueue(w, r, task)
}

func (h *Handler) TaskGenerate(w http.ResponseWriter, r *http.Request) {
	recipe, err := h.Dispatcher.Dispatch(r.Context(), r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, recipe)
}

func (h *Handler) History(w http.ResponseWriter, _ *http.Request) {
	h.renderSimplePage(w, PageData{Title: "History", Message: "history storage adapter is not configured yet"}, historyHTML)
}

func (h *Handler) DeleteHistory(w http.ResponseWriter, r *http.Request) {
	jobID := strings.TrimSpace(chi.URLParam(r, "jobID"))
	if err := domain.ValidateJobID(jobID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"job_id": jobID, "status": "delete adapter is not configured yet"})
}

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

func (h *Handler) renderIndex(w http.ResponseWriter, _ *http.Request, data PageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.Index.Execute(w, data); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

func (h *Handler) renderSimplePage(w http.ResponseWriter, data PageData, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	page := strings.ReplaceAll(simpleLayoutHTML, "{{TITLE}}", template.HTMLEscapeString(data.Title))
	page = strings.ReplaceAll(page, "{{BODY}}", body)
	page = strings.ReplaceAll(page, "{{CSRF}}", template.HTMLEscapeString(data.CSRFToken))
	page = strings.ReplaceAll(page, "{{MESSAGE}}", template.HTMLEscapeString(data.Message))
	_, _ = w.Write([]byte(page))
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

const simpleLayoutHTML = `<!doctype html><html lang="ja"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>{{TITLE}} - AP MV</title><style>body{font-family:ui-sans-serif,system-ui;margin:0;background:#f4f9f1;color:#172033}.wrap{max-width:880px;margin:48px auto;padding:0 20px}.card{background:#fff;border:1px solid #cfe4be;border-radius:20px;padding:28px;box-shadow:0 8px 30px #263b1a12}label{display:block;font-weight:700;margin:16px 0 6px}input,textarea{width:100%;box-sizing:border-box;border:1px solid #bdd7aa;border-radius:12px;padding:12px;font:inherit}textarea{min-height:220px}button,a.btn{display:inline-block;margin-top:18px;background:#689f38;color:#fff;border:0;border-radius:12px;padding:12px 18px;text-decoration:none;font-weight:700}.nav{color:#558b2f;text-decoration:none}</style></head><body><main class="wrap"><a class="nav" href="/">AP MV</a><section class="card"><h1>{{TITLE}}</h1>{{BODY}}</section></main></body></html>`

const composeFormHTML = `<form method="post" action="/web/compose"><input type="hidden" name="csrf_token" value="{{CSRF}}"><label>URL</label><input name="url" placeholder="https://example.com/source"><label>Text</label><textarea name="text" placeholder="原稿、コンセプト、歌詞の方向性など"></textarea><label>Image URL</label><input name="image_url" placeholder="https://example.com/reference.png"><button type="submit">Queue Compose</button></form>`

const recipeFormHTML = `<form method="post" action="/web/generate-from-recipe"><input type="hidden" name="csrf_token" value="{{CSRF}}"><label>MusicRecipe JSON</label><textarea name="recipe_json" placeholder='{"title":"...","sections":[{"name":"intro","duration_seconds":8,"prompt":"..."}]}'></textarea><button type="submit">Queue Recipe Generation</button></form>`

const historyHTML = `<p>{{MESSAGE}}</p><a class="btn" href="/">Back Home</a>`
