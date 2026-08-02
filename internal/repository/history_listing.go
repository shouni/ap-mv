package repository

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/jellydator/ttlcache/v3"
	"github.com/shouni/go-job-kit/cache"
	"github.com/shouni/go-job-kit/paging"
	"github.com/shouni/go-remote-io/remoteio"
	"golang.org/x/sync/errgroup"

	"github.com/shouni/go-utils/jobid"

	"github.com/shouni/ap-mv/internal/domain"
)

// このファイルは履歴の公開エントリポイント（一覧・詳細）を集めています。
// ストレージ読み込み・キャッシュ・署名URLは history_storage.go、
// recipe→表示モデルの純粋な変換は history_mapping.go を参照してください。

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
	historyCache *cache.TTL[domain.VideoHistory]
	recipeCache  *cache.TTL[domain.VideoRecipe]
	// jobIDCache は一覧走査で得たジョブ ID を短時間キャッシュします。
	// メタデータ本体のキャッシュと違い、これが無いと履歴画面を開くたびに
	// baseURI 配下全体の List が走ります。
	jobIDCache *ttlcache.Cache[string, []string]
}

// VideoHistoryRepositoryConfig は VideoHistoryRepository の依存関係です。
//
// reader / writer / signer は用途が近く、位置引数だと取り違えても型が同じ方向へ通って
// しまう箇所があるため、名前で受けます。
type VideoHistoryRepositoryConfig struct {
	// BaseURI は履歴を走査する起点です（末尾のスラッシュは正規化します）。
	BaseURI string
	Reader  remoteio.InputReader
	// Writer は履歴の削除・更新に使います。読み取り専用の用途では nil を許容します。
	Writer remoteio.OutputWriter
	// Signer は再生用の署名付き URL を発行します。発行しない用途では nil を許容します。
	Signer remoteio.URLSigner
	// HistoryCache は履歴メタデータのキャッシュです。nil なら既定の TTL キャッシュを作ります。
	HistoryCache *cache.TTL[domain.VideoHistory]
}

// NewVideoHistoryRepository creates a generated MV history repository.
func NewVideoHistoryRepository(cfg VideoHistoryRepositoryConfig) *VideoHistoryRepository {
	historyCache := cfg.HistoryCache
	if historyCache == nil {
		historyCache = NewHistoryCache()
	}
	return &VideoHistoryRepository{
		baseURI:      strings.TrimRight(strings.TrimSpace(cfg.BaseURI), "/"),
		reader:       cfg.Reader,
		writer:       cfg.Writer,
		signer:       cfg.Signer,
		historyCache: historyCache,
		recipeCache:  NewVideoRecipeCache(),
		jobIDCache:   NewJobIDListCache(),
	}
}

// collectJobIDs は baseURI 直下を走査して MV ジョブの ID を集めます。
// バケット全体の List になるため、呼び出しは listJobIDs のキャッシュ越しに行います。
func (r *VideoHistoryRepository) collectJobIDs(ctx context.Context) ([]string, error) {
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
		if err := jobid.Validate(jobID); err != nil {
			return nil
		}
		seen[jobID] = true
		jobIDs = append(jobIDs, jobID)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list history objects: %w", err)
	}
	return jobIDs, nil
}

// ListHistoryPage lists generated MV jobs with paging.
func (r *VideoHistoryRepository) ListHistoryPage(ctx context.Context, page int, perPage int) (domain.VideoHistoryPage, error) {
	if r == nil || r.reader == nil || r.baseURI == "" {
		return domain.VideoHistoryPage{}, nil
	}
	jobIDs, err := r.listJobIDs(ctx, r.collectJobIDs)
	if err != nil {
		return domain.VideoHistoryPage{}, err
	}

	// ジョブ ID は "{用途}-{生成時刻}-{乱数}" 形式で、用途プレフィックスが 7 種あります。
	// ID の文字列比較ではプレフィックス順になってしまうため、埋め込まれた時刻を
	// ソートキーとして渡します。時刻を持たない ID は末尾に回ります。
	selectedIDs, meta := paging.SelectIDs(jobIDs, page, perPage, paging.WithSortKey(jobid.SortKey))
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
		ti, tj := jobid.SortKey(histories[i].JobID), jobid.SortKey(histories[j].JobID)
		if ti != tj {
			return ti > tj
		}
		return histories[i].JobID > histories[j].JobID
	})

	return domain.VideoHistoryPage{
		Items:    histories,
		PageMeta: paging.AdjustItemCount(meta, len(histories)),
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
	if err := jobid.Validate(jobID); err != nil {
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
