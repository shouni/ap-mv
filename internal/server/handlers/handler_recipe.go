package handlers

import (
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/shouni/go-utils/jobid"

	"github.com/shouni/ap-mv/internal/domain"

	"github.com/shouni/go-serve-kit/respond"
)

// homeRecentJobs はホームに表示する直近ジョブ件数です。
const homeRecentJobs = 10

// Home renders the home page with the most recent jobs and the latest generated video.
func (h *Handler) Home(w http.ResponseWriter, r *http.Request) {
	data := PageData{Title: "Home"}
	if h.HistoryRepository != nil {
		page, err := h.HistoryRepository.ListHistoryPage(r.Context(), 1, homeRecentJobs, "")
		if err != nil {
			slog.ErrorContext(r.Context(), "failed to load recent history for home", "error", err)
		} else {
			data.HistoryItems = page.Items
			data.LatestVideo = h.latestVideoForHome(r, page.Items)
		}
	}
	h.renderPage(w, r, data, "index.html")
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
		// FinalVideoURL（継続チェーンをハードカットで結合した完成動画、chain_finalize.go）が
		// あればそれを最優先で使う。無い場合（final_video_url追加より前に生成された旧ジョブ）
		// は、末尾カットから探して最初に見つかった完成版を使う旧来のフォールバックに戻る。
		// 注意: 旧フォールバックは「チェーンが1本だけ」という前提に基づいており、チェーンが
		// リセットされたジョブでは末尾チェーンの断片だけが表示されてしまう
		// （video_music_meta.jsonがfinal_video_urlを持つ新しいジョブでは発生しない）。
		// 指すのは同一オリジンのパスです（署名は 302 の時点で 1 本だけ）。
		applyWebMediaURLs(&detail)
		if detail.FinalVideoWebURL != "" {
			poster := ""
			if len(detail.Cuts) > 0 {
				poster = detail.Cuts[0].KeyframeWebURL
			}
			return &HomeLatestVideo{
				JobID:     detail.JobID,
				Title:     detail.Title,
				VideoURL:  detail.FinalVideoWebURL,
				PosterURL: poster,
			}
		}
		for _, cut := range slices.Backward(detail.Cuts) {

			if cut.VideoWebURL != "" {
				return &HomeLatestVideo{
					JobID:     detail.JobID,
					Title:     detail.Title,
					VideoURL:  cut.VideoWebURL,
					PosterURL: cut.KeyframeWebURL,
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
	h.renderPage(w, r, h.withModelOptions(PageData{
		Title:     "Video Recipe Create",
		CSRFToken: csrfTokenFromContext(r.Context()),
	}), "compose.html")
}

// PostVideoRecipeCreate handles video recipe creation form submissions.
func (h *Handler) PostVideoRecipeCreate(w http.ResponseWriter, r *http.Request) {
	h.postMusicRecipeTask(w, r, "video-recipe", domain.CommandVideoRecipeCreate)
}

// PostVideoRecipeDraft handles draft creation form submissions.
//
// 入力は PostVideoRecipeCreate と同じで、違いはコマンドとジョブ ID プレフィックスだけです。
// キーフレームを 1 枚も焼かずにカット割りまでで止めます。保存先は完成ジョブと同じ
// video_music_meta.json で、履歴には script 段階として並びます（/history?stage=script）。
// ジョブ ID の用途プレフィックスを分けているのは、どちらのものか ID だけで分かるようにするためです。
func (h *Handler) PostVideoRecipeDraft(w http.ResponseWriter, r *http.Request) {
	h.postMusicRecipeTask(w, r, "recipe", domain.CommandVideoRecipeDraft)
}

// postMusicRecipeTask は、Music Recipe を入力とする作成系フォーム（本生成と下書き）の
// 共通実装です。2 つのハンドラは定数 2 つを除いて byte 単位で同一でした。
func (h *Handler) postMusicRecipeTask(w http.ResponseWriter, r *http.Request, jobPrefix string, command domain.TaskCommand) {
	if err := r.ParseForm(); err != nil {
		respond.Error(w, r, http.StatusBadRequest, "invalid form")
		return
	}
	jobID, ok := mintJobID(w, r, jobPrefix)
	if !ok {
		return
	}
	sourceURL, err := h.musicRecipeSourceURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, err.Error())
		return
	}
	task := &domain.Task{
		JobID:          jobID,
		Command:        command,
		AIModels:       h.aiModelsFromForm(r),
		SourceURL:      sourceURL,
		Text:           strings.TrimSpace(r.FormValue("text")),
		ImageURL:       strings.TrimSpace(r.FormValue("image_url")),
		CharacterID:    h.characterIDFromForm(r),
		VisualMode:     h.visualModeFromForm(r),
		VeoAspectRatio: strings.TrimSpace(r.FormValue("aspect_ratio")),
		CreatedAt:      time.Now().UTC(),
	}
	h.enqueue(w, r, task)
}

// musicRecipeSourceURL resolves the MusicRecipe source for video recipe creation. The Web UI
// submits music_job_id (ap-comp と同じ規則で gs://<MusicBucket>/music/<jobID>/recipe.json を組み立てる)。
// M2M callers (ap-mcp's compose_video) keep sending a raw url, since that field also accepts
// plain text/image sources unrelated to a music job.
//
// 両者は同じ Task.SourceURL に畳まれ、指す対象も同じです（url は music_job_id が組み立てる
// URI を手で書いたもの）。両方来た場合は music_job_id を優先します。そのため Web UI の
// 入力欄は music_job_id だけで、生の url は M2M 専用です。
func (h *Handler) musicRecipeSourceURL(r *http.Request) (string, error) {
	musicJobID := strings.TrimSpace(r.FormValue("music_job_id"))
	if musicJobID == "" {
		return strings.TrimSpace(r.FormValue("url")), nil
	}
	if err := jobid.Validate(musicJobID); err != nil {
		return "", fmt.Errorf("invalid music_job_id: %w", err)
	}
	if h.MusicBucket == "" {
		return "", fmt.Errorf("AP_MUSIC_BUCKET is not configured")
	}
	return fmt.Sprintf("gs://%s/music/%s/recipe.json", h.MusicBucket, musicJobID), nil
}

// PostRecipe handles recipe submissions.
// Web UI のフォーム画面は履歴詳細の動画生成フォームに統合されて廃止済みですが、
// このエンドポイントは ap-mcp 等の M2M 呼び出し（JSON レスポンス）で使われ続けています。
func (h *Handler) PostRecipe(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		respond.Error(w, r, http.StatusBadRequest, "invalid form")
		return
	}
	var recipe *domain.MusicRecipe
	var videoRecipe *domain.VideoRecipe
	recipeJSON := strings.TrimSpace(r.FormValue("recipe_json"))
	if recipeJSON != "" {
		parsedRecipe, parsedVideoRecipe, err := domain.UnmarshalRecipeOrVideoRecipe([]byte(recipeJSON))
		if err != nil {
			respond.Error(w, r, http.StatusBadRequest, "invalid recipe json: "+err.Error())
			return
		}
		recipe = parsedRecipe
		videoRecipe = parsedVideoRecipe
	}
	jobID, ok := mintJobID(w, r, "recipe")
	if !ok {
		return
	}
	task := &domain.Task{
		JobID:          jobID,
		Command:        domain.CommandMVFromKeyframeVideoRecipe,
		AIModels:       h.aiModelsFromForm(r),
		RecipeURL:      strings.TrimSpace(r.FormValue("recipe_url")),
		CharacterID:    h.characterIDFromForm(r),
		AudioURL:       strings.TrimSpace(r.FormValue("audio_url")),
		Recipe:         recipe,
		VideoRecipe:    videoRecipe,
		VeoAspectRatio: strings.TrimSpace(r.FormValue("aspect_ratio")),
		CreatedAt:      time.Now().UTC(),
	}
	h.enqueue(w, r, task)
}
