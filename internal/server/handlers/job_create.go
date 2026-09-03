package handlers

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/shouni/ap-mv/internal/domain"
	"github.com/shouni/go-serve-kit/respond"
	"github.com/shouni/go-utils/jobid"
)

// JobCreate は、新しいジョブを投入します（POST /jobs）。
//
// 入口は 1 本で、本文の command で分かれます。video_recipe_create（既定）と
// video_recipe_draft は Music Recipe を入力にする作成系で、違いはカット割りの先へ進むか
// どうかだけです。mv_from_keyframe_video_recipe は VideoRecipe JSON からの動画生成で、
// ap-mcp 等の M2M 呼び出しが使います（フォーム画面は履歴詳細の動画生成フォームへ統合済み）。
func (h *Handler) JobCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		respond.Error(w, r, http.StatusBadRequest, "invalid form")
		return
	}
	switch command := domain.TaskCommand(strings.TrimSpace(r.FormValue("command"))); command {
	case "", domain.CommandVideoRecipeCreate:
		h.postMusicRecipeTask(w, r, "video-recipe", domain.CommandVideoRecipeCreate)
	case domain.CommandVideoRecipeDraft:
		h.postMusicRecipeTask(w, r, "recipe", domain.CommandVideoRecipeDraft)
	case domain.CommandMVFromKeyframeVideoRecipe:
		h.postVideoRecipeTask(w, r)
	default:
		respond.Error(w, r, http.StatusBadRequest, fmt.Sprintf("command は %s / %s / %s のいずれかです",
			domain.CommandVideoRecipeCreate, domain.CommandVideoRecipeDraft, domain.CommandMVFromKeyframeVideoRecipe))
	}
}

// postMusicRecipeTask は、Music Recipe を入力とする作成系（本生成と台本のみ）の共通実装です。
// 違いはコマンドとジョブ ID プレフィックスだけで、台本のみはキーフレームを 1 枚も焼かずに
// カット割りまでで止まります（履歴には script 段階として並び、?stage=script で絞れます）。
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
// submits music_job_id (ap-music と同じ規則で gs://<MusicBucket>/music/<jobID>/recipe.json を組み立てる)。
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

// postVideoRecipeTask は VideoRecipe JSON からの動画生成を投入します（command=mv_from_keyframe_video_recipe）。
func (h *Handler) postVideoRecipeTask(w http.ResponseWriter, r *http.Request) {
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
