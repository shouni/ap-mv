// Package repository は、生成履歴（History）の永続化・キャッシュ・一覧整形を行います。
package repository

import (
	"time"

	"github.com/shouni/go-job-kit/cache"

	"github.com/shouni/ap-mv/internal/domain"
)

const defaultHistoryCacheTTL = 10 * time.Minute

// NewHistoryCache creates a cache for generated MV history metadata.
// 期限切れエントリの回収は生成と同時に始まります。停止は Close で行ってください。
func NewHistoryCache() *cache.TTL[domain.VideoHistory] {
	return cache.NewTTL[domain.VideoHistory](defaultHistoryCacheTTL)
}

// NewVideoRecipeCache creates a cache for generated MV recipe metadata.
func NewVideoRecipeCache() *cache.TTL[domain.VideoRecipe] {
	return cache.NewTTL[domain.VideoRecipe](defaultHistoryCacheTTL)
}

// キャッシュキーのジョブ ID 正規化は cache.TTL 側で行われます。
func (r *VideoHistoryRepository) getCachedHistory(jobID string) (domain.VideoHistory, bool) {
	return r.historyCache.Get(jobID)
}

func (r *VideoHistoryRepository) setCachedHistory(jobID string, history domain.VideoHistory) {
	r.historyCache.Set(jobID, history)
}

func (r *VideoHistoryRepository) deleteCachedHistory(jobID string) {
	r.historyCache.Delete(jobID)
}

func (r *VideoHistoryRepository) getCachedVideoRecipe(jobID string) (domain.VideoRecipe, bool) {
	return r.recipeCache.Get(jobID)
}

func (r *VideoHistoryRepository) setCachedVideoRecipe(jobID string, recipe domain.VideoRecipe) {
	r.recipeCache.Set(jobID, recipe)
}

func (r *VideoHistoryRepository) deleteCachedVideoRecipe(jobID string) {
	r.recipeCache.Delete(jobID)
}

// InvalidateJob clears cached history/recipe metadata for jobID.
func (r *VideoHistoryRepository) InvalidateJob(jobID string) {
	r.deleteCachedHistory(jobID)
	r.deleteCachedVideoRecipe(jobID)
}

// Close は履歴・レシピキャッシュの回収を停止します。
// 生成時に回収が始まるため、リポジトリを畳むときに必ず呼んでください。
func (r *VideoHistoryRepository) Close() error {
	r.historyCache.Close()
	r.recipeCache.Close()
	return nil
}
