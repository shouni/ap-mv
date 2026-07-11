package repository

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jellydator/ttlcache/v3"
	"github.com/shouni/go-remote-io/remoteio"
	"golang.org/x/sync/errgroup"

	"github.com/shouni/ap-mv/internal/domain"
)

const (
	videoMetadataFile   = "video_music_meta.json"
	regenKeyframePrefix = "regen-keyframe-"
	// historyFetchConcurrency caps parallel GCS fetches when signing/loading history in bulk.
	historyFetchConcurrency = 10
)

// VideoHistoryRepository lists generated MV metadata from the workflow output directory.
type VideoHistoryRepository struct {
	baseURI      string
	reader       remoteio.InputReader
	writer       remoteio.OutputWriter
	signer       remoteio.URLSigner
	historyCache *ttlcache.Cache[string, domain.VideoHistory]
	recipeCache  *ttlcache.Cache[string, domain.VideoRecipe]
}

// NewVideoHistoryRepository creates a generated MV history repository.
func NewVideoHistoryRepository(baseURI string, reader remoteio.InputReader, writer remoteio.OutputWriter, signer remoteio.URLSigner, historyCache *ttlcache.Cache[string, domain.VideoHistory]) *VideoHistoryRepository {
	if historyCache == nil {
		historyCache = NewHistoryCache()
	}
	return &VideoHistoryRepository{
		baseURI:      strings.TrimRight(strings.TrimSpace(baseURI), "/"),
		reader:       reader,
		writer:       writer,
		signer:       signer,
		historyCache: historyCache,
		recipeCache:  NewVideoRecipeCache(),
	}
}

// ListHistoryPage lists generated MV jobs with paging.
func (r *VideoHistoryRepository) ListHistoryPage(ctx context.Context, page int, perPage int) (domain.VideoHistoryPage, error) {
	if r == nil || r.reader == nil || r.baseURI == "" {
		return domain.VideoHistoryPage{}, nil
	}
	prefix := r.baseURI + "/"
	seen := map[string]bool{}
	var jobIDs []string
	err := r.reader.List(ctx, prefix, func(gcsPath string) error {
		if path.Base(gcsPath) != videoMetadataFile {
			return nil
		}
		// {baseURI}/{jobID}/video_music_meta.json の1階層のみ対象にする。
		// サブディレクトリ（regens/cut-N/ 等）の metadata は除外する。
		// path.Dir は gs:// の // を潰すため strings ベースで深さを確認する。
		// コンストラクタで末尾スラッシュは除去済みだが、防御的に再度除去する。
		baseURI := strings.TrimSuffix(r.baseURI, "/")
		rel := strings.TrimPrefix(gcsPath, baseURI+"/")
		if strings.Count(rel, "/") != 1 {
			return nil
		}
		jobID := path.Base(path.Dir(gcsPath))
		if jobID == "." || jobID == "/" || jobID == "" || seen[jobID] {
			return nil
		}
		if strings.HasPrefix(jobID, regenKeyframePrefix) {
			return nil
		}
		if err := domain.ValidateJobID(jobID); err != nil {
			return nil
		}
		seen[jobID] = true
		jobIDs = append(jobIDs, jobID)
		return nil
	})
	if err != nil {
		return domain.VideoHistoryPage{}, fmt.Errorf("list history objects: %w", err)
	}

	selectedIDs, meta := selectHistoryPageIDs(jobIDs, page, perPage)
	eg, ctx := errgroup.WithContext(ctx)
	eg.SetLimit(historyFetchConcurrency)

	histories := make([]domain.VideoHistory, 0, len(selectedIDs))
	var mu sync.Mutex
	for _, id := range selectedIDs {
		eg.Go(func() error {
			history, err := r.buildHistory(ctx, id)
			if err != nil {
				slog.WarnContext(ctx, "failed to load history metadata",
					"job_id", id,
					"error", err,
				)
				history = domain.VideoHistory{
					JobID:      id,
					Title:      id,
					CreatedAt:  formatHistoryCreatedAt(id),
					StorageURI: r.metadataURI(id),
				}
			}
			mu.Lock()
			histories = append(histories, history)
			mu.Unlock()
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return domain.VideoHistoryPage{}, err
	}
	// 並列取得で順序が崩れるため、ページ選択時と同じ作成日時降順で並べ直す。
	sort.Slice(histories, func(i, j int) bool {
		ti, tj := historyCreatedAtRaw(histories[i].JobID), historyCreatedAtRaw(histories[j].JobID)
		if ti != tj {
			return ti > tj
		}
		return histories[i].JobID > histories[j].JobID
	})

	return domain.VideoHistoryPage{
		Items:    histories,
		PageMeta: adjustPageMetaItemCount(meta, len(histories)),
	}, nil
}

func (r *VideoHistoryRepository) buildHistory(ctx context.Context, jobID string) (domain.VideoHistory, error) {
	recipe, err := r.loadVideoRecipe(ctx, jobID)
	if err != nil {
		return domain.VideoHistory{}, err
	}
	return r.buildHistoryFromRecipe(ctx, jobID, recipe), nil
}

// GetHistory loads generated MV job metadata and cut keyframe references.
//
// Unlike ListHistoryPage, this always reads storage directly rather than the TTL cache: a
// single-job read is cheap, and callers use GetHistory precisely to check the latest state
// right after a regenerate/edit job completes, when a stale cached copy would be most visible
// and confusing (and, under multiple running instances, cache invalidation from the worker
// instance that ran the job can't reach every other instance's in-memory cache anyway).
func (r *VideoHistoryRepository) GetHistory(ctx context.Context, jobID string) (domain.VideoHistoryDetail, error) {
	if r == nil || r.reader == nil || r.baseURI == "" {
		return domain.VideoHistoryDetail{}, errors.New("history repository is not properly configured")
	}
	if err := domain.ValidateJobID(jobID); err != nil {
		return domain.VideoHistoryDetail{}, err
	}
	recipe, err := r.fetchVideoRecipe(ctx, jobID)
	if err != nil {
		return domain.VideoHistoryDetail{}, err
	}
	history := r.buildHistoryFromFreshRecipe(ctx, jobID, recipe)
	detail := domain.VideoHistoryDetail{
		VideoHistory: history,
		Cuts:         make([]domain.VideoHistoryCut, 0, len(recipe.Cuts)),
	}
	for _, cut := range recipe.Cuts {
		keyframeReference := r.resolveJobObjectURI(jobID, strings.TrimSpace(cut.KeyframeReference))
		detail.Cuts = append(detail.Cuts, domain.VideoHistoryCut{
			CutIndex:          cut.CutIndex,
			DurationSec:       cut.DurationSec,
			AudioCue:          strings.TrimSpace(cut.AudioCue),
			VisualAnchor:      strings.TrimSpace(cut.VisualAnchor),
			CharacterID:       strings.TrimSpace(cut.CharacterID),
			Dialogue:          strings.TrimSpace(cut.Dialogue),
			KeyframeReference: keyframeReference,
			VideoURL:          strings.TrimSpace(cut.VideoURL),
			Status:            strings.TrimSpace(string(cut.Status)),
			StartSec:          cut.StartSec,
			EndSec:            cut.EndSec,
		})
	}
	detail.Sections = historySectionsFromRecipe(recipe)
	signedCuts, err := r.signHistoryCutURLs(ctx, detail.Cuts)
	if err != nil {
		return domain.VideoHistoryDetail{}, err
	}
	detail.Cuts = signedCuts
	return detail, nil
}

// historySectionsFromRecipe converts recipe sections into display-ready entries with
// normalized time ranges, so the short-video form can label each section option.
func historySectionsFromRecipe(recipe domain.VideoRecipe) []domain.VideoHistorySection {
	sections := recipe.MusicRecipe.Sections
	if len(sections) == 0 {
		return nil
	}
	result := make([]domain.VideoHistorySection, 0, len(sections))
	cursor := 0
	for i, sec := range sections {
		start := sec.StartSeconds
		if start == 0 && i > 0 {
			start = cursor
		}
		end := domain.SectionEndSeconds(start, sec.EndSeconds, sec.Duration)
		cursor = end
		result = append(result, domain.VideoHistorySection{
			SectionIndex: i,
			Name:         strings.TrimSpace(sec.Name),
			StartSeconds: start,
			EndSeconds:   end,
		})
	}
	return result
}

// buildHistoryFromRecipe builds (or reuses a cached) VideoHistory for the bulk ListHistoryPage
// path, where re-deriving every listed job's metadata on every page view would be wasteful.
func (r *VideoHistoryRepository) buildHistoryFromRecipe(ctx context.Context, jobID string, recipe domain.VideoRecipe) domain.VideoHistory {
	history, ok := r.getCachedHistory(jobID)
	if !ok {
		history = videoHistoryFromRecipe(jobID, r.metadataURI(jobID), recipe)
		r.setCachedHistory(jobID, history)
	}
	return r.finalizeHistory(ctx, jobID, history)
}

// buildHistoryFromFreshRecipe builds VideoHistory directly from recipe without reading or
// populating the history cache, so single-job reads (GetHistory) always reflect the latest
// storage state rather than a snapshot cached before a regenerate/edit job completed.
func (r *VideoHistoryRepository) buildHistoryFromFreshRecipe(ctx context.Context, jobID string, recipe domain.VideoRecipe) domain.VideoHistory {
	history := videoHistoryFromRecipe(jobID, r.metadataURI(jobID), recipe)
	return r.finalizeHistory(ctx, jobID, history)
}

// finalizeHistory fills in the fields that are never cached (signed URLs expire, so they're
// always regenerated; the keyframe zip URI is cheap to derive).
func (r *VideoHistoryRepository) finalizeHistory(ctx context.Context, jobID string, history domain.VideoHistory) domain.VideoHistory {
	history.SignedURL = ""
	if signedURL, err := r.signedURL(ctx, history.StorageURI); err == nil {
		history.SignedURL = signedURL
	} else {
		slog.WarnContext(ctx, "failed to generate metadata signed URL",
			"uri", history.StorageURI,
			"error", err,
		)
	}
	history.KeyframeZipURI = r.keyframeZipURI(jobID)
	return history
}

// keyframeZipURI returns the GCS URI for the pre-built keyframe zip of a job.
func (r *VideoHistoryRepository) keyframeZipURI(jobID string) string {
	return r.jobObjectURI(jobID, "keyframes.zip")
}

// KeyframeZipSignedURL returns a signed download URL for the pre-built keyframe zip.
// Returns empty string (without error) if the zip does not exist yet.
func (r *VideoHistoryRepository) KeyframeZipSignedURL(ctx context.Context, jobID string) (string, error) {
	if r == nil || r.reader == nil || r.signer == nil {
		return "", nil
	}
	uri := r.keyframeZipURI(jobID)
	exists, err := r.reader.Exists(ctx, uri)
	if err != nil {
		return "", fmt.Errorf("check keyframe zip existence: %w", err)
	}
	if !exists {
		return "", nil
	}
	return r.signedURL(ctx, uri)
}

// loadVideoRecipe returns a cached recipe if present, otherwise fetches and caches one. Used by
// the bulk ListHistoryPage path.
func (r *VideoHistoryRepository) loadVideoRecipe(ctx context.Context, jobID string) (domain.VideoRecipe, error) {
	if recipe, ok := r.getCachedVideoRecipe(jobID); ok {
		return recipe, nil
	}
	recipe, err := r.fetchVideoRecipe(ctx, jobID)
	if err != nil {
		return domain.VideoRecipe{}, err
	}
	r.setCachedVideoRecipe(jobID, recipe)
	return recipe, nil
}

// fetchVideoRecipe always reads the recipe directly from storage, bypassing the cache entirely.
// Used by single-job reads (GetHistory, DownloadKeyframes) that need the current state.
func (r *VideoHistoryRepository) fetchVideoRecipe(ctx context.Context, jobID string) (domain.VideoRecipe, error) {
	rc, err := r.reader.Open(ctx, r.metadataURI(jobID))
	if err != nil {
		return domain.VideoRecipe{}, err
	}
	defer rc.Close()

	raw, err := io.ReadAll(rc)
	if err != nil {
		return domain.VideoRecipe{}, err
	}
	recipe, err := domain.DecodeVideoRecipeJSON(raw)
	if err != nil {
		return domain.VideoRecipe{}, err
	}
	return *recipe, nil
}

func (r *VideoHistoryRepository) signHistoryCutURLs(ctx context.Context, cuts []domain.VideoHistoryCut) ([]domain.VideoHistoryCut, error) {
	signedCuts := append([]domain.VideoHistoryCut(nil), cuts...)
	eg, ctx := errgroup.WithContext(ctx)
	eg.SetLimit(historyFetchConcurrency)
	for i := range signedCuts {
		eg.Go(func() error {
			ref := signedCuts[i].KeyframeReference
			if strings.TrimSpace(ref) == "" {
				return nil
			}
			signedURL, err := r.signedURL(ctx, ref)
			if err != nil {
				slog.WarnContext(ctx, "failed to generate keyframe signed URL",
					"uri", ref,
					"error", err,
				)
				return nil
			}
			signedCuts[i].KeyframeURL = signedURL
			return nil
		})
		eg.Go(func() error {
			videoURI := strings.TrimSpace(signedCuts[i].VideoURL)
			if !strings.HasPrefix(videoURI, "gs://") {
				return nil
			}
			signedURL, err := r.signedURL(ctx, videoURI)
			if err != nil {
				slog.WarnContext(ctx, "failed to generate video signed URL",
					"uri", videoURI,
					"error", err,
				)
				return nil
			}
			signedCuts[i].VideoSignedURL = signedURL
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, err
	}
	return signedCuts, nil
}

func videoHistoryFromRecipe(jobID string, metadataURI string, recipe domain.VideoRecipe) domain.VideoHistory {
	history := domain.VideoHistory{
		JobID:      jobID,
		Title:      strings.TrimSpace(firstNonEmpty(recipe.MusicRecipe.Title, recipe.ProjectTitle)),
		Mood:       strings.TrimSpace(recipe.MusicRecipe.Mood),
		Tempo:      recipe.MusicRecipe.Tempo,
		CreatedAt:  formatHistoryCreatedAt(jobID),
		VisualMode: strings.TrimSpace(recipe.MusicRecipe.ComposeMode),
		CutCount:   len(recipe.Cuts),
		StorageURI: metadataURI,
		Generated:  allCutsGenerated(recipe.Cuts),
	}
	if history.Title == "" {
		history.Title = jobID
	}
	return history
}

func (r *VideoHistoryRepository) resolveJobObjectURI(jobID string, uri string) string {
	if uri == "" || strings.Contains(uri, "://") {
		return uri
	}
	return r.jobObjectURI(jobID, strings.TrimLeft(uri, "/"))
}

func (r *VideoHistoryRepository) metadataURI(jobID string) string {
	return r.jobObjectURI(jobID, videoMetadataFile)
}

// jobObjectURI builds the GCS URI for a named object under a job's output directory.
func (r *VideoHistoryRepository) jobObjectURI(jobID, name string) string {
	return r.baseURI + "/" + jobID + "/" + name
}

func (r *VideoHistoryRepository) signedURL(ctx context.Context, uri string) (string, error) {
	if r.signer == nil || strings.TrimSpace(uri) == "" {
		return "", nil
	}
	return r.signer.GenerateSignedURL(ctx, uri, "GET", 15*time.Minute)
}

func allCutsGenerated(cuts []domain.VideoCut) bool {
	if len(cuts) == 0 {
		return false
	}
	for _, cut := range cuts {
		if !cut.IsGenerated() {
			return false
		}
	}
	return true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
