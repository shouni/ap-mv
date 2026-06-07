package workflow

import (
	"time"

	"github.com/jellydator/ttlcache/v3"
)

type imageCache struct {
	cache *ttlcache.Cache[string, any]
}

func newImageCache() *imageCache {
	c := ttlcache.New[string, any](
		ttlcache.WithTTL[string, any](defaultCacheExpiration),
		ttlcache.WithDisableTouchOnHit[string, any](),
	)
	go c.Start()

	return &imageCache{cache: c}
}

func (c *imageCache) Get(key string) (any, bool) {
	item := c.cache.Get(key)
	if item == nil {
		return nil, false
	}

	return item.Value(), true
}

func (c *imageCache) Set(key string, value any, ttl time.Duration) {
	c.cache.Set(key, value, ttl)
}

func (c *imageCache) Stop() {
	if c != nil && c.cache != nil {
		c.cache.Stop()
	}
}
