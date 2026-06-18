package repository

import (
	"time"

	"github.com/jellydator/ttlcache/v3"

	"ap-mv/internal/domain"
)

const defaultHistoryCacheTTL = 10 * time.Minute

// NewHistoryCache creates a cache for generated MV history metadata.
func NewHistoryCache() *ttlcache.Cache[string, domain.VideoHistory] {
	return ttlcache.New[string, domain.VideoHistory](
		ttlcache.WithTTL[string, domain.VideoHistory](defaultHistoryCacheTTL),
		ttlcache.WithDisableTouchOnHit[string, domain.VideoHistory](),
	)
}

// NewVideoRecipeCache creates a cache for generated MV recipe metadata.
func NewVideoRecipeCache() *ttlcache.Cache[string, domain.VideoRecipe] {
	return ttlcache.New[string, domain.VideoRecipe](
		ttlcache.WithTTL[string, domain.VideoRecipe](defaultHistoryCacheTTL),
		ttlcache.WithDisableTouchOnHit[string, domain.VideoRecipe](),
	)
}

func (r *VideoHistoryRepository) getCachedHistory(jobID string) (domain.VideoHistory, bool) {
	item := r.historyCache.Get(jobID)
	if item == nil {
		return domain.VideoHistory{}, false
	}
	return item.Value(), true
}

func (r *VideoHistoryRepository) setCachedHistory(jobID string, history domain.VideoHistory) {
	r.historyCache.Set(jobID, history, ttlcache.DefaultTTL)
}

func (r *VideoHistoryRepository) deleteCachedHistory(jobID string) {
	r.historyCache.Delete(jobID)
}

func (r *VideoHistoryRepository) getCachedVideoRecipe(jobID string) (domain.VideoRecipe, bool) {
	item := r.recipeCache.Get(jobID)
	if item == nil {
		return domain.VideoRecipe{}, false
	}
	return item.Value(), true
}

func (r *VideoHistoryRepository) setCachedVideoRecipe(jobID string, recipe domain.VideoRecipe) {
	r.recipeCache.Set(jobID, recipe, ttlcache.DefaultTTL)
}

func (r *VideoHistoryRepository) deleteCachedVideoRecipe(jobID string) {
	r.recipeCache.Delete(jobID)
}
