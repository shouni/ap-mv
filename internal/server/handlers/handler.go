package handlers

import (
	"context"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	characterkit "github.com/shouni/go-character-kit/character"

	"github.com/shouni/ap-mv/internal/domain"
	"github.com/shouni/ap-mv/internal/ports"

	"github.com/shouni/go-serve-kit/respond"
)

// Handler holds the dependencies and form options shared by the web UI handlers.
type Handler struct {
	Queue             ports.TaskQueue
	HistoryRepository ports.HistoryRepository
	// JobStatus is where job progress is recorded and read. When unset, the status features are disabled.
	JobStatus        ports.JobStatusStore
	Templates        map[string]*template.Template
	ModelOptions     ModelOptions
	CharacterOptions CharacterOptions
	VisualOptions    VisualModeOptions
	// MusicBucket is the GCS bucket name used to resolve the recipe JSON
	// (gs://<MusicBucket>/music/<jobID>/recipe.json) from the Video Recipe Create form's Music Job ID.
	MusicBucket string
	// VeoPricing is the price table for the estimated cost on the history page. When nil it falls
	// back to domain.DefaultVeoPriceUSDPerSecond, so the display does not break when unset.
	VeoPricing domain.VeoPricing
	// Characters is the catalogue used to look up the character's name and art for the Flow Music
	// paste text (JobPrompt). When nil, the name stays the character_id and no art is returned.
	Characters *characterkit.Characters
}

// applyCostEstimate fills the estimated cost into the history detail.
//
// When there is a usage record (veo_usage.json), the price is looked up by the model recorded
// there, and the regeneration loss — the difference from the seconds actually submitted — is
// filled in too. For jobs without a record (from before usage records, or keyframes only) only
// the estimate based on the finished length computed from the recipe is shown, using the current
// default model's price. Failing to read the record is not worth failing the whole page, so it
// logs a warning and continues with the estimate only.
func (h *Handler) applyCostEstimate(ctx context.Context, jobID string, detail *domain.VideoHistoryDetail) {
	var usage *domain.VeoUsage
	if h.HistoryRepository != nil {
		var err error
		if usage, err = h.HistoryRepository.GetVeoUsage(ctx, jobID); err != nil {
			slog.WarnContext(ctx, "failed to load veo usage; falling back to the recipe-derived estimate",
				"job_id", jobID,
				"error", err,
			)
			usage = nil
		}
	}
	model := h.ModelOptions.DefaultVeoModel
	if usage != nil && strings.TrimSpace(usage.Model) != "" {
		model = usage.Model
	}
	domain.ApplyVeoCostEstimate(detail, model, h.VeoPricing)
	domain.ApplyVeoUsage(detail, usage)
}

// PageData is the common rendering data passed to HTML templates.
type PageData struct {
	Title     string
	CSRFToken string
	// JS is the paths of JavaScript this page additionally loads. renderPage fills it from
	// pageScripts, so handlers do not set it.
	JS []string
	// NavPath is the key marking the current place in the nav. renderPage fills it from the request,
	// so handlers do not set it. The history listing makes Scripts current only with ?stage=script,
	// so the path alone does not decide it.
	NavPath               string
	JobID                 string
	Status                string
	Message               string
	GeminiModels          []string
	ImageModels           []string
	VeoModels             []string
	Characters            []CharacterOption
	VisualModes           []VisualModeOption
	SelectedGeminiModel   string
	SelectedImageModel    string
	SelectedVeoModel      string
	SelectedCharacterID   string
	SelectedVisualMode    string
	HistoryItems          []domain.VideoHistory
	HistoryDetail         domain.VideoHistoryDetail
	PageMeta              domain.PageMeta
	RegenerateCut         domain.VideoHistoryCut
	RegenerateSection     domain.VideoHistorySectionGroup
	RegenerateSeedDefault string
	LatestVideo           *HomeLatestVideo
}

// HomeLatestVideo is the playback information of the latest job's video embedded on the home page.
type HomeLatestVideo struct {
	JobID     string
	Title     string
	VideoURL  string
	PosterURL string
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
		"history.html",
		"history_detail.html",
		"regenerate_cut.html",
		"regenerate_section.html",
		"queued.html",
	} {
		tmpl, err := template.New(name).Funcs(template.FuncMap{
			"dict": templateDict,
		}).ParseFS(
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

// templateDict builds a map from alternating string-key/value pairs, letting templates pass
// multiple named values into a {{template}} invocation (which otherwise accepts only one "."
// pipeline value).
func templateDict(pairs ...any) (map[string]any, error) {
	if len(pairs)%2 != 0 {
		return nil, fmt.Errorf("dict: odd number of arguments")
	}
	m := make(map[string]any, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		key, ok := pairs[i].(string)
		if !ok {
			return nil, fmt.Errorf("dict: key %v is not a string", pairs[i])
		}
		m[key] = pairs[i+1]
	}
	return m, nil
}

// withModelOptions adds model and character selections to page data.
func (h *Handler) withModelOptions(data PageData) PageData {
	data = h.ModelOptions.applyToPageData(data)
	data = h.CharacterOptions.applyToPageData(data)
	return h.VisualOptions.applyToPageData(data)
}

func pageFromQuery(r *http.Request) int {
	page, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("page")))
	if err != nil || page < 1 {
		return 1
	}
	return page
}

// enqueue validates and submits a task to the configured queue.
//
// The order is validate → record queued → enqueue. Cloud Tasks delivers within tens of
// milliseconds, so recording after enqueueing lets a late queued overwrite the running the worker
// wrote, and a running job looks stuck at accepted in history (the order a sibling app hit in
// production, pinned by section 2.1 of the worker conventions in public-docs). When enqueueing
// fails the record is withdrawn, so a job that could not be queued does not stay in history.
func (h *Handler) enqueue(w http.ResponseWriter, r *http.Request, task *domain.Task) {
	if err := task.Validate(); err != nil {
		respond.Error(w, r, http.StatusBadRequest, err.Error())
		return
	}
	h.recordQueuedStatus(r, task)
	if h.Queue != nil {
		if err := h.Queue.Enqueue(r.Context(), task); err != nil {
			h.deleteJobStatus(r, task.JobID)
			respond.ServerError(w, r, http.StatusBadGateway, err, "failed to enqueue task", "job_id", task.JobID)
			return
		}
	}
	// Location is where to poll for progress. The page-facing response carries the same header too,
	// so the next URL to call is known without reading the body.
	w.Header().Set("Location", "/jobs/"+url.PathEscape(task.JobID))
	if !respond.WantsJSON(w, r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusAccepted)
		h.renderPage(w, r, PageData{
			Title:  "Queued",
			JobID:  task.JobID,
			Status: "queued",
		}, "queued.html")
		return
	}
	respond.JSON(w, r, http.StatusAccepted, map[string]string{"job_id": task.JobID, "status": "queued"})
}

// pageScripts is the JavaScript each page additionally loads.
//
// Scripts used to be written directly in the templates, but that meant embedding template values
// such as {{.JobID}} inside JS, mixing the page's structure and behaviour in one file. With
// external files, values are handed over through data attributes. Which page uses which script
// is fixed, so it is decided centrally here rather than in each handler.
var pageScripts = map[string][]string{
	"queued.html":             {"/static/js/job_status.js"},
	"history_detail.html":     {"/static/js/history_detail.js", "/static/js/flow_prompt.js"},
	"regenerate_cut.html":     {"/static/js/regenerate_mode.js"},
	"regenerate_section.html": {"/static/js/regenerate_mode.js"},
}

// renderPage renders a named HTML template.
func (h *Handler) renderPage(w http.ResponseWriter, r *http.Request, data PageData, templateName string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl, ok := h.Templates[templateName]
	if !ok {
		http.Error(w, "Template Not Found", http.StatusInternalServerError)
		return
	}

	data.JS = pageScripts[templateName]
	data.NavPath = navPathFor(r)
	// The model names shown in the nav are needed even on pages without a form (history, accepted, etc.).
	// Only handlers that build the options fill them, so when unset fall back to the default here.
	if data.SelectedGeminiModel == "" {
		data.SelectedGeminiModel = h.ModelOptions.DefaultGeminiModel
	}
	if err := tmpl.ExecuteTemplate(w, "layout.html", data); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// navPathFor returns the nav's current-place key.
//
// The job listing makes Scripts current with ?stage=script and History otherwise.
// Two nav items point at the same template, so the path alone does not decide it.
func navPathFor(r *http.Request) string {
	if r == nil {
		return ""
	}
	path := r.URL.Path
	if path == "/jobs" && r.URL.Query().Get("stage") == "script" {
		return "/jobs?stage=script"
	}
	return path
}
