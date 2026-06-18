package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jellydator/ttlcache/v3"
	"github.com/shouni/go-remote-io/remoteio"
	"golang.org/x/sync/errgroup"

	"ap-mv/internal/domain"
)

const videoMetadataFile = "video_music_meta.json"

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
		jobID := path.Base(path.Dir(gcsPath))
		if jobID == "." || jobID == "/" || jobID == "" || seen[jobID] {
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
	eg.SetLimit(10)

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
	sort.Slice(histories, func(i, j int) bool {
		return histories[i].JobID > histories[j].JobID
	})

	return domain.VideoHistoryPage{
		Items:    histories,
		PageMeta: adjustPageMetaItemCount(meta, len(histories)),
	}, nil
}

func (r *VideoHistoryRepository) buildHistory(ctx context.Context, jobID string) (domain.VideoHistory, error) {
	if history, ok := r.getCachedHistory(jobID); ok {
		return history, nil
	}
	recipe, err := r.loadVideoRecipe(ctx, jobID)
	if err != nil {
		return domain.VideoHistory{}, err
	}
	return r.buildHistoryFromRecipe(ctx, jobID, recipe), nil
}

// GetHistory loads generated MV job metadata and cut keyframe references.
func (r *VideoHistoryRepository) GetHistory(ctx context.Context, jobID string) (domain.VideoHistoryDetail, error) {
	if r == nil || r.reader == nil || r.baseURI == "" {
		return domain.VideoHistoryDetail{}, errors.New("history repository is not properly configured")
	}
	if err := domain.ValidateJobID(jobID); err != nil {
		return domain.VideoHistoryDetail{}, err
	}
	recipe, err := r.loadVideoRecipe(ctx, jobID)
	if err != nil {
		return domain.VideoHistoryDetail{}, err
	}
	history := r.buildHistoryFromRecipe(ctx, jobID, recipe)
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
	signedCuts, err := r.signHistoryCutURLs(ctx, detail.Cuts)
	if err != nil {
		return domain.VideoHistoryDetail{}, err
	}
	detail.Cuts = signedCuts
	return detail, nil
}

func (r *VideoHistoryRepository) buildHistoryFromRecipe(ctx context.Context, jobID string, recipe domain.VideoRecipe) domain.VideoHistory {
	if history, ok := r.getCachedHistory(jobID); ok {
		return history
	}
	history := videoHistoryFromRecipe(jobID, r.metadataURI(jobID), recipe)
	if signedURL, err := r.signedURL(ctx, history.StorageURI); err == nil {
		history.SignedURL = signedURL
	} else {
		slog.WarnContext(ctx, "failed to generate metadata signed URL",
			"uri", history.StorageURI,
			"error", err,
		)
	}
	r.setCachedHistory(jobID, history)
	return history
}

func (r *VideoHistoryRepository) loadVideoRecipe(ctx context.Context, jobID string) (domain.VideoRecipe, error) {
	if recipe, ok := r.getCachedVideoRecipe(jobID); ok {
		return recipe, nil
	}
	rc, err := r.reader.Open(ctx, r.metadataURI(jobID))
	if err != nil {
		return domain.VideoRecipe{}, err
	}
	defer rc.Close()

	var recipe domain.VideoRecipe
	if err := json.NewDecoder(rc).Decode(&recipe); err != nil {
		return domain.VideoRecipe{}, err
	}
	recipe.Normalize()
	r.setCachedVideoRecipe(jobID, recipe)
	return recipe, nil
}

func (r *VideoHistoryRepository) signHistoryCutURLs(ctx context.Context, cuts []domain.VideoHistoryCut) ([]domain.VideoHistoryCut, error) {
	signedCuts := append([]domain.VideoHistoryCut(nil), cuts...)
	eg, ctx := errgroup.WithContext(ctx)
	eg.SetLimit(10)
	for i := range signedCuts {
		i := i
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
	}
	if err := eg.Wait(); err != nil {
		return nil, err
	}
	return signedCuts, nil
}

func videoHistoryFromRecipe(jobID string, metadataURI string, recipe domain.VideoRecipe) domain.VideoHistory {
	history := domain.VideoHistory{
		JobID:      jobID,
		Title:      strings.TrimSpace(firstNonEmpty(recipe.Title, recipe.ProjectTitle)),
		Mood:       strings.TrimSpace(recipe.Mood),
		Tempo:      recipe.Tempo,
		CreatedAt:  formatHistoryCreatedAt(jobID),
		VisualMode: strings.TrimSpace(recipe.ComposeMode),
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
	return r.baseURI + "/" + jobID + "/" + strings.TrimLeft(uri, "/")
}

func (r *VideoHistoryRepository) metadataURI(jobID string) string {
	return r.baseURI + "/" + jobID + "/" + videoMetadataFile
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
