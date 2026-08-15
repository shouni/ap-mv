package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/shouni/go-veo-orchestrator/video"

	"github.com/shouni/ap-mv/assets"
	"github.com/shouni/ap-mv/internal/domain"
)

type fakeDraftRepository struct {
	page    domain.VideoDraftPage
	recipe  *domain.VideoRecipe
	err     error
	deleted []string
	// saved records the last SaveDraftRecipe call so update tests can assert what was persisted.
	saved      *domain.VideoRecipe
	saveJobID  string
	saveErr    error
	saveCalled bool
}

func (f *fakeDraftRepository) SaveDraftRecipe(_ context.Context, jobID string, recipe *domain.VideoRecipe) error {
	f.saveCalled = true
	if f.saveErr != nil {
		return f.saveErr
	}
	f.saveJobID = jobID
	f.saved = recipe
	return nil
}

func (f *fakeDraftRepository) ListDraftPage(context.Context, int, int) (domain.VideoDraftPage, error) {
	return f.page, f.err
}

func (f *fakeDraftRepository) GetDraftRecipe(_ context.Context, jobID string) (*domain.VideoRecipe, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.recipe == nil {
		return nil, errors.New("no draft for " + jobID)
	}
	return f.recipe, nil
}

func (f *fakeDraftRepository) DeleteDraft(_ context.Context, jobID string) error {
	if f.err != nil {
		return f.err
	}
	f.deleted = append(f.deleted, jobID)
	return nil
}

const draftTestJobID = "video-draft-20260804-101112-abc"

func newDraftHandler(t *testing.T, repo *fakeDraftRepository) *Handler {
	t.Helper()
	h, err := NewHandler(assets.Templates, nil)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	h.DraftRepository = repo
	return h
}

func draftRequestWithJobID(t *testing.T, method, target, jobID string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("jobID", jobID)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeContext))
}

// TestDraftsRendersCutBudget checks that the list surfaces what the draft gate exists for: how
// many keyframes the draft would cost and how long the cuts run.
func TestDraftsRendersCutBudget(t *testing.T) {
	h := newDraftHandler(t, &fakeDraftRepository{
		page: domain.VideoDraftPage{Items: []domain.VideoDraft{{
			JobID:            draftTestJobID,
			Title:            "軌跡のアーキテクト",
			CutCount:         12,
			SectionCount:     4,
			TotalDurationSec: 186,
			AspectRatio:      "9:16",
			StorageURI:       "gs://bucket/ap-mv/veo/drafts/" + draftTestJobID + "/video_recipe_draft.json",
		}}},
	})

	rec := httptest.NewRecorder()
	h.Drafts(rec, httptest.NewRequest(http.MethodGet, "/web/drafts", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("Drafts status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"軌跡のアーキテクト",
		">12<",
		"186 sec",
		// 下書きから本生成へ渡す導線は recipe_url で、詳細画面を経由しない。
		`name="recipe_url"`,
		"gs://bucket/ap-mv/veo/drafts/" + draftTestJobID + "/video_recipe_draft.json",
		`action="/web/generate-from-recipe"`,
		// 下書きのアスペクト比は明示的にフォームで運ぶ。渡さないとプロセス既定
		// （16:9）で生成され、9:16 の下書きから 16:9 の動画ができてしまう。
		`name="aspect_ratio" value="9:16"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Drafts body missing %q", want)
		}
	}
}

// TestDraftReturnsJSONRecipe pins the ap-mcp path: Accept: application/json gets the recipe.
func TestDraftReturnsJSONRecipe(t *testing.T) {
	h := newDraftHandler(t, &fakeDraftRepository{
		recipe: &domain.VideoRecipe{
			ProjectTitle: "draft test",
			Cuts:         []video.Cut{{CutIndex: 1, VisualAnchor: "rooftop"}},
		},
	})

	req := draftRequestWithJobID(t, http.MethodGet, "/web/drafts/"+draftTestJobID, draftTestJobID)
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()

	h.Draft(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Draft status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got struct {
		JobID  string             `json:"job_id"`
		Recipe domain.VideoRecipe `json:"recipe"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, rec.Body.String())
	}
	if got.JobID != draftTestJobID {
		t.Errorf("job_id = %q, want %q", got.JobID, draftTestJobID)
	}
	if len(got.Recipe.Cuts) != 1 {
		t.Errorf("recipe.cuts = %d, want 1", len(got.Recipe.Cuts))
	}
}

// TestDraftRedirectsBrowsersToList pins the deliberate absence of a draft detail page: a browser
// hitting the JSON endpoint lands back on the list, which has the delete and generate actions.
func TestDraftRedirectsBrowsersToList(t *testing.T) {
	h := newDraftHandler(t, &fakeDraftRepository{})

	rec := httptest.NewRecorder()
	h.Draft(rec, draftRequestWithJobID(t, http.MethodGet, "/web/drafts/"+draftTestJobID, draftTestJobID))

	if rec.Code != http.StatusFound {
		t.Fatalf("Draft status = %d, want %d", rec.Code, http.StatusFound)
	}
	if location := rec.Header().Get("Location"); location != "/web/drafts" {
		t.Errorf("Location = %q, want %q", location, "/web/drafts")
	}
}

func draftUpdateRequest(t *testing.T, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/web/drafts/"+draftTestJobID, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("jobID", draftTestJobID)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeContext))
}

const draftUpdateRecipeJSON = `{
  "project_title": "updated",
  "cuts": [
    {"cut_index": 1, "section_index": 1, "visual_anchor": "rewritten anchor", "duration_sec": 8, "start_sec": 0, "end_sec": 8}
  ]
}`

// TestUpdateDraftAcceptsEnvelopeShape pins the round trip an agent actually performs: what
// GET /web/drafts/{jobID} returns ({"recipe": ...}) can be edited and PUT back unchanged in shape.
func TestUpdateDraftAcceptsEnvelopeShape(t *testing.T) {
	repo := &fakeDraftRepository{}
	h := newDraftHandler(t, repo)

	rec := httptest.NewRecorder()
	h.UpdateDraft(rec, draftUpdateRequest(t, `{"job_id":"`+draftTestJobID+`","recipe":`+draftUpdateRecipeJSON+`}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("UpdateDraft status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !repo.saveCalled {
		t.Fatal("SaveDraftRecipe was not called")
	}
	if repo.saveJobID != draftTestJobID {
		t.Errorf("saved job ID = %q, want %q", repo.saveJobID, draftTestJobID)
	}
	if repo.saved == nil || len(repo.saved.Cuts) != 1 {
		t.Fatalf("saved recipe = %+v, want 1 cut", repo.saved)
	}
	if got := repo.saved.Cuts[0].VisualAnchor; got != "rewritten anchor" {
		t.Errorf("saved visual anchor = %q, want the edited value", got)
	}
	var out struct {
		Status           string  `json:"status"`
		CutCount         int     `json:"cut_count"`
		TotalDurationSec float64 `json:"total_duration_sec"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out.Status != "updated" || out.CutCount != 1 || out.TotalDurationSec != 8 {
		t.Errorf("response = %+v, want status=updated cut_count=1 total_duration_sec=8", out)
	}
}

// TestUpdateDraftAcceptsBareRecipe covers the other accepted shape: the VideoRecipe alone.
func TestUpdateDraftAcceptsBareRecipe(t *testing.T) {
	repo := &fakeDraftRepository{}
	h := newDraftHandler(t, repo)

	rec := httptest.NewRecorder()
	h.UpdateDraft(rec, draftUpdateRequest(t, draftUpdateRecipeJSON))

	if rec.Code != http.StatusOK {
		t.Fatalf("UpdateDraft status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if repo.saved == nil || len(repo.saved.Cuts) != 1 {
		t.Fatalf("saved recipe = %+v, want 1 cut", repo.saved)
	}
}

// TestUpdateDraftRejectsInvalidRecipe pins that a repository-level validation failure surfaces as
// a 400 rather than overwriting the draft with something keyframe generation would choke on.
// 検証失敗は video.ErrRecipeInvalid を包んで返るのが実リポジトリの挙動で、
// ストレージ障害（500 になる）とはこのセンチネルで区別される。
func TestUpdateDraftRejectsInvalidRecipe(t *testing.T) {
	repo := &fakeDraftRepository{saveErr: fmt.Errorf("%w: video recipe requires cuts", video.ErrRecipeInvalid)}
	h := newDraftHandler(t, repo)

	rec := httptest.NewRecorder()
	h.UpdateDraft(rec, draftUpdateRequest(t, `{"recipe":{"project_title":"x","cuts":[]}}`))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("UpdateDraft status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

// TestUpdateDraftStorageFailureIsServerError pins that a write/GCS failure is reported as the
// server's fault (500), not the caller's (400) — a storage outage must not read as "your
// recipe is wrong".
func TestUpdateDraftStorageFailureIsServerError(t *testing.T) {
	repo := &fakeDraftRepository{saveErr: errors.New("write draft (gs://bucket/x): connection reset")}
	h := newDraftHandler(t, repo)

	rec := httptest.NewRecorder()
	h.UpdateDraft(rec, draftUpdateRequest(t, `{"recipe":{"project_title":"x","cuts":[{"cut_index":1}]}}`))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("UpdateDraft status = %d, want %d; body=%s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
}

func TestUpdateDraftRejectsEmptyBody(t *testing.T) {
	repo := &fakeDraftRepository{}
	h := newDraftHandler(t, repo)

	rec := httptest.NewRecorder()
	h.UpdateDraft(rec, draftUpdateRequest(t, ""))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("UpdateDraft status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if repo.saveCalled {
		t.Error("SaveDraftRecipe was called for an empty body; want no write")
	}
}

func TestDeleteDraftRejectsInvalidJobID(t *testing.T) {
	repo := &fakeDraftRepository{}
	h := newDraftHandler(t, repo)

	rec := httptest.NewRecorder()
	h.DeleteDraft(rec, draftRequestWithJobID(t, http.MethodDelete, "/web/drafts/../etc", "../etc"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("DeleteDraft status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if len(repo.deleted) != 0 {
		t.Errorf("deleted = %v, want no deletions for an invalid job ID", repo.deleted)
	}
}

func TestDeleteDraftDeletes(t *testing.T) {
	repo := &fakeDraftRepository{}
	h := newDraftHandler(t, repo)

	rec := httptest.NewRecorder()
	h.DeleteDraft(rec, draftRequestWithJobID(t, http.MethodDelete, "/web/drafts/"+draftTestJobID, draftTestJobID))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("DeleteDraft status = %d, want %d; body=%s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if len(repo.deleted) != 1 || repo.deleted[0] != draftTestJobID {
		t.Errorf("deleted = %v, want [%s]", repo.deleted, draftTestJobID)
	}
}
