package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/shouni/go-utils/jobid"
	"github.com/shouni/go-veo-orchestrator/video"

	"github.com/shouni/ap-mv/internal/domain"

	"github.com/shouni/gcp-kit/negotiate"
)

// maxRecipeJSONSize caps the accepted recipe body. Mirrors the pipeline's read limit
// (filter.maxRecipeJSONSize): it is the same document read from the other direction.
const maxRecipeJSONSize = 5 * 1024 * 1024

// GetJobRecipe returns a job's stored VideoRecipe as {"job_id": ..., "recipe": {...}}.
//
// 表示用に整形する履歴詳細とは別経路です。読んだものをそのまま直して PutJobRecipe へ
// 返せる形にしてあり、署名 URL や概算コストのような表示専用の値は混ざりません。
func (h *Handler) GetJobRecipe(w http.ResponseWriter, r *http.Request) {
	jobID := strings.TrimSpace(chi.URLParam(r, "jobID"))
	if err := jobid.Validate(jobID); err != nil {
		negotiate.Error(w, r, http.StatusBadRequest, err.Error())
		return
	}
	if h.HistoryRepository == nil {
		negotiate.Error(w, r, http.StatusInternalServerError, "history storage adapter is not configured")
		return
	}
	recipe, err := h.HistoryRepository.GetRecipe(r.Context(), jobID)
	if err != nil {
		slog.ErrorContext(r.Context(), "failed to get job recipe", "job_id", jobID, "error", err)
		negotiate.Error(w, r, http.StatusNotFound, "job not found")
		return
	}
	negotiate.JSON(w, r, http.StatusOK, map[string]any{
		"job_id": jobID,
		"recipe": recipe,
	})
}

// PutJobRecipe overwrites a job's VideoRecipe.
//
// 本文は GetJobRecipe の応答と同じ形（{"recipe": {...}}）を受け取ります。VideoRecipe を
// そのまま本文にした形も受け付けます（"cuts" を持つ物体はレシピ本体としか解釈しようが
// ないため、取り違えは起きません）。
//
// **台本のみのジョブ（StageScript）でしか許しません。** キーフレームを焼いた後に
// カット割りを差し替えると、保存済みの keyframe_reference が別のカットを指すことに
// なり、絵と台本の対応が静かに壊れます。焼き直したいカットは再生成の導線を使います。
//
// 注意: ここで保存した尺は、生成時に SceneSplitFilter が Veo のサポート尺へ丸め直します。
func (h *Handler) PutJobRecipe(w http.ResponseWriter, r *http.Request) {
	jobID := strings.TrimSpace(chi.URLParam(r, "jobID"))
	if err := jobid.Validate(jobID); err != nil {
		negotiate.Error(w, r, http.StatusBadRequest, err.Error())
		return
	}
	if h.HistoryRepository == nil {
		negotiate.Error(w, r, http.StatusInternalServerError, "history storage adapter is not configured")
		return
	}

	current, err := h.HistoryRepository.GetRecipe(r.Context(), jobID)
	if err != nil {
		negotiate.Error(w, r, http.StatusNotFound, "job not found")
		return
	}
	if stage := domain.NewJobProgress(current.Cuts).Stage; stage != domain.StageScript {
		negotiate.Error(w, r, http.StatusConflict,
			"recipe can only be edited before keyframes are generated (current stage: "+string(stage)+")")
		return
	}

	raw, err := io.ReadAll(io.LimitReader(r.Body, maxRecipeJSONSize))
	if err != nil {
		negotiate.Error(w, r, http.StatusBadRequest, "failed to read request body")
		return
	}
	recipeJSON, err := recipeJSONFromBody(raw)
	if err != nil {
		negotiate.Error(w, r, http.StatusBadRequest, err.Error())
		return
	}
	recipe, err := domain.DecodeVideoRecipeJSON(recipeJSON)
	if err != nil {
		negotiate.Error(w, r, http.StatusBadRequest, "invalid video recipe json: "+err.Error())
		return
	}

	if err := h.HistoryRepository.SaveRecipe(r.Context(), jobID, recipe); err != nil {
		slog.ErrorContext(r.Context(), "failed to save job recipe", "job_id", jobID, "error", err)
		// SaveRecipe はレシピの検証と GCS への書き込みの両方で失敗しうる。検証失敗
		// （ErrRecipeInvalid）だけが呼び出し側の誤りで、ストレージ障害を 400 で返すと
		// 「あなたのレシピが悪い」という嘘になる。
		status := http.StatusInternalServerError
		if errors.Is(err, video.ErrRecipeInvalid) {
			status = http.StatusBadRequest
		}
		negotiate.Error(w, r, status, err.Error())
		return
	}

	// 保存後のカット数と尺を返す。直す目的はカット割りの確認なので、反映結果を
	// もう一度読みに行かせる必要はない。
	negotiate.JSON(w, r, http.StatusOK, map[string]any{
		"job_id":             jobID,
		"status":             "updated",
		"cut_count":          len(recipe.Cuts),
		"total_duration_sec": domain.TotalDurationSecOfCuts(recipe.Cuts),
	})
}

// recipeJSONFromBody extracts the VideoRecipe JSON from an update request body, accepting
// either {"recipe": {...}} (the GET response shape) or a bare VideoRecipe object.
func recipeJSONFromBody(raw []byte) ([]byte, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, errors.New("request body is empty")
	}
	var envelope struct {
		Recipe json.RawMessage `json:"recipe"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, errors.New("invalid json body: " + err.Error())
	}
	if len(bytes.TrimSpace(envelope.Recipe)) > 0 {
		return envelope.Recipe, nil
	}
	return raw, nil
}
