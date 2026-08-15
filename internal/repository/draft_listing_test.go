package repository

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-veo-orchestrator/video"

	"github.com/shouni/ap-mv/internal/domain"
)

// draftStore は、下書き CRUD の読み書きを1つのインメモリ・マップで受けるフェイクです。
// fakeHistoryReader と違い List がプレフィックスで絞るため、drafts/ と jobs/ の
// 2 名前空間を併用する DeleteDraft の挙動を検証できます。
type draftStore struct {
	mu    sync.Mutex
	files map[string][]byte
}

func newDraftStore() *draftStore {
	return &draftStore{files: map[string][]byte{}}
}

func (s *draftStore) Open(_ context.Context, p string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.files[p]
	if !ok {
		return nil, errors.New("object not found: " + p)
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (s *draftStore) List(_ context.Context, prefix string, callback func(path string) error, _ ...remoteio.ListOption) error {
	s.mu.Lock()
	var paths []string
	for p := range s.files {
		if strings.HasPrefix(p, prefix) {
			paths = append(paths, p)
		}
	}
	s.mu.Unlock()
	for _, p := range paths {
		if err := callback(p); err != nil {
			return err
		}
	}
	return nil
}

func (s *draftStore) Exists(_ context.Context, p string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.files[p]
	return ok, nil
}

func (s *draftStore) Write(_ context.Context, p string, r io.Reader, _ ...remoteio.WriteOption) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files[p] = data
	return nil
}

func (s *draftStore) Delete(_ context.Context, p string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.files, p)
	return nil
}

const draftRepoTestJobID = "video-draft-20260804-101112-abcd1234"

func newDraftRepository(store *draftStore) *VideoHistoryRepository {
	return NewVideoHistoryRepository(VideoHistoryRepositoryConfig{
		BaseURI:      "gs://bucket/ap-mv/veo/jobs",
		DraftBaseURI: "gs://bucket/ap-mv/veo/drafts",
		Reader:       store,
		Writer:       store,
	})
}

func draftTestRecipe() *domain.VideoRecipe {
	return &domain.VideoRecipe{
		ProjectTitle: "下書きテスト",
		AspectRatio:  "9:16",
		MusicRecipe: domain.MusicRecipe{
			Title: "下書きテスト",
			Mood:  "sentimental",
			Tempo: 120,
			Sections: []domain.MusicSection{
				{Name: "Verse", Duration: 12, StartSeconds: 0, EndSeconds: 12, Prompt: "pulse"},
			},
		},
		Cuts: []video.Cut{
			{CutIndex: 1, VisualAnchor: "rooftop", AudioSync: video.AudioSync{DurationSec: 6}},
			{CutIndex: 2, VisualAnchor: "street", AudioSync: video.AudioSync{DurationSec: 6}},
		},
	}
}

// TestDraftRecipeSaveGetRoundTrip は、下書きの保存 → 取得の往復でレシピが保たれることを
// 検証します。これは ap-mcp の update_video_draft / get_video_draft が使うレビューループの
// ストレージ層そのものです（このファイル全体が以前はテストゼロでした）。
func TestDraftRecipeSaveGetRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := newDraftStore()
	repo := newDraftRepository(store)

	if err := repo.SaveDraftRecipe(ctx, draftRepoTestJobID, draftTestRecipe()); err != nil {
		t.Fatalf("SaveDraftRecipe() error = %v", err)
	}

	wantURI := "gs://bucket/ap-mv/veo/drafts/" + draftRepoTestJobID + "/" + domain.VideoDraftFileName
	if _, ok := store.files[wantURI]; !ok {
		t.Fatalf("draft was not written to %s (files: %v)", wantURI, storeKeys(store))
	}

	got, err := repo.GetDraftRecipe(ctx, draftRepoTestJobID)
	if err != nil {
		t.Fatalf("GetDraftRecipe() error = %v", err)
	}
	if got.ProjectTitle != "下書きテスト" || len(got.Cuts) != 2 || got.AspectRatio != "9:16" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

// TestSaveDraftRecipeRejectsInvalidRecipe は、キーフレーム生成で必ず落ちるレシピ
// （カット無し）が下書きとして保存されないこと、そしてその失敗が ErrRecipeInvalid で
// 分類できること（handler が 400/500 を出し分ける根拠）を検証します。
func TestSaveDraftRecipeRejectsInvalidRecipe(t *testing.T) {
	store := newDraftStore()
	repo := newDraftRepository(store)

	invalid := &domain.VideoRecipe{ProjectTitle: "empty cuts"}
	err := repo.SaveDraftRecipe(context.Background(), draftRepoTestJobID, invalid)
	if err == nil {
		t.Fatal("SaveDraftRecipe() must reject a recipe with no cuts")
	}
	if !errors.Is(err, video.ErrRecipeInvalid) {
		t.Fatalf("error = %v, want ErrRecipeInvalid for validation failures", err)
	}
	if len(store.files) != 0 {
		t.Errorf("invalid draft must not be written: %v", storeKeys(store))
	}
}

// TestListDraftPageBuildsDisplayModel は、一覧が下書き JSON から表示モデル
// （カット数・セクション数・合計尺・アスペクト比）を算出することを検証します。
func TestListDraftPageBuildsDisplayModel(t *testing.T) {
	ctx := context.Background()
	store := newDraftStore()
	repo := newDraftRepository(store)

	if err := repo.SaveDraftRecipe(ctx, draftRepoTestJobID, draftTestRecipe()); err != nil {
		t.Fatalf("SaveDraftRecipe() error = %v", err)
	}

	page, err := repo.ListDraftPage(ctx, 1, 10)
	if err != nil {
		t.Fatalf("ListDraftPage() error = %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("drafts = %d, want 1", len(page.Items))
	}
	draft := page.Items[0]
	if draft.JobID != draftRepoTestJobID || draft.CutCount != 2 || draft.SectionCount != 1 {
		t.Errorf("draft display model mismatch: %+v", draft)
	}
	if draft.AspectRatio != "9:16" {
		t.Errorf("AspectRatio = %q, want 9:16", draft.AspectRatio)
	}
	if draft.TotalDurationSec <= 0 {
		t.Errorf("TotalDurationSec = %v, want positive", draft.TotalDurationSec)
	}
}

// TestDeleteDraftRemovesBothNamespaces は、下書き削除が drafts/ 配下の本体と
// jobs/ 配下の進行状況（status.json）の両方を消すことを検証します。片方だけだと、
// 履歴にも下書き一覧にも現れないディレクトリが jobs 配下に残り続けます。
func TestDeleteDraftRemovesBothNamespaces(t *testing.T) {
	ctx := context.Background()
	store := newDraftStore()
	repo := newDraftRepository(store)

	if err := repo.SaveDraftRecipe(ctx, draftRepoTestJobID, draftTestRecipe()); err != nil {
		t.Fatalf("SaveDraftRecipe() error = %v", err)
	}
	statusURI := "gs://bucket/ap-mv/veo/jobs/" + draftRepoTestJobID + "/status.json"
	store.files[statusURI] = []byte(`{"state":"succeeded"}`)

	if err := repo.DeleteDraft(ctx, draftRepoTestJobID); err != nil {
		t.Fatalf("DeleteDraft() error = %v", err)
	}
	if len(store.files) != 0 {
		t.Errorf("delete left objects behind: %v", storeKeys(store))
	}
}

// TestGetDraftRecipeRejectsInvalidJobID は、パス連結に使われる jobID の検証が
// 効いていることを検証します（`../` のような値で別プレフィックスを読ませない）。
func TestGetDraftRecipeRejectsInvalidJobID(t *testing.T) {
	repo := newDraftRepository(newDraftStore())

	if _, err := repo.GetDraftRecipe(context.Background(), "../jobs/other"); err == nil {
		t.Fatal("GetDraftRecipe() must reject an invalid job ID")
	}
}

func storeKeys(s *draftStore) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]string, 0, len(s.files))
	for k := range s.files {
		keys = append(keys, k)
	}
	return keys
}
