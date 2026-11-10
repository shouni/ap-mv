package handlers

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/shouni/go-utils/jobid"

	"github.com/shouni/ap-mv/internal/adapters/prompt"
	"github.com/shouni/ap-mv/internal/domain"

	"github.com/shouni/go-serve-kit/respond"
)

// jobPromptResponse is the JSON representation of GET /jobs/{jobID}/prompt.
type jobPromptResponse struct {
	JobID string `json:"job_id"`
	// Prompt is the text to paste into Google Flow Music's MV creation (the same as the text/plain response).
	// It is one direction note for the whole song, passed together with the song's audio.
	Prompt string `json:"prompt"`
	// ReferenceImages is the character art that keeps the characters' look consistent when attached with the text.
	ReferenceImages []promptReferenceImage `json:"reference_images,omitempty"`
}

// promptReferenceImage is where one character's art lives.
type promptReferenceImage struct {
	CharacterID string `json:"character_id"`
	Name        string `json:"name"`
	URI         string `json:"uri"`
}

// JobPrompt returns the job's recipe as text to paste into Google Flow Music's MV creation.
//
// It is the path for handing over pasteable text rather than recipe JSON when the same MV is to be
// made through another entrance (the same role as the music-generation service's
// GET /jobs/{jobID}/prompt). Flow Music takes the song's audio and one text, so what is returned is
// a direction note for the whole song, not Veo's per-cut text (prompt.FlowMusicBrief). The text is
// not stored and is rebuilt from the stored recipe every time.
func (h *Handler) JobPrompt(w http.ResponseWriter, r *http.Request) {
	jobID := strings.TrimSpace(chi.URLParam(r, "jobID"))
	if err := jobid.Validate(jobID); err != nil {
		respond.Error(w, r, http.StatusBadRequest, err.Error())
		return
	}
	if h.HistoryRepository == nil {
		respond.Error(w, r, http.StatusInternalServerError, "history storage adapter is not configured")
		return
	}
	recipe, err := h.HistoryRepository.GetRecipe(r.Context(), jobID)
	if err != nil {
		slog.ErrorContext(r.Context(), "failed to get job recipe", "job_id", jobID, "error", err)
		respond.Error(w, r, http.StatusNotFound, "job not found")
		return
	}

	recipe.Normalize()
	text := prompt.FlowMusicBrief(recipe, h.Characters)

	if !respond.WantsJSON(w, r) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(text))
		return
	}
	respond.JSON(w, r, http.StatusOK, jobPromptResponse{
		JobID:           jobID,
		Prompt:          text,
		ReferenceImages: h.promptReferenceImages(recipe),
	})
}

// promptReferenceImages returns the art of the characters appearing in the recipe, in order of first appearance.
func (h *Handler) promptReferenceImages(recipe *domain.VideoRecipe) []promptReferenceImage {
	if h.Characters == nil {
		return nil
	}
	var images []promptReferenceImage
	seen := map[string]bool{}
	for _, cut := range recipe.Cuts {
		id := strings.TrimSpace(cut.CharacterID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		char := h.Characters.GetCharacter(id)
		if char == nil {
			continue
		}
		if uri := char.ReferenceURLFor(recipe.AspectRatio); uri != "" {
			images = append(images, promptReferenceImage{CharacterID: id, Name: char.Name, URI: uri})
		}
	}
	return images
}
