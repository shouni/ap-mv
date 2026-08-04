package repository

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"strings"

	"github.com/shouni/go-job-kit/cache"
	"github.com/shouni/go-job-kit/paging"
	"github.com/shouni/go-remote-io/remoteio"

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
	baseURI string
	// draftBaseURI は下書き（video_recipe_draft.json）を走査する起点です。baseURI とは
	// 別プレフィックスで、履歴の走査・削除とは対象範囲が重なりません。
	draftBaseURI string
	reader       remoteio.InputReader
	writer       remoteio.OutputWriter
	signer       remoteio.URLSigner
	historyCache *cache.TTL[domain.VideoHistory]
	recipeCache  *cache.TTL[domain.VideoRecipe]
	// jobIDCache は一覧走査で得たジョブ ID を短時間キャッシュします。
	// メタデータ本体のキャッシュと違い、これが無いと履歴画面を開くたびに
	// baseURI 配下全体の List が走ります。
	jobIDCache *cache.IDList
}

// VideoHistoryRepositoryConfig は VideoHistoryRepository の依存関係です。
//
// reader / writer / signer は用途が近く、位置引数だと取り違えても型が同じ方向へ通って
// しまう箇所があるため、名前で受けます。
type VideoHistoryRepositoryConfig struct {
	// BaseURI は履歴を走査する起点です（末尾のスラッシュは正規化します）。
	BaseURI string
	// DraftBaseURI は下書きを走査する起点です（末尾のスラッシュは正規化します）。
	// 空の場合、下書き機能は無効になります（一覧は空を返します）。
	DraftBaseURI string
	Reader       remoteio.InputReader
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
		draftBaseURI: strings.TrimRight(strings.TrimSpace(cfg.DraftBaseURI), "/"),
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
	jobIDs, err := r.listJobIDs(ctx, jobIDListCacheKey, r.collectJobIDs)
	if err != nil {
		return domain.VideoHistoryPage{}, err
	}

	// 読み込めなかったジョブも一覧には残したいので、代替値は load の中で返します
	// （LoadPage は load がエラーを返した ID を一覧から落とします）。
	load := func(ctx context.Context, id string) (domain.VideoHistory, error) {
		history, err := r.buildHistory(ctx, id)
		if err != nil {
			slog.WarnContext(ctx, "failed to load history metadata",
				"job_id", id,
				"error", err,
			)
			return domain.VideoHistory{
				JobID:      id,
				Title:      id,
				CreatedAt:  formatHistoryCreatedAt(id),
				StorageURI: r.metadataURI(id),
			}, nil
		}
		return history, nil
	}

	// ジョブ ID は "{用途}-{生成時刻}-{乱数}" 形式で、用途プレフィックスが 7 種あります。
	// ID の文字列比較ではプレフィックス順になってしまうため、埋め込まれた時刻を
	// ソートキーとして渡します。時刻を持たない ID は末尾に回ります。
	histories, meta, err := paging.LoadPage(ctx, jobIDs, page, perPage, load,
		paging.WithSortKey(jobid.SortKey),
		paging.WithConcurrency(historyFetchConcurrency),
	)
	if err != nil {
		return domain.VideoHistoryPage{}, err
	}

	return domain.VideoHistoryPage{
		Items:    histories,
		PageMeta: meta,
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
