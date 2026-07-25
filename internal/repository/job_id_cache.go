package repository

import (
	"context"
	"slices"
	"time"

	"github.com/jellydator/ttlcache/v3"
)

// jobIDListCacheTTL はジョブ ID 一覧のキャッシュ期間です。
// 履歴メタデータ本体（10 分）より大幅に短いのは、新しく完成したジョブが一覧に現れるまでの
// 遅延を最小にするためです。削除時はこのキャッシュを明示的に破棄するので、
// 削除操作が TTL 分だけ画面に反映されない、という事態は起きません。
const jobIDListCacheTTL = time.Minute

// jobIDListCacheKey はジョブ ID 一覧のキャッシュキーです。
// 一覧対象はリポジトリごとに baseURI 直下の 1 種類だけなので固定キーで足ります。
const jobIDListCacheKey = "job-ids"

// NewJobIDListCache はジョブ ID 一覧用のキャッシュを生成します。
// 保持されるのは 1 エントリだけで、同じキーへの Set で上書きされていくため、
// 期限切れ要素を回収する Start は不要です。
func NewJobIDListCache() *ttlcache.Cache[string, []string] {
	return ttlcache.New[string, []string](
		ttlcache.WithTTL[string, []string](jobIDListCacheTTL),
		ttlcache.WithDisableTouchOnHit[string, []string](),
	)
}

// listJobIDs は baseURI 配下のジョブ ID を集めます。
// 一覧はバケット全体の走査になるため、短い TTL のキャッシュを挟みます。
func (r *VideoHistoryRepository) listJobIDs(ctx context.Context, collect func(context.Context) ([]string, error)) ([]string, error) {
	if r.jobIDCache != nil {
		if cached := r.jobIDCache.Get(jobIDListCacheKey); cached != nil {
			// 呼び出し側（selectHistoryPageIDs）が受け取ったスライスをその場でソートするため、
			// キャッシュ本体を渡すと並行リクエスト同士で競合します。必ず複製を返します。
			return slices.Clone(cached.Value()), nil
		}
	}

	jobIDs, err := collect(ctx)
	if err != nil {
		return nil, err
	}

	if r.jobIDCache != nil {
		r.jobIDCache.Set(jobIDListCacheKey, jobIDs, ttlcache.DefaultTTL)
	}

	return slices.Clone(jobIDs), nil
}

// invalidateJobIDList は一覧キャッシュを破棄し、削除や追加を即座に反映させます。
func (r *VideoHistoryRepository) invalidateJobIDList() {
	if r.jobIDCache != nil {
		r.jobIDCache.Delete(jobIDListCacheKey)
	}
}
