package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/shouni/go-utils/jobid"
	"golang.org/x/sync/errgroup"

	"github.com/shouni/ap-mv/internal/domain"
)

// このファイルは履歴のストレージ読み込み・キャッシュ利用・署名URL生成を集めています。
// recipe→表示モデルの純粋な変換は history_mapping.go、公開エントリポイントは
// history_listing.go を参照してください。

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
	history.FinalVideoSignedURL = ""
	if signedURL, err := r.signedURL(ctx, history.FinalVideoURL); err == nil {
		history.FinalVideoSignedURL = signedURL
	} else {
		slog.WarnContext(ctx, "failed to generate final video signed URL",
			"uri", history.FinalVideoURL,
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
	// jobID はそのままオブジェクトパスへ埋め込まれるため、他の公開メソッドと同様に検証する。
	// 呼び出し元のハンドラーでも検証しているが、リポジトリ単体で成立させておく。
	if err := jobid.Validate(jobID); err != nil {
		return "", err
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

// GetVeoUsage loads the job's recorded Veo generation tally (written per cut by the video
// generation filter). A missing file is not an error: jobs that predate the record, and jobs
// that stopped after keyframes, simply have none — callers then show only the recipe-derived
// estimate. The record is small and read once per detail view, so it is not cached; it also
// changes while a job is still generating, which is exactly when a stale copy would mislead.
func (r *VideoHistoryRepository) GetVeoUsage(ctx context.Context, jobID string) (*domain.VeoUsage, error) {
	if r == nil || r.reader == nil || r.baseURI == "" {
		return nil, nil
	}
	if err := jobid.Validate(jobID); err != nil {
		return nil, err
	}
	rc, err := r.reader.Open(ctx, r.jobObjectURI(jobID, domain.VeoUsageFileName))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = rc.Close() }()

	raw, err := io.ReadAll(rc)
	if err != nil {
		return nil, err
	}
	// 空オブジェクトは「記録なし」と同じ扱いにする。書き込み途中の中断などで 0 バイトの
	// オブジェクトが残っても、履歴画面をエラーにする理由にはならない。
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	var usage domain.VeoUsage
	if err := json.Unmarshal(raw, &usage); err != nil {
		return nil, fmt.Errorf("decode %s for job %s: %w", domain.VeoUsageFileName, jobID, err)
	}
	return &usage, nil
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
