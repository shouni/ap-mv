package repository

import (
	"context"

	"github.com/shouni/go-job-kit/cache"
)

// 一覧対象は baseURI 配下のジョブだけです。台本のみのジョブも同じ場所に保存されるため、
// 下書き用に分けていたキャッシュキーは不要になりました。
const jobIDListCacheKey = "job-ids"

// NewJobIDListCache はジョブ ID 一覧用のキャッシュを生成します。
// 保持期間・複製を返す理由・期限切れ要素を回収しない理由は cache.IDList を参照してください。
func NewJobIDListCache() *cache.IDList {
	return cache.NewIDList(cache.DefaultIDListTTL)
}

// listJobIDs は key に対応するプレフィックス配下のジョブ ID を集めます。
// 一覧はバケット全体の走査になるため、短い TTL のキャッシュを挟みます。
func (r *VideoHistoryRepository) listJobIDs(ctx context.Context, key string, collect func(context.Context) ([]string, error)) ([]string, error) {
	return r.jobIDCache.Load(ctx, key, collect)
}

// invalidateJobIDList は一覧キャッシュを破棄し、削除や追加を即座に反映させます。
func (r *VideoHistoryRepository) invalidateJobIDList(key string) {
	r.jobIDCache.Invalidate(key)
}
