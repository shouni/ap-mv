package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"github.com/shouni/ap-mv/assets"
	"github.com/shouni/ap-mv/internal/domain"
)

type fakeDraftRepository struct {
	page    domain.VideoDraftPage
	recipe  *domain.VideoRecipe
	err     error
	deleted []string
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
			AspectRatio:      "16:9",
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
			Cuts:         []orchestrator.Cut{{CutIndex: 1, VisualAnchor: "rooftop"}},
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
