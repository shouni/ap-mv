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

	"github.com/shouni/ap-mv/internal/domain"
)

// fakeHistoryReader is a storage fake wrapping memio.
//
// Storage behaviour such as folding listings, how absence is returned and the unit of deletion
// belongs to memio (it passes the same conformance suite as the real handlers). What remains
// here is recording the calls (how many opens, how many scans, what was deleted) and building
// signed URLs.
//
// Reads, writes and signing have been folded into one Store, so this type also covers the former
// fakeHistoryWriter and fakeHistorySigner.
type fakeHistoryReader struct {
	remoteio.Store
	h *memio.Handler

	// paths are objects that appear only in listings (whose contents are not needed).
	paths []string
	// files are objects with contents.
	files map[string]string

	mu        sync.Mutex
	openCount map[string]int
	listCount map[string]int
	deleted   []string
	ready     bool
}

// ensure builds the memio and loads paths / files into it.
// Construction is deferred to the first call so each test can state its preconditions as a struct literal.
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

// SignURL pretends to have built a signed URL.
func (r *fakeHistoryReader) SignURL(_ context.Context, uri, _ string, _ time.Duration) (string, error) {
	return "https://signed.example/" + strings.TrimPrefix(uri, "gs://"), nil
}
func TestListHistoryPageBuildsRowsFromJobStatus(t *testing.T) {
	t.Parallel()

	status := domain.JobStatus{
		Mood:             "Sparkling",
		Tempo:            168,
		VisualMode:       "sparkle_rock",
		AspectRatio:      "16:9",
		GeneratedSeconds: 16,
		Progress:         domain.JobProgress{Stage: domain.StageCompleted, TotalCuts: 2, KeyframeCuts: 2, VideoCuts: 2},
	}
	status.JobID = "video-recipe-20260618-081931-abc"
	status.Title = "軌跡のアーキテクト"
	store := newFakeStatusStore(status)
	repo := NewVideoHistoryRepository(VideoHistoryRepositoryConfig{
		BaseURI:   "gs://bucket/ap-mv/veo/jobs",
		Store:     &fakeHistoryReader{},
		JobStatus: store,
	})

	page, err := repo.ListHistoryPage(context.Background(), 1, 20, "")
	if err != nil {
		t.Fatalf("ListHistoryPage() error = %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("len(page.Items) = %d, want 1", len(page.Items))
	}
	got := page.Items[0]
	if got.JobID != status.JobID {
		t.Fatalf("JobID = %q", got.JobID)
	}
	if got.Title != "軌跡のアーキテクト" {
		t.Fatalf("Title = %q", got.Title)
	}
	if got.CutCount != 2 {
		t.Fatalf("CutCount = %d, want 2 (progress.total_cuts is the only cut count)", got.CutCount)
	}
	if !got.Generated {
		t.Fatal("Generated = false, want true")
	}
	// Storage locations are not copied into the document; they are derived from the job ID.
	want := "gs://bucket/ap-mv/veo/jobs/" + status.JobID + "/video_music_meta.json"
	if got.StorageURI != want {
		t.Fatalf("StorageURI = %q, want %q", got.StorageURI, want)
	}
	if got.KeyframeZipURI == "" {
		t.Fatal("KeyframeZipURI is empty")
	}
	// The listing opens no metadata at all (when it did, a page's worth of parallel reads was needed).
	if store.listCalls != 1 {
		t.Fatalf("listCalls = %d, want 1", store.listCalls)
	}
}

// Filtering by stage is done by the query. The stage used to be derivable only from the cut
// list, so filtering read every job's metadata.
func TestListHistoryPageAddsStageFilter(t *testing.T) {
	t.Parallel()

	store := newFakeStatusStore()
	repo := NewVideoHistoryRepository(VideoHistoryRepositoryConfig{
		BaseURI:   "gs://bucket/ap-mv/veo/jobs",
		Store:     &fakeHistoryReader{},
		JobStatus: store,
	})

	if _, err := repo.ListHistoryPage(context.Background(), 2, 10, ""); err != nil {
		t.Fatalf("ListHistoryPage() error = %v", err)
	}
	// Even without a filter the command restriction is always applied (so maintenance jobs do not mix in).
	if store.lastOptions != 1 {
		t.Fatalf("options = %d, want 1 (command only)", store.lastOptions)
	}
	if store.lastPage != 2 || store.lastPerPage != 10 {
		t.Fatalf("page/perPage = %d/%d, want 2/10", store.lastPage, store.lastPerPage)
	}

	if _, err := repo.ListHistoryPage(context.Background(), 1, 10, domain.StageScript); err != nil {
		t.Fatalf("ListHistoryPage(stage) error = %v", err)
	}
	if store.lastOptions != 2 {
		t.Fatalf("options = %d, want 2 (command + progress.stage)", store.lastOptions)
	}
}

// With nowhere to read from, it returns empty. Not being able to list cannot be told from zero
// rows, but Firestore is always built for both the web and worker roles, so it does not happen in practice.
func TestListHistoryPageWithoutJobStatusReturnsEmpty(t *testing.T) {
	t.Parallel()

	repo := NewVideoHistoryRepository(VideoHistoryRepositoryConfig{
		BaseURI: "gs://bucket/ap-mv/veo/jobs",
		Store:   &fakeHistoryReader{},
	})

	page, err := repo.ListHistoryPage(context.Background(), 1, 20, "")
	if err != nil {
		t.Fatalf("ListHistoryPage() error = %v", err)
	}
	if len(page.Items) != 0 {
		t.Fatalf("len(page.Items) = %d, want 0", len(page.Items))
	}
}

// The listing takes the failure and its reason from the status document. The stage is derived
// from the cut list, so a failed job just stops at "keyframes 3/12" and the artifacts do not show it failed.
func TestListHistoryPageCarriesFailureFromJobStatus(t *testing.T) {
	t.Parallel()

	status := domain.JobStatus{Progress: domain.JobProgress{Stage: domain.StageKeyframes, TotalCuts: 12, KeyframeCuts: 3}}
	status.JobID = "video-recipe-20260618-081931-abc"
	status.State = domain.JobStateFailed
	status.Error = "veo operation timed out"
	repo := NewVideoHistoryRepository(VideoHistoryRepositoryConfig{
		BaseURI:   "gs://bucket/ap-mv/veo/jobs",
		Store:     &fakeHistoryReader{},
		JobStatus: newFakeStatusStore(status),
	})

	page, err := repo.ListHistoryPage(context.Background(), 1, 20, "")
	if err != nil {
		t.Fatalf("ListHistoryPage() error = %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("len(page.Items) = %d, want 1", len(page.Items))
	}
	if got := page.Items[0].State; got != domain.JobStateFailed {
		t.Errorf("State = %q, want failed", got)
	}
	if got := page.Items[0].Error; got != "veo operation timed out" {
		t.Errorf("Error = %q", got)
	}
}

// The detail shows the same state. Artifacts remain partway, so if this were empty you would have
// to go digging in Slack or the logs for "why it stopped". It is empty for jobs without a record (pre-migration).
func TestGetHistoryFillsStateFromJobStatus(t *testing.T) {
	t.Parallel()

	const jobID = "video-recipe-20260618-081931-abc"
	const metadataURI = "gs://bucket/ap-mv/veo/jobs/" + jobID + "/video_music_meta.json"
	reader := &fakeHistoryReader{
		files: map[string]string{
			metadataURI: `{"title": "half baked", "cuts": [{"cut_index": 1, "duration_sec": 8, "visual_anchor": "stage", "status": "pending"}]}`,
		},
	}

	status := domain.JobStatus{}
	status.JobID = jobID
	status.State = domain.JobStateFailed
	status.Error = "veo operation timed out"

	repo := NewVideoHistoryRepository(VideoHistoryRepositoryConfig{
		BaseURI:   "gs://bucket/ap-mv/veo/jobs",
		Store:     reader,
		JobStatus: newFakeStatusStore(status),
	})

	detail, err := repo.GetHistory(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetHistory() error = %v", err)
	}
	if detail.State != domain.JobStateFailed || detail.Error != "veo operation timed out" {
		t.Fatalf("State/Error = %q/%q, want failed/veo operation timed out", detail.State, detail.Error)
	}

	// Even a job without a record returns the detail itself.
	repo = NewVideoHistoryRepository(VideoHistoryRepositoryConfig{
		BaseURI:   "gs://bucket/ap-mv/veo/jobs",
		Store:     reader,
		JobStatus: newFakeStatusStore(),
	})
	detail, err = repo.GetHistory(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetHistory() without a status record error = %v", err)
	}
	if detail.State != "" || detail.Title != "half baked" {
		t.Fatalf("State = %q, Title = %q; the detail must be returned even without a record", detail.State, detail.Title)
	}
}

// GetHistory goes only as far as making keyframe references (gs:// URIs) absolute; **it does not sign**.
// The page follows same-origin paths, and exactly one URL is signed at redirect time. Signatures
// are added only when SignHistoryURLs is called explicitly (the JSON response path).
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
	repo := NewVideoHistoryRepository(VideoHistoryRepositoryConfig{BaseURI: "gs://bucket/ap-mv/veo/jobs", Store: reader})

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
		t.Fatalf("GetHistory signed: %q (pages receive signatures through the redirect)", history.Cuts[0].KeyframeURL)
	}
	if history.Cuts[1].DurationSec != 7.5 {
		t.Fatalf("second duration = %v", history.Cuts[1].DurationSec)
	}

	// Only the JSON response path asks for signatures.
	if err := repo.SignHistoryURLs(context.Background(), &history); err != nil {
		t.Fatalf("SignHistoryURLs() error = %v", err)
	}
	if !strings.HasPrefix(history.Cuts[0].KeyframeURL, "https://signed.example/") {
		t.Fatalf("keyframe URL after SignHistoryURLs = %q", history.Cuts[0].KeyframeURL)
	}
}

func TestGetHistoryReturnsErrorWhenRepositoryMisconfigured(t *testing.T) {
	t.Parallel()

	repo := NewVideoHistoryRepository(VideoHistoryRepositoryConfig{BaseURI: "", Store: nil})

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
	repo := NewVideoHistoryRepository(VideoHistoryRepositoryConfig{BaseURI: "gs://bucket/ap-mv/veo/jobs", Store: reader})

	if err := repo.DeleteHistory(context.Background(), "job-1"); err != nil {
		t.Fatalf("DeleteHistory() error = %v", err)
	}
	if len(reader.deleted) != 2 {
		t.Fatalf("deleted count = %d, want 2: %#v", len(reader.deleted), reader.deleted)
	}
}

// matching how remoteio's GCS reader signals a missing object.
// notFoundReader is a store that holds only the given contents.
// It is used to verify paths that treat "not there yet" and "unreadable" differently.
// Unregistered objects make memio return ErrNotExist as-is.
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
		BaseURI: "gs://bucket/ap-mv/veo/jobs",
		Store:   notFoundReader(map[string]string{}),
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
		BaseURI: "gs://bucket/ap-mv/veo/jobs",
		Store:   notFoundReader(map[string]string{usageURI: "  \n"}),
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
		BaseURI: "gs://bucket/ap-mv/veo/jobs",
		Store:   notFoundReader(map[string]string{}),
	})

	if _, err := repo.GetVeoUsage(context.Background(), "../escape"); err == nil {
		t.Fatal("GetVeoUsage() error = nil, want a validation error for a traversal-style job ID")
	}
}
