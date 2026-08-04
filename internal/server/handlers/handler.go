package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/shouni/ap-mv/internal/domain"
	"github.com/shouni/ap-mv/internal/ports"
)

// Handler は、Web UIのハンドラーが共有する依存関係とフォーム選択肢を保持します。
type Handler struct {
	Queue             ports.TaskQueue
	HistoryRepository ports.HistoryRepository
	// DraftRepository は VideoRecipe 下書きの一覧・取得・削除先です。
	// 未設定なら下書き機能は無効です（一覧は設定案内を表示します）。
	DraftRepository ports.DraftRepository
	// JobStatus はジョブ進行状況の記録・参照先です。未設定なら状態機能は無効です。
	JobStatus        ports.JobStatusStore
	Templates        map[string]*template.Template
	ModelOptions     ModelOptions
	CharacterOptions CharacterOptions
	VisualOptions    VisualModeOptions
	// MusicBucket は、Video Recipe Create フォームの Music Job ID から
	// レシピJSON（gs://<MusicBucket>/music/<jobID>/recipe.json）を解決するためのGCSバケット名です。
	MusicBucket string
	// VeoPricing は履歴画面に出す概算コストの単価表です。nil のときは
	// domain.DefaultVeoPriceUSDPerSecond へフォールバックするため、未設定でも表示は壊れません。
	VeoPricing domain.VeoPricing
}

// applyCostEstimate は履歴詳細に概算コストを埋めます。
//
// 実績記録（veo_usage.json）があればそこに残ったモデルで単価を引き、実際に投げた秒数との
// 差＝再生成ロスも埋めます。記録が無いジョブ（実績記録の導入前、またはキーフレームのみ）では
// レシピから算出した完成尺ベースの概算だけを出し、単価は現在の既定モデルで代用します。
// 実績の読み取り失敗でページ全体を落とす価値は無いので、警告を残して概算のみで続けます。
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
	DraftItems            []domain.VideoDraft
	HistoryDetail         domain.VideoHistoryDetail
	PageMeta              domain.PageMeta
	RegenerateCut         domain.VideoHistoryCut
	RegenerateSection     domain.VideoHistorySectionGroup
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
		"regenerate_section.html",
		"drafts.html",
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
	h.recordQueuedStatus(r, task)
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
