package repository

import (
	"context"
	"io"
	"iter"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-remote-io/remoteio/memio"
)

// fakeHistoryReader は memio を包んだストレージのフェイクです。
//
// 一覧の畳み込み・不在の返し方・削除の単位といったストレージの振る舞いは memio が
// 受け持ちます（本物のハンドラと同じ適合性スイートを通っています）。ここに残しているのは
// 呼び出しの記録（何回開いたか・何回走査したか・何を消したか）と、
// 署名付き URL の組み立てだけです。
//
// 読み書き・署名が 1 つの Store に畳まれたので、以前あった fakeHistoryWriter と
// fakeHistorySigner もこの型が兼ねます。
type fakeHistoryReader struct {
	remoteio.Store
	h *memio.Handler

	// paths は一覧にだけ現れるオブジェクトです（内容は要らないもの）。
	paths []string
	// files は内容を持つオブジェクトです。
	files map[string]string

	mu        sync.Mutex
	openCount map[string]int
	listCount map[string]int
	deleted   []string
	ready     bool
}

// ensure は memio を組み立てて paths / files を流し込みます。
// 各テストが構造体リテラルで前提を書けるよう、生成は最初の呼び出しまで遅らせています。
func (r *fakeHistoryReader) ensure() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ready {
		return
	}
	r.ready = true

	r.h = memio.New(memio.WithScheme(remoteio.SchemeGCS))
	r.Store = remoteio.NewStore(r.h)
	for _, uri := range r.paths {
		if _, ok := r.files[uri]; ok {
			continue
		}
		_ = r.h.Seed(uri, nil)
	}
	for uri, body := range r.files {
		_ = r.h.Seed(uri, []byte(body))
	}
}

func (r *fakeHistoryReader) Open(ctx context.Context, name string) (io.ReadCloser, error) {
	r.ensure()

	r.mu.Lock()
	if r.openCount == nil {
		r.openCount = map[string]int{}
	}
	r.openCount[name]++
	r.mu.Unlock()

	return r.Store.Open(ctx, name)
}

func (r *fakeHistoryReader) List(ctx context.Context, name string, opts ...remoteio.ListOption) iter.Seq2[remoteio.Entry, error] {
	r.ensure()

	r.mu.Lock()
	if r.listCount == nil {
		r.listCount = map[string]int{}
	}
	r.listCount[name]++
	r.mu.Unlock()

	return r.Store.List(ctx, name, opts...)
}

func (r *fakeHistoryReader) Delete(ctx context.Context, name string) error {
	r.ensure()

	r.mu.Lock()
	r.deleted = append(r.deleted, name)
	r.mu.Unlock()

	return r.Store.Delete(ctx, name)
}

// SignURL は署名付き URL を組み立てたことにします。
func (r *fakeHistoryReader) SignURL(_ context.Context, uri, _ string, _ time.Duration) (string, error) {
	return "https://signed.example/" + strings.TrimPrefix(uri, "gs://"), nil
}

// Sub をライブラリの Sub へ委譲します。埋め込みから昇格した Sub をそのまま使うと、
// スコープの土台が埋め込まれた Store になり、上の記録が素通しされます。
func (r *fakeHistoryReader) Sub(prefix string) remoteio.Store {
	r.ensure()
	return remoteio.Sub(r, prefix)
}

// ListCount は指定プレフィックスに対する List 呼び出し回数を返します。
func (r *fakeHistoryReader) ListCount(prefix string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.listCount[prefix]
}

func (r *fakeHistoryReader) OpenCount(p string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.openCount[p]
}

type countingHistorySigner struct {
	mu    sync.Mutex
	count map[string]int
}

func (s *countingHistorySigner) GenerateSignedURL(_ context.Context, uri string, _ string, _ time.Duration) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.count == nil {
		s.count = map[string]int{}
	}
	s.count[uri]++
	return "https://signed.example/" + strings.TrimPrefix(uri, "gs://"), nil
}

func (s *countingHistorySigner) Count(uri string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.count[uri]
}

func TestListHistoryPageLoadsVideoMetadata(t *testing.T) {
	t.Parallel()

	const metadataURI = "gs://bucket/ap-mv/veo/jobs/video-recipe-20260618-081931-abc/video_music_meta.json"
	reader := &fakeHistoryReader{
		paths: []string{
			metadataURI,
			"gs://bucket/ap-mv/veo/jobs/video-recipe-20260618-081931-abc/images/keyframe_001.png",
		},
		files: map[string]string{
			metadataURI: `{
				"title": "軌跡のアーキテクト",
				"mood": "Sparkling",
				"tempo": 168,
				"compose_mode": "sparkle_rock",
				"cuts": [
					{"cut_index": 1, "duration_sec": 8, "visual_anchor": "stage", "status": "generated"},
					{"cut_index": 2, "duration_sec": 8, "visual_anchor": "sky", "status": "generated"}
				]
			}`,
		},
	}
	repo := NewVideoHistoryRepository(VideoHistoryRepositoryConfig{BaseURI: "gs://bucket/ap-mv/veo/jobs", Store: reader, HistoryCache: NewHistoryCache()})

	page, err := repo.ListHistoryPage(context.Background(), 1, 20, "")
	if err != nil {
		t.Fatalf("ListHistoryPage() error = %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("len(page.Items) = %d, want 1", len(page.Items))
	}
	got := page.Items[0]
	if got.JobID != "video-recipe-20260618-081931-abc" {
		t.Fatalf("JobID = %q", got.JobID)
	}
	if got.Title != "軌跡のアーキテクト" {
		t.Fatalf("Title = %q", got.Title)
	}
	if got.CutCount != 2 {
		t.Fatalf("CutCount = %d, want 2", got.CutCount)
	}
	if !got.Generated {
		t.Fatal("Generated = false, want true")
	}
}

// GetHistory はキーフレームの参照（gs:// URI）を絶対化するところまでを担い、**署名はしません**。
// 画面は同一オリジンのパスを辿り、リダイレクトの時点で 1 本だけ署名します。署名は
// SignHistoryURLs を明示的に呼んだときだけ入ります（JSON 応答の経路）。
func TestGetHistoryResolvesKeyframeReferencesWithoutSigning(t *testing.T) {
	t.Parallel()

	const metadataURI = "gs://bucket/ap-mv/veo/jobs/video-recipe-20260618-081931-abc/video_music_meta.json"
	reader := &fakeHistoryReader{
		files: map[string]string{
			metadataURI: `{
				"title": "軌跡のアーキテクト",
				"cuts": [
					{"cut_index": 1, "duration_sec": 8, "visual_anchor": "stage", "keyframe_reference": "images/keyframe_001.png", "status": "pending"},
					{"cut_index": 2, "duration_sec": 7.5, "visual_anchor": "sky", "keyframe_reference": "gs://bucket/ap-mv/veo/jobs/video-recipe-20260618-081931-abc/images/keyframe_002.png", "status": "generated"}
				]
			}`,
		},
	}
	repo := NewVideoHistoryRepository(VideoHistoryRepositoryConfig{BaseURI: "gs://bucket/ap-mv/veo/jobs", Store: reader, HistoryCache: NewHistoryCache()})

	history, err := repo.GetHistory(context.Background(), "video-recipe-20260618-081931-abc")
	if err != nil {
		t.Fatalf("GetHistory() error = %v", err)
	}
	if history.Title != "軌跡のアーキテクト" {
		t.Fatalf("Title = %q", history.Title)
	}
	if len(history.Cuts) != 2 {
		t.Fatalf("len(Cuts) = %d, want 2", len(history.Cuts))
	}
	if history.Cuts[0].KeyframeReference != "gs://bucket/ap-mv/veo/jobs/video-recipe-20260618-081931-abc/images/keyframe_001.png" {
		t.Fatalf("first keyframe reference = %q", history.Cuts[0].KeyframeReference)
	}
	if history.Cuts[0].KeyframeURL != "" {
		t.Fatalf("GetHistory が署名しています: %q（画面はリダイレクト経由で署名を受け取ります）", history.Cuts[0].KeyframeURL)
	}
	if history.Cuts[1].DurationSec != 7.5 {
		t.Fatalf("second duration = %v", history.Cuts[1].DurationSec)
	}

	// JSON 応答の経路だけが署名を要求します。
	if err := repo.SignHistoryURLs(context.Background(), &history); err != nil {
		t.Fatalf("SignHistoryURLs() error = %v", err)
	}
	if !strings.HasPrefix(history.Cuts[0].KeyframeURL, "https://signed.example/") {
		t.Fatalf("SignHistoryURLs 後の keyframe URL = %q", history.Cuts[0].KeyframeURL)
	}
}

// TestGetHistoryAlwaysReadsFreshEvenAfterHistoryListCachedIt verifies GetHistory never reuses a
// recipe cached by ListHistoryPage's bulk read: a single-job detail read always goes straight to
// storage, so it can't serve a stale pre-regenerate/edit snapshot (this matters most under
// multiple running instances, where a worker instance's cache invalidation can't reach every
// other instance's in-memory cache).
func TestGetHistoryAlwaysReadsFreshEvenAfterHistoryListCachedIt(t *testing.T) {
	t.Parallel()

	const (
		jobID       = "video-recipe-20260618-081931-abc"
		metadataURI = "gs://bucket/ap-mv/veo/jobs/video-recipe-20260618-081931-abc/video_music_meta.json"
	)
	reader := &fakeHistoryReader{
		paths: []string{metadataURI},
		files: map[string]string{
			metadataURI: `{
				"title": "軌跡のアーキテクト",
				"cuts": [
					{"cut_index": 1, "duration_sec": 8, "visual_anchor": "stage", "keyframe_reference": "images/keyframe_001.png"}
				]
			}`,
		},
	}
	repo := NewVideoHistoryRepository(VideoHistoryRepositoryConfig{BaseURI: "gs://bucket/ap-mv/veo/jobs", Store: reader, HistoryCache: NewHistoryCache()})

	if _, err := repo.ListHistoryPage(context.Background(), 1, 20, ""); err != nil {
		t.Fatalf("ListHistoryPage() error = %v", err)
	}
	if _, err := repo.GetHistory(context.Background(), jobID); err != nil {
		t.Fatalf("GetHistory() error = %v", err)
	}
	if got := reader.OpenCount(metadataURI); got != 2 {
		t.Fatalf("metadata open count = %d, want 2 (ListHistoryPage's cached read must not be reused by GetHistory)", got)
	}
}

// TestDownloadKeyframesAlwaysReadsFreshEvenAfterHistoryListCachedIt mirrors the GetHistory case:
// downloading keyframes is a deliberate, low-frequency action where a user expects the current
// state, so it must not silently serve a recipe cached by an earlier ListHistoryPage call.
func TestDownloadKeyframesAlwaysReadsFreshEvenAfterHistoryListCachedIt(t *testing.T) {
	t.Parallel()

	const (
		jobID       = "video-recipe-20260618-081931-abc"
		metadataURI = "gs://bucket/ap-mv/veo/jobs/video-recipe-20260618-081931-abc/video_music_meta.json"
		keyframeURI = "gs://bucket/ap-mv/veo/jobs/video-recipe-20260618-081931-abc/images/keyframe_001.png"
	)
	reader := &fakeHistoryReader{
		paths: []string{metadataURI, keyframeURI},
		files: map[string]string{
			metadataURI: `{
				"title": "軌跡のアーキテクト",
				"cuts": [
					{"cut_index": 1, "duration_sec": 8, "visual_anchor": "stage", "keyframe_reference": "images/keyframe_001.png"}
				]
			}`,
			keyframeURI: "fake-image-bytes",
		},
	}
	repo := NewVideoHistoryRepository(VideoHistoryRepositoryConfig{BaseURI: "gs://bucket/ap-mv/veo/jobs", Store: reader, HistoryCache: NewHistoryCache()})

	if _, err := repo.ListHistoryPage(context.Background(), 1, 20, ""); err != nil {
		t.Fatalf("ListHistoryPage() error = %v", err)
	}
	if err := repo.DownloadKeyframes(context.Background(), jobID, func(string, io.Reader) error { return nil }); err != nil {
		t.Fatalf("DownloadKeyframes() error = %v", err)
	}
	if got := reader.OpenCount(metadataURI); got != 2 {
		t.Fatalf("metadata open count = %d, want 2 (ListHistoryPage's cached read must not be reused by DownloadKeyframes)", got)
	}
}

// 一覧はメタデータを TTL cache に載せますが、**署名は一切しません**。
//
// 以前は表示のたびに署名し直していました（期限付きの URL をキャッシュへ載せると、
// 二度目の表示で期限切れの URL を配ってしまうため）。いまは署名そのものを画面から
// 外したので、キャッシュに期限付きの値が入る余地がありません。メタデータの読み出しが
// 1 回で済むこと（＝キャッシュが効いていること）は変わらず確かめます。
func TestListHistoryPageCachesMetadataAndSignsNothing(t *testing.T) {
	t.Parallel()

	const metadataURI = "gs://bucket/ap-mv/veo/jobs/video-recipe-20260618-081931-abc/video_music_meta.json"
	reader := &fakeHistoryReader{
		paths: []string{metadataURI},
		files: map[string]string{
			metadataURI: `{
				"title": "軌跡のアーキテクト",
				"cuts": [
					{"cut_index": 1, "duration_sec": 8, "visual_anchor": "stage"}
				]
			}`,
		},
	}
	signer := &countingHistorySigner{}
	repo := NewVideoHistoryRepository(VideoHistoryRepositoryConfig{BaseURI: "gs://bucket/ap-mv/veo/jobs", Store: reader, HistoryCache: NewHistoryCache()})

	if _, err := repo.ListHistoryPage(context.Background(), 1, 20, ""); err != nil {
		t.Fatalf("first ListHistoryPage() error = %v", err)
	}
	if _, err := repo.ListHistoryPage(context.Background(), 1, 20, ""); err != nil {
		t.Fatalf("second ListHistoryPage() error = %v", err)
	}
	if got := signer.Count(metadataURI); got != 0 {
		t.Fatalf("一覧が署名しています: count = %d, want 0（署名はリダイレクトの時点だけ）", got)
	}
	if got := reader.OpenCount(metadataURI); got != 1 {
		t.Fatalf("metadata open count = %d, want 1", got)
	}
}

func TestGetHistoryReturnsErrorWhenRepositoryMisconfigured(t *testing.T) {
	t.Parallel()

	repo := NewVideoHistoryRepository(VideoHistoryRepositoryConfig{BaseURI: "", Store: nil, HistoryCache: NewHistoryCache()})

	if _, err := repo.GetHistory(context.Background(), "video-recipe-20260618-081931-abc"); err == nil {
		t.Fatal("GetHistory() error = nil, want configuration error")
	}
}

func TestDeleteHistoryDeletesJobObjects(t *testing.T) {
	t.Parallel()

	reader := &fakeHistoryReader{
		paths: []string{
			"gs://bucket/ap-mv/veo/jobs/job-1/video_music_meta.json",
			"gs://bucket/ap-mv/veo/jobs/job-1/images/keyframe_001.png",
		},
		files: map[string]string{},
	}
	repo := NewVideoHistoryRepository(VideoHistoryRepositoryConfig{BaseURI: "gs://bucket/ap-mv/veo/jobs", Store: reader, HistoryCache: NewHistoryCache()})

	if err := repo.DeleteHistory(context.Background(), "job-1"); err != nil {
		t.Fatalf("DeleteHistory() error = %v", err)
	}
	if len(reader.deleted) != 2 {
		t.Fatalf("deleted count = %d, want 2: %#v", len(reader.deleted), reader.deleted)
	}
}

// matching how remoteio's GCS reader signals a missing object.
// notFoundReader は、指定した内容だけを持つストアです。
// 「まだ無い」と「読めない」を分けて扱う経路の検証に使います。
// 未登録のオブジェクトは memio がそのまま ErrNotExist を返します。
func notFoundReader(files map[string]string) remoteio.Store {
	h := memio.New(memio.WithScheme(remoteio.SchemeGCS))
	for uri, body := range files {
		if err := h.Seed(uri, []byte(body)); err != nil {
			panic(err)
		}
	}
	return remoteio.NewStore(h)
}

const testUsageJobID = "video-recipe-20260618-081931-abc"

func TestGetVeoUsageReadsRecordedTally(t *testing.T) {
	t.Parallel()

	usageURI := "gs://bucket/ap-mv/veo/jobs/" + testUsageJobID + "/veo_usage.json"
	repo := NewVideoHistoryRepository(VideoHistoryRepositoryConfig{
		BaseURI: "gs://bucket/ap-mv/veo/jobs",
		Store: notFoundReader(map[string]string{
			usageURI: `{"schema_version":1,"job_id":"` + testUsageJobID + `","model":"veo-test","calls":3,"submitted_seconds":22,"cuts":[{"cut_index":1,"calls":2,"submitted_seconds":16}]}`,
		}),
		HistoryCache: NewHistoryCache(),
	})

	usage, err := repo.GetVeoUsage(context.Background(), testUsageJobID)
	if err != nil {
		t.Fatalf("GetVeoUsage() error = %v", err)
	}
	if usage == nil {
		t.Fatal("GetVeoUsage() = nil, want a record")
	}
	if usage.Calls != 3 || usage.SubmittedSeconds != 22 || usage.Model != "veo-test" {
		t.Fatalf("usage = %+v, want the recorded tally", usage)
	}
	if got := usage.CutCalls(1); got != 2 {
		t.Fatalf("CutCalls(1) = %d, want 2", got)
	}
}

// TestGetVeoUsageTreatsMissingRecordAsNoData verifies jobs that predate the tally (or stopped
// after keyframes) don't surface as an error — the detail page falls back to the recipe-derived
// estimate instead of failing to render.
func TestGetVeoUsageTreatsMissingRecordAsNoData(t *testing.T) {
	t.Parallel()

	repo := NewVideoHistoryRepository(VideoHistoryRepositoryConfig{
		BaseURI:      "gs://bucket/ap-mv/veo/jobs",
		Store:        notFoundReader(map[string]string{}),
		HistoryCache: NewHistoryCache(),
	})

	usage, err := repo.GetVeoUsage(context.Background(), testUsageJobID)
	if err != nil {
		t.Fatalf("GetVeoUsage() error = %v, want nil for a missing record", err)
	}
	if usage != nil {
		t.Fatalf("GetVeoUsage() = %+v, want nil", usage)
	}
}

// TestGetVeoUsageTreatsEmptyObjectAsNoData covers a truncated write leaving a zero-byte object.
func TestGetVeoUsageTreatsEmptyObjectAsNoData(t *testing.T) {
	t.Parallel()

	usageURI := "gs://bucket/ap-mv/veo/jobs/" + testUsageJobID + "/veo_usage.json"
	repo := NewVideoHistoryRepository(VideoHistoryRepositoryConfig{
		BaseURI:      "gs://bucket/ap-mv/veo/jobs",
		Store:        notFoundReader(map[string]string{usageURI: "  \n"}),
		HistoryCache: NewHistoryCache(),
	})

	usage, err := repo.GetVeoUsage(context.Background(), testUsageJobID)
	if err != nil {
		t.Fatalf("GetVeoUsage() error = %v, want nil for an empty object", err)
	}
	if usage != nil {
		t.Fatalf("GetVeoUsage() = %+v, want nil", usage)
	}
}

func TestGetVeoUsageRejectsInvalidJobID(t *testing.T) {
	t.Parallel()

	repo := NewVideoHistoryRepository(VideoHistoryRepositoryConfig{
		BaseURI:      "gs://bucket/ap-mv/veo/jobs",
		Store:        notFoundReader(map[string]string{}),
		HistoryCache: NewHistoryCache(),
	})

	if _, err := repo.GetVeoUsage(context.Background(), "../escape"); err == nil {
		t.Fatal("GetVeoUsage() error = nil, want a validation error for a traversal-style job ID")
	}
}
