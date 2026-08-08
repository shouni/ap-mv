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

	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"github.com/shouni/ap-mv/internal/domain"
)

// Drafts renders the VideoRecipe draft list, or returns JSON when Accept: application/json is set.
func (h *Handler) Drafts(w http.ResponseWriter, r *http.Request) {
	if h.DraftRepository == nil {
		h.renderPage(w, PageData{Title: "Drafts", Message: "draft storage adapter is not configured yet"}, "drafts.html")
		return
	}
	page, err := h.DraftRepository.ListDraftPage(r.Context(), pageFromQuery(r), 20)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, page)
		return
	}
	h.renderPage(w, PageData{
		Title:      "Drafts",
		CSRFToken:  csrfTokenFromContext(r.Context()),
		DraftItems: page.Items,
		PageMeta:   page.PageMeta,
	}, "drafts.html")
}

// Draft returns a single draft's VideoRecipe as JSON.
//
// 下書きに専用の詳細画面はありません。下書きを確認する相手は ap-mcp 越しの
// エージェントで、人間が読む画面としては一覧に並ぶカット数・尺・セクション数で足ります。
// ブラウザからのアクセスは一覧へ戻します（詳細画面の代わりに JSON を生で見せても、
// 一覧に用意した削除・生成の導線から離れるだけです）。
func (h *Handler) Draft(w http.ResponseWriter, r *http.Request) {
	jobID := strings.TrimSpace(chi.URLParam(r, "jobID"))
	if err := jobid.Validate(jobID); err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	if !wantsJSON(r) {
		http.Redirect(w, r, "/web/drafts", http.StatusFound)
		return
	}
	if h.DraftRepository == nil {
		writeError(w, r, http.StatusInternalServerError, "draft storage adapter is not configured")
		return
	}
	recipe, err := h.DraftRepository.GetDraftRecipe(r.Context(), jobID)
	if err != nil {
		slog.ErrorContext(r.Context(), "failed to get draft recipe",
			"job_id", jobID,
			"error", err,
		)
		writeError(w, r, http.StatusNotFound, "draft not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"job_id": jobID,
		"recipe": recipe,
	})
}

// maxDraftJSONSize caps the accepted draft body. Mirrors the pipeline's recipe read limit
// (filter.maxRecipeJSONSize): a draft is the same document read from the other direction.
const maxDraftJSONSize = 5 * 1024 * 1024

// UpdateDraft overwrites a draft's VideoRecipe.
//
// 本文は GET /web/drafts/{jobID} の応答と同じ形（{"recipe": {...}}）を受け取ります。読んだ
// ものをそのまま直して返せるようにするためです。VideoRecipe をそのまま本文にした形も
// 受け付けます（"cuts" を持つ物体はレシピ本体としか解釈しようがないため、取り違えは
// 起きません）。
//
// 注意: ここで保存した尺（duration_sec / start_sec / end_sec）は、生成時に
// SceneSplitFilter が楽曲タイムラインを正として割り付け直します。編集して意味があるのは
// visual_anchor / audio_cue / character_id / dialogue といったプロンプト側の値です。
func (h *Handler) UpdateDraft(w http.ResponseWriter, r *http.Request) {
	jobID := strings.TrimSpace(chi.URLParam(r, "jobID"))
	if err := jobid.Validate(jobID); err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	if h.DraftRepository == nil {
		writeError(w, r, http.StatusInternalServerError, "draft storage adapter is not configured")
		return
	}

	raw, err := io.ReadAll(io.LimitReader(r.Body, maxDraftJSONSize))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "failed to read request body")
		return
	}
	recipeJSON, err := draftRecipeJSONFromBody(raw)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	recipe, err := domain.DecodeVideoRecipeJSON(recipeJSON)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid video recipe json: "+err.Error())
		return
	}

	if err := h.DraftRepository.SaveDraftRecipe(r.Context(), jobID, recipe); err != nil {
		slog.ErrorContext(r.Context(), "failed to save draft recipe",
			"job_id", jobID,
			"error", err,
		)
		// SaveDraftRecipe はレシピの検証と GCS への書き込みの両方で失敗しうる。
		// 検証失敗（ErrRecipeInvalid）だけが呼び出し側の誤りで、ストレージ障害を
		// 400 で返すと「あなたのレシピが悪い」という嘘になる（以前は全部 400 だった）。
		status := http.StatusInternalServerError
		if errors.Is(err, orchestrator.ErrRecipeInvalid) {
			status = http.StatusBadRequest
		}
		writeError(w, r, status, err.Error())
		return
	}

	// 保存後のカット数と尺を返す。下書きを直す目的はカット割りの確認なので、
	// 反映結果をもう一度読みに行かせる必要はない。
	writeJSON(w, http.StatusOK, map[string]any{
		"job_id":             jobID,
		"status":             "updated",
		"cut_count":          len(recipe.Cuts),
		"total_duration_sec": domain.TotalDurationSecOfCuts(recipe.Cuts),
	})
}

// draftRecipeJSONFromBody extracts the VideoRecipe JSON from an update request body, accepting
// either {"recipe": {...}} (the GET response shape) or a bare VideoRecipe object.
func draftRecipeJSONFromBody(raw []byte) ([]byte, error) {
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

// DeleteDraft handles draft deletion requests.
func (h *Handler) DeleteDraft(w http.ResponseWriter, r *http.Request) {
	jobID := strings.TrimSpace(chi.URLParam(r, "jobID"))
	if err := jobid.Validate(jobID); err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	if h.DraftRepository == nil {
		writeError(w, r, http.StatusServiceUnavailable, "draft storage adapter is not configured")
		return
	}
	if err := h.DraftRepository.DeleteDraft(r.Context(), jobID); err != nil {
		writeError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"job_id": jobID, "status": "deleted"})
}
