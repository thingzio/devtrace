package service

import (
	"sync"
	"time"

	"github.com/thingzio/devtrace/pkg/config"
	"github.com/thingzio/devtrace/pkg/model"
)

type cacheEntry struct {
	resp      *model.ScoreResponse
	expiresAt time.Time
}

type scoreCache struct {
	mu      sync.RWMutex
	entries map[string]*cacheEntry
	ttl     time.Duration
	stop    chan struct{}
}

func newScoreCache() *scoreCache {
	ttlSec := config.GetEnvAsInt("SCORE_CACHE_TTL_SEC", 300) // default 5 minutes
	c := &scoreCache{
		entries: make(map[string]*cacheEntry),
		ttl:     time.Duration(ttlSec) * time.Second,
		stop:    make(chan struct{}),
	}
	go c.evictLoop()
	return c
}

// cacheKey combines username and repo into a lookup key.
func cacheKey(username, repo string) string {
	if repo != "" {
		return username + ":" + repo
	}
	return username
}

// get returns a cached response if present and not expired.
func (c *scoreCache) get(username, repo string) *model.ScoreResponse {
	c.mu.RLock()
	defer c.mu.RUnlock()

	e, ok := c.entries[cacheKey(username, repo)]
	if !ok || time.Now().After(e.expiresAt) {
		return nil
	}
	return e.resp
}

// set stores a response in the cache.
func (c *scoreCache) set(username, repo string, resp *model.ScoreResponse) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[cacheKey(username, repo)] = &cacheEntry{
		resp:      resp,
		expiresAt: time.Now().Add(c.ttl),
	}
}

// evictLoop periodically removes expired entries.
func (c *scoreCache) evictLoop() {
	ticker := time.NewTicker(c.ttl)
	defer ticker.Stop()

	for {
		select {
		case <-c.stop:
			return
		case <-ticker.C:
			c.mu.Lock()
			now := time.Now()
			for k, e := range c.entries {
				if now.After(e.expiresAt) {
					delete(c.entries, k)
				}
			}
			c.mu.Unlock()
		}
	}
}

// Close stops the eviction loop.
func (c *scoreCache) Close() {
	close(c.stop)
}
