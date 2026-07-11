package handlers

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/shouni/ap-mv/internal/domain"
	"github.com/shouni/ap-mv/internal/ports"
)

// Handler は、Web UIのハンドラーが共有する依存関係とフォーム選択肢を保持します。
type Handler struct {
	Queue             ports.TaskQueue
	HistoryRepository ports.HistoryRepository
	Templates         map[string]*template.Template
	ModelOptions      ModelOptions
	CharacterOptions  CharacterOptions
	VisualOptions     VisualModeOptions
}

// PageData は、HTMLテンプレートに渡す共通の描画データです。
type PageData struct {
	Title                 string
	CSRFToken             string
	JobID                 string
	Status                string
	Message               string
	CSS                   []string
	JS                    []string
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
	RegenerateSeedDefault string
	LatestVideo           *HomeLatestVideo
}

// HomeLatestVideo は、ホームに埋め込む最新ジョブの動画再生情報です。
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

// homeRecentJobs はホームに表示する直近ジョブ件数です。
const homeRecentJobs = 10

// Home renders the home page with the most recent jobs and the latest generated video.
func (h *Handler) Home(w http.ResponseWriter, r *http.Request) {
	data := PageData{Title: "Home"}
	if h.HistoryRepository != nil {
		page, err := h.HistoryRepository.ListHistoryPage(r.Context(), 1, homeRecentJobs)
		if err != nil {
			slog.ErrorContext(r.Context(), "failed to load recent history for home", "error", err)
		} else {
			data.HistoryItems = page.Items
			data.LatestVideo = h.latestVideoForHome(r, page.Items)
		}
	}
	h.renderPage(w, data, "index.html")
}

// latestVideoForHome は直近ジョブのうち最初に動画を持つジョブから、ホーム掲載用の
// 再生情報を返します。見つからなければ nil を返します。
func (h *Handler) latestVideoForHome(r *http.Request, items []domain.VideoHistory) *HomeLatestVideo {
	for _, item := range items {
		if !item.Generated {
			continue
		}
		detail, err := h.HistoryRepository.GetHistory(r.Context(), item.JobID)
		if err != nil {
			slog.WarnContext(r.Context(), "failed to load latest video for home",
				"job_id", item.JobID,
				"error", err,
			)
			return nil
		}
		for _, cut := range detail.Cuts {
			if cut.VideoSignedURL != "" {
				return &HomeLatestVideo{
					JobID:     detail.JobID,
					Title:     detail.Title,
					VideoURL:  cut.VideoSignedURL,
					PosterURL: cut.KeyframeURL,
				}
			}
		}
		// 最新の生成済みジョブに再生可能な動画がなければ、それ以上は遡らない
		// （表示ごとに GetHistory を積み重ねない）。
		return nil
	}
	return nil
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
		writeError(w, r, http.StatusBadRequest, "invalid form")
		return
	}
	jobID, err := domain.NewJobID("video-recipe")
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, err.Error())
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

// PostRecipe handles recipe submissions.
// Web UI のフォーム画面は履歴詳細の動画生成フォームに統合されて廃止済みですが、
// このエンドポイントは ap-mcp 等の M2M 呼び出し（JSON レスポンス）で使われ続けています。
func (h *Handler) PostRecipe(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid form")
		return
	}
	var recipe *domain.MusicRecipe
	var videoRecipe *domain.VideoRecipe
	recipeJSON := strings.TrimSpace(r.FormValue("recipe_json"))
	if recipeJSON != "" {
		parsedRecipe, parsedVideoRecipe, err := domain.UnmarshalRecipeOrVideoRecipe([]byte(recipeJSON))
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "invalid recipe json: "+err.Error())
			return
		}
		recipe = parsedRecipe
		videoRecipe = parsedVideoRecipe
	}
	jobID, err := domain.NewJobID("recipe")
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	task := &domain.Task{
		JobID:       jobID,
		Command:     domain.CommandMVFromKeyframeVideoRecipe,
		AIModels:    h.aiModelsFromForm(r),
		RecipeURL:   strings.TrimSpace(r.FormValue("recipe_url")),
		CharacterID: h.characterIDFromForm(r),
		AudioURL:    strings.TrimSpace(r.FormValue("audio_url")),
		Recipe:      recipe,
		VideoRecipe: videoRecipe,
		CreatedAt:   time.Now().UTC(),
	}
	h.enqueue(w, r, task)
}

// History renders the history page, or returns JSON when Accept: application/json is set.
func (h *Handler) History(w http.ResponseWriter, r *http.Request) {
	if h.HistoryRepository == nil {
		h.renderPage(w, PageData{Title: "History", Message: "history storage adapter is not configured yet"}, "history.html")
		return
	}
	page, err := h.HistoryRepository.ListHistoryPage(r.Context(), pageFromQuery(r), 20)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, page)
		return
	}
	h.renderPage(w, PageData{
		Title:        "History",
		HistoryItems: page.Items,
		PageMeta:     page.PageMeta,
	}, "history.html")
}

// HistoryDetail renders a generated MV history detail page, or returns JSON when Accept: application/json is set.
func (h *Handler) HistoryDetail(w http.ResponseWriter, r *http.Request) {
	if h.HistoryRepository == nil {
		h.renderPage(w, PageData{Title: "History", Message: "history storage adapter is not configured yet"}, "history_detail.html")
		return
	}
	jobID := strings.TrimSpace(chi.URLParam(r, "jobID"))
	if err := domain.ValidateJobID(jobID); err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	history, err := h.HistoryRepository.GetHistory(r.Context(), jobID)
	if err != nil {
		slog.ErrorContext(r.Context(), "failed to get history detail",
			"job_id", jobID,
			"error", err,
		)
		writeError(w, r, http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
		return
	}
	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, history)
		return
	}
	h.renderPage(w, h.withModelOptions(PageData{
		Title:         "History Detail",
		CSRFToken:     csrfTokenFromContext(r.Context()),
		HistoryDetail: history,
	}), "history_detail.html")
}

// DownloadKeyframes redirects to the pre-built keyframe zip in GCS.
// Falls back to on-demand zip generation for jobs that predate the pipeline change.
func (h *Handler) DownloadKeyframes(w http.ResponseWriter, r *http.Request) {
	jobID := strings.TrimSpace(chi.URLParam(r, "jobID"))
	if err := domain.ValidateJobID(jobID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if h.HistoryRepository == nil {
		http.Error(w, "history storage adapter is not configured", http.StatusInternalServerError)
		return
	}

	// Try to redirect to the pre-built zip uploaded by the pipeline.
	// Continue to on-demand fallback even on signing error — intentional fail-soft.
	signedURL, err := h.HistoryRepository.KeyframeZipSignedURL(r.Context(), jobID)
	if err != nil {
		slog.ErrorContext(r.Context(), "keyframe zip signed URL failed, falling back to on-demand generation", "job_id", jobID, "error", err)
	}
	if signedURL != "" {
		http.Redirect(w, r, signedURL, http.StatusFound)
		return
	}

	// Fall back: build zip on-demand for jobs without a pre-built zip.
	history, err := h.HistoryRepository.GetHistory(r.Context(), jobID)
	if err != nil {
		slog.ErrorContext(r.Context(), "failed to get history for keyframe download", "job_id", jobID, "error", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	hasKeyframes := false
	for _, cut := range history.Cuts {
		if strings.TrimSpace(cut.KeyframeReference) != "" {
			hasKeyframes = true
			break
		}
	}
	if !hasKeyframes {
		http.Error(w, "no keyframes available", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="keyframes-%s.zip"`, jobID))
	zw := zip.NewWriter(w)
	if err := h.HistoryRepository.DownloadKeyframes(r.Context(), jobID, func(name string, reader io.Reader) error {
		fw, err := zw.Create(name)
		if err != nil {
			slog.ErrorContext(r.Context(), "failed to create zip entry", "job_id", jobID, "file", name, "error", err)
			return err
		}
		if _, err := io.Copy(fw, reader); err != nil {
			slog.ErrorContext(r.Context(), "failed to write zip entry", "job_id", jobID, "file", name, "error", err)
			return err
		}
		return nil
	}); err != nil {
		slog.ErrorContext(r.Context(), "failed to stream keyframe zip", "job_id", jobID, "error", err)
		return
	}
	if assContent := domain.GenerateASS(history.Cuts, domain.ASSColors{}); assContent != "" {
		fw, err := zw.Create("lyrics.ass")
		if err != nil {
			slog.ErrorContext(r.Context(), "failed to create ass zip entry", "job_id", jobID, "error", err)
		} else if _, err := io.WriteString(fw, assContent); err != nil {
			slog.ErrorContext(r.Context(), "failed to write ass zip entry", "job_id", jobID, "error", err)
		}
	}
	if err := zw.Close(); err != nil {
		slog.ErrorContext(r.Context(), "failed to finalize zip", "job_id", jobID, "error", err)
	}
}

// findHistoryCutByIndex returns the cut matching cutIndex, or false if none matches.
func findHistoryCutByIndex(cuts []domain.VideoHistoryCut, cutIndex int) (domain.VideoHistoryCut, bool) {
	for _, cut := range cuts {
		if cut.CutIndex == cutIndex {
			return cut, true
		}
	}
	return domain.VideoHistoryCut{}, false
}

// parseJobIDAndCutIndex reads and validates the jobID and cutIndex URL params shared by the
// regenerate-keyframe form page and its submit handler.
func parseJobIDAndCutIndex(r *http.Request) (jobID string, cutIndex int, err error) {
	jobID = strings.TrimSpace(chi.URLParam(r, "jobID"))
	if err = domain.ValidateJobID(jobID); err != nil {
		return "", 0, err
	}
	cutIndexStr := strings.TrimSpace(chi.URLParam(r, "cutIndex"))
	cutIndex, err = strconv.Atoi(cutIndexStr)
	if err != nil || cutIndex < 1 {
		return "", 0, errors.New("invalid cut_index")
	}
	return jobID, cutIndex, nil
}

// loadHistoryForMutation loads history for jobID and validates it has a resolvable storage URI,
// as required by every handler that enqueues a follow-up task against an existing job. On
// failure it writes the appropriate error response itself and returns ok=false.
func (h *Handler) loadHistoryForMutation(w http.ResponseWriter, r *http.Request, jobID string) (domain.VideoHistoryDetail, bool) {
	if h.HistoryRepository == nil {
		writeError(w, r, http.StatusInternalServerError, "history storage adapter is not configured")
		return domain.VideoHistoryDetail{}, false
	}
	history, err := h.HistoryRepository.GetHistory(r.Context(), jobID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
		return domain.VideoHistoryDetail{}, false
	}
	if strings.TrimSpace(history.StorageURI) == "" {
		writeError(w, r, http.StatusInternalServerError, "recipe storage URI is not available")
		return domain.VideoHistoryDetail{}, false
	}
	return history, true
}

// RegenerateCutKeyframeForm renders a dedicated page for configuring a single cut's
// keyframe regeneration (prompt override, seed override, overwrite) before submitting it.
func (h *Handler) RegenerateCutKeyframeForm(w http.ResponseWriter, r *http.Request) {
	jobID, cutIndex, err := parseJobIDAndCutIndex(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if h.HistoryRepository == nil {
		http.Error(w, "history storage adapter is not configured", http.StatusInternalServerError)
		return
	}
	history, err := h.HistoryRepository.GetHistory(r.Context(), jobID)
	if err != nil {
		slog.ErrorContext(r.Context(), "failed to get history detail for regenerate form",
			"job_id", jobID,
			"error", err,
		)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	cut, ok := findHistoryCutByIndex(history.Cuts, cutIndex)
	if !ok {
		http.Error(w, "cut not found", http.StatusNotFound)
		return
	}
	seedDefault := ""
	if seed := h.CharacterOptions.seed(cut.CharacterID); seed != nil {
		seedDefault = strconv.FormatInt(*seed, 10)
	}
	h.renderPage(w, PageData{
		Title:                 "Regenerate Cut",
		CSRFToken:             csrfTokenFromContext(r.Context()),
		HistoryDetail:         history,
		RegenerateCut:         cut,
		RegenerateSeedDefault: seedDefault,
	}, "regenerate_cut.html")
}

// PostRegenerateCutKeyframe enqueues a keyframe regeneration task for a single cut.
func (h *Handler) PostRegenerateCutKeyframe(w http.ResponseWriter, r *http.Request) {
	jobID, cutIndex, err := parseJobIDAndCutIndex(r)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	history, ok := h.loadHistoryForMutation(w, r, jobID)
	if !ok {
		return
	}
	cut, ok := findHistoryCutByIndex(history.Cuts, cutIndex)
	if !ok {
		writeError(w, r, http.StatusNotFound, "cut not found")
		return
	}
	newJobID, err := domain.NewJobID("regen-keyframe")
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	task := &domain.Task{
		JobID:                newJobID,
		Command:              domain.CommandRegenerateCutKeyframe,
		RecipeURL:            history.StorageURI,
		CutIndex:             &cutIndex,
		OverwriteKeyframe:    r.FormValue("overwrite") == "on",
		OriginalJobID:        jobID,
		VisualAnchorOverride: strings.TrimSpace(r.FormValue("visual_anchor")),
		EditPrompt:           strings.TrimSpace(r.FormValue("edit_prompt")),
		CreatedAt:            time.Now().UTC(),
	}
	if seedStr := strings.TrimSpace(r.FormValue("seed")); seedStr != "" {
		if strings.TrimSpace(cut.CharacterID) == "" {
			writeError(w, r, http.StatusBadRequest, "cut has no character to apply a seed override to")
			return
		}
		seed, err := strconv.ParseInt(seedStr, 10, 64)
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "invalid seed")
			return
		}
		if current := h.CharacterOptions.seed(cut.CharacterID); current == nil || *current != seed {
			task.SeedOverride = &seed
			task.SeedOverrideCharacterID = cut.CharacterID
		}
	}
	h.enqueue(w, r, task)
}

// PostRegenerateZip enqueues a ZIP re-creation task for an existing job.
// Optional form fields primary_color and secondary_color (CSS hex) override the default karaoke colors.
func (h *Handler) PostRegenerateZip(w http.ResponseWriter, r *http.Request) {
	jobID := strings.TrimSpace(chi.URLParam(r, "jobID"))
	if err := domain.ValidateJobID(jobID); err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	history, ok := h.loadHistoryForMutation(w, r, jobID)
	if !ok {
		return
	}
	newJobID, err := domain.NewJobID("regen-zip")
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	task := &domain.Task{
		JobID:             newJobID,
		Command:           domain.CommandRegenerateZip,
		RecipeURL:         history.StorageURI,
		ASSPrimaryColor:   strings.TrimSpace(r.FormValue("primary_color")),
		ASSSecondaryColor: strings.TrimSpace(r.FormValue("secondary_color")),
		OriginalJobID:     jobID,
		CreatedAt:         time.Now().UTC(),
	}
	h.enqueue(w, r, task)
}

// PostGenerateVideoFromHistory は、既存ジョブの保存済みレシピから動画生成タスクを投入します。
// target=full で全カットのフルMV生成、target=<セクションインデックス> でそのセクションだけの
// ショート動画生成になります。キーフレーム・歌詞・時間割はジョブの保存済みレシピから
// サーバー側で解決するため、フォーム入力は対象と Veo モデル・アスペクト比だけです。
func (h *Handler) PostGenerateVideoFromHistory(w http.ResponseWriter, r *http.Request) {
	jobID := strings.TrimSpace(chi.URLParam(r, "jobID"))
	if err := domain.ValidateJobID(jobID); err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	history, ok := h.loadHistoryForMutation(w, r, jobID)
	if !ok {
		return
	}

	command, sectionIndex, err := resolveHistoryGenerationTask(strings.TrimSpace(r.FormValue("target")), len(history.Sections))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	task := &domain.Task{
		RecipeURL:      history.StorageURI,
		VeoModel:       h.veoModelFromForm(r),
		VeoAspectRatio: strings.TrimSpace(r.FormValue("aspect_ratio")),
		Command:        command,
		SectionIndex:   sectionIndex,
		CreatedAt:      time.Now().UTC(),
	}
	jobIDPrefix := "mv"
	if command == domain.CommandShortVideoFromSection {
		jobIDPrefix = "short"
	}
	task.JobID, err = domain.NewJobID(jobIDPrefix)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	h.enqueue(w, r, task)
}

// resolveHistoryGenerationTask decides the task command and section index implied by a
// "generate video from history" request's target selector: "full" for the whole recipe's MV,
// or a section array index (as a string) for a short video from just that section.
func resolveHistoryGenerationTask(target string, sectionCount int) (domain.TaskCommand, *int, error) {
	if target == "full" {
		return domain.CommandMVFromKeyframeVideoRecipe, nil, nil
	}
	if sectionCount == 0 {
		return "", nil, errors.New("no sections available in this recipe")
	}
	sectionIndex, err := strconv.Atoi(target)
	if err != nil || sectionIndex < 0 || sectionIndex >= sectionCount {
		return "", nil, errors.New("invalid target section")
	}
	return domain.CommandShortVideoFromSection, &sectionIndex, nil
}

// DeleteHistory handles history deletion requests.
func (h *Handler) DeleteHistory(w http.ResponseWriter, r *http.Request) {
	jobID := strings.TrimSpace(chi.URLParam(r, "jobID"))
	if err := domain.ValidateJobID(jobID); err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	if h.HistoryRepository != nil {
		if err := h.HistoryRepository.DeleteHistory(r.Context(), jobID); err != nil {
			writeError(w, r, http.StatusInternalServerError, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"job_id": jobID, "status": "deleted"})
}

func pageFromQuery(r *http.Request) int {
	page, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("page")))
	if err != nil || page < 1 {
		return 1
	}
	return page
}

// enqueue validates and submits a task to the configured queue.
func (h *Handler) enqueue(w http.ResponseWriter, r *http.Request, task *domain.Task) {
	if err := task.Validate(); err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	if h.Queue != nil {
		if err := h.Queue.Enqueue(r.Context(), task); err != nil {
			writeError(w, r, http.StatusBadGateway, err.Error())
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
	return strings.Contains(strings.ToLower(r.Header.Get("Accept")), "application/json")
}

// writeError writes an error response, using JSON when the caller requested it (mirroring
// writeJSON's success-path content negotiation) so M2M/JSON callers (Accept: application/json)
// reliably get a JSON error body instead of an HTML/plain-text one.
func writeError(w http.ResponseWriter, r *http.Request, status int, message string) {
	if wantsJSON(r) {
		writeJSON(w, status, map[string]string{"error": message})
		return
	}
	http.Error(w, message, status)
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

// writeJSON writes a JSON response with the given status.
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
