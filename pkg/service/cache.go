// Copyright 2026 Thingz LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

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
	// Default raised from 30 → 60 minutes to halve the cache-miss rate
	// for CI integrations that poll the same usernames every few
	// minutes. Background scorer is unaffected — it does not go through
	// this cache. Override via SCORE_CACHE_TTL_SEC.
	ttlSec := config.GetEnvAsInt("SCORE_CACHE_TTL_SEC", 3600)
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
