package repository

import (
	"context"

	"github.com/shouni/go-job-kit/cache"
)

// jobIDListCacheKey はジョブ ID 一覧のキャッシュキーです。
// 一覧対象はリポジトリごとに baseURI 直下の 1 種類だけなので固定キーで足ります。
const jobIDListCacheKey = "job-ids"

// NewJobIDListCache はジョブ ID 一覧用のキャッシュを生成します。
// 保持期間・複製を返す理由・期限切れ要素を回収しない理由は cache.IDList を参照してください。
func NewJobIDListCache() *cache.IDList {
	return cache.NewIDList(cache.DefaultIDListTTL)
}

// listJobIDs は baseURI 配下のジョブ ID を集めます。
// 一覧はバケット全体の走査になるため、短い TTL のキャッシュを挟みます。
func (r *VideoHistoryRepository) listJobIDs(ctx context.Context, collect func(context.Context) ([]string, error)) ([]string, error) {
	return r.jobIDCache.Load(ctx, jobIDListCacheKey, collect)
}

// invalidateJobIDList は一覧キャッシュを破棄し、削除や追加を即座に反映させます。
func (r *VideoHistoryRepository) invalidateJobIDList() {
	r.jobIDCache.Invalidate(jobIDListCacheKey)
}
