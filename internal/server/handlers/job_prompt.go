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

// jobPromptResponse は GET /jobs/{jobID}/prompt の JSON 表現です。
type jobPromptResponse struct {
	JobID string `json:"job_id"`
	// Prompt は Google Flow Music の MV 作成へ貼る本文です（text/plain の応答と同じ）。
	// 曲の音源と一緒に渡す、曲全体の演出メモ 1 本です。
	Prompt string `json:"prompt"`
	// ReferenceImages は、本文と一緒に添えるとキャラクターの見た目が揃う立ち絵です。
	ReferenceImages []promptReferenceImage `json:"reference_images,omitempty"`
}

// promptReferenceImage はキャラクター 1 人ぶんの立ち絵の在り処です。
type promptReferenceImage struct {
	CharacterID string `json:"character_id"`
	Name        string `json:"name"`
	URI         string `json:"uri"`
}

// JobPrompt は、ジョブのレシピを Google Flow Music の MV 作成へ貼る本文にして返します。
//
// 同じ MV を別の入口で作りたいときに、レシピの JSON ではなく貼れる本文を渡すための経路です
// （ap-music の GET /jobs/{jobID}/prompt と同じ位置づけ）。Flow Music は曲の音源と本文 1 本を
// 受け取るので、返すのは Veo のカット別本文ではなく、曲全体の演出メモです
// （prompt.FlowMusicBrief）。本文は保存しておらず、保存済みレシピから毎回組み直します。
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

// promptReferenceImages は、レシピに登場するキャラクターの立ち絵を初出順で返します。
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
