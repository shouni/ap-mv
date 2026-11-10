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

// JobCreate submits a new job (POST /jobs).
//
// There is one entrance, split by the body's command. video_recipe_create (the default) and
// video_recipe_draft are the creating commands that take a Music Recipe as input; they differ only
// in whether they go past the cut list. mv_from_keyframe_video_recipe generates video from
// VideoRecipe JSON and is used by M2M callers such as the MCP gateway (its form page has been
// merged into the history detail's video-generation form).
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
		respond.Error(w, r, http.StatusBadRequest, fmt.Sprintf("command must be one of %s / %s / %s",
			domain.CommandVideoRecipeCreate, domain.CommandVideoRecipeDraft, domain.CommandMVFromKeyframeVideoRecipe))
	}
}

// postMusicRecipeTask is the shared implementation of the creating commands that take a Music
// Recipe (full generation and script only). They differ only in command and job ID prefix; script
// only stops at the cut list without rendering a single keyframe (it is listed at the script stage
// and can be filtered with ?stage=script).
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
// submits music_job_id (building gs://<MusicBucket>/music/<jobID>/recipe.json by the same rule as the music-generation service).
// M2M callers (the MCP gateway's compose_video) keep sending a raw url, since that field also accepts
// plain text/image sources unrelated to a music job.
//
// Both fold into the same Task.SourceURL and point at the same thing (url is the URI music_job_id
// builds, written by hand). When both come, music_job_id wins. So the web UI's input field is
// music_job_id only, and the raw url is for M2M only.
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

// postVideoRecipeTask enqueues video generation from VideoRecipe JSON (command=mv_from_keyframe_video_recipe).
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
