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

// historyFetchConcurrency caps parallel signed-URL requests when signing a job's cuts in bulk.
// 署名は Cloud Run に秘密鍵が無いためネットワーク呼び出し（IAM SignBlob）です。
const historyFetchConcurrency = 10

// buildHistoryFromFreshRecipe builds VideoHistory directly from the stored recipe, so
// single-job reads (GetHistory) always reflect the latest storage state.
func (r *VideoHistoryRepository) buildHistoryFromFreshRecipe(ctx context.Context, jobID string, recipe domain.VideoRecipe) domain.VideoHistory {
	return r.finalizeHistory(ctx, jobID, r.videoHistoryFromRecipe(jobID, recipe))
}

// finalizeHistory fills in the fields that are never cached.
//
// 署名付き URL はここでは作りません。画面は同一オリジンのパスを辿り、ハンドラーが
// リダイレクトの時点で 1 本だけ署名します。JSON の呼び出し元だけが SignHistoryURLs を
// 明示的に呼びます。
func (r *VideoHistoryRepository) finalizeHistory(_ context.Context, jobID string, history domain.VideoHistory) domain.VideoHistory {
	history.SignedURL = ""
	history.FinalVideoSignedURL = ""
	history.KeyframeZipURI = r.keyframeZipURI(jobID)
	return history
}

// SignHistoryURLs は、GetHistory が空のままにした署名付き URL を埋めます。
// JSON 応答のためだけの経路で、画面は呼びません（リダイレクトで 1 本ずつ署名するため）。
func (r *VideoHistoryRepository) SignHistoryURLs(ctx context.Context, detail *domain.VideoHistoryDetail) error {
	if detail == nil {
		return nil
	}
	if signedURL, err := r.signedURL(ctx, detail.StorageURI); err == nil {
		detail.SignedURL = signedURL
	} else {
		slog.WarnContext(ctx, "failed to generate metadata signed URL", "uri", detail.StorageURI, "error", err)
	}
	if signedURL, err := r.signedURL(ctx, detail.FinalVideoURL); err == nil {
		detail.FinalVideoSignedURL = signedURL
	} else {
		slog.WarnContext(ctx, "failed to generate final video signed URL", "uri", detail.FinalVideoURL, "error", err)
	}

	signedCuts, err := r.signHistoryCutURLs(ctx, detail.Cuts)
	if err != nil {
		return err
	}
	detail.Cuts = signedCuts
	return nil
}

// SignedObjectURL は、ジョブのメタデータに記録された gs:// URI を署名します。
// リダイレクトハンドラーが、読み込み済みの履歴から取り出した URI に対してだけ呼びます
// （呼び出し元の入力をそのまま署名させないため）。
func (r *VideoHistoryRepository) SignedObjectURL(ctx context.Context, uri string) (string, error) {
	return r.signedURL(ctx, uri)
}

// keyframeZipURI returns the GCS URI for the pre-built keyframe zip of a job.
func (r *VideoHistoryRepository) keyframeZipURI(jobID string) string {
	return r.jobObjectURI(jobID, "keyframes.zip")
}

// KeyframeZipSignedURL returns a signed download URL for the pre-built keyframe zip.
// Returns empty string (without error) if the zip does not exist yet.
func (r *VideoHistoryRepository) KeyframeZipSignedURL(ctx context.Context, jobID string) (string, error) {
	if r == nil || r.store == nil {
		return "", nil
	}
	// jobID はそのままオブジェクトパスへ埋め込まれるため、他の公開メソッドと同様に検証する。
	// 呼び出し元のハンドラーでも検証しているが、リポジトリ単体で成立させておく。
	if err := jobid.Validate(jobID); err != nil {
		return "", err
	}
	uri := r.keyframeZipURI(jobID)
	exists, err := r.store.Exists(ctx, uri)
	if err != nil {
		return "", fmt.Errorf("check keyframe zip existence: %w", err)
	}
	if !exists {
		return "", nil
	}
	return r.signedURL(ctx, uri)
}

// fetchVideoRecipe reads the recipe directly from storage.
func (r *VideoHistoryRepository) fetchVideoRecipe(ctx context.Context, jobID string) (domain.VideoRecipe, error) {
	rc, err := r.store.Open(ctx, r.metadataURI(jobID))
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
	if r == nil || r.store == nil || r.baseURI == "" {
		return nil, nil
	}
	if err := jobid.Validate(jobID); err != nil {
		return nil, err
	}
	rc, err := r.store.Open(ctx, r.jobObjectURI(jobID, domain.VeoUsageFileName))
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
	if r.store == nil || strings.TrimSpace(uri) == "" {
		return "", nil
	}
	return r.store.SignURL(ctx, uri, "GET", 15*time.Minute)
}

// listObjectsUnder は prefix 配下のオブジェクト URI を集めます。
func (r *VideoHistoryRepository) listObjectsUnder(ctx context.Context, prefix string) ([]string, error) {
	var paths []string
	for entry, err := range r.store.List(ctx, prefix) {
		if err != nil {
			return nil, err
		}
		paths = append(paths, entry.URI)
	}
	return paths, nil
}
