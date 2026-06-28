/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package reporter

import (
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/types"
)

// DefaultReportIDCacheTTL is how long processed reportIds are remembered per session.
// In-process only; controller restart clears the cache (clients may safely retry).
const DefaultReportIDCacheTTL = 24 * time.Hour

// reportIDCache remembers recently accepted reportIds per session for cheap idempotency.
type reportIDCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]time.Time // cache key -> expiry
}

func newReportIDCache(ttl time.Duration) *reportIDCache {
	if ttl <= 0 {
		ttl = DefaultReportIDCacheTTL
	}
	return &reportIDCache{
		ttl:     ttl,
		entries: make(map[string]time.Time),
	}
}

func reportIDCacheKey(session types.NamespacedName, reportID string) string {
	return session.String() + "\x00" + reportID
}

func normalizeReportID(raw string) string {
	return strings.TrimSpace(raw)
}

// contains reports whether reportId was already accepted for session within TTL.
func (c *reportIDCache) contains(key string, now time.Time) bool {
	if c == nil || key == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.evictExpiredLocked(now)
	exp, ok := c.entries[key]
	return ok && now.Before(exp)
}

// mark records a successfully processed reportId until now+TTL.
func (c *reportIDCache) mark(key string, now time.Time) {
	if c == nil || key == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.evictExpiredLocked(now)
	c.entries[key] = now.Add(c.ttl)
}

// reserve atomically reports whether key is newly recorded: it records key until
// now+TTL and returns true when key is absent/expired, or returns false when key
// is already present within TTL. The check and set happen under a single lock
// acquisition, closing the TOCTOU window where two concurrent identical reportIds
// could both pass contains() before either called mark().
func (c *reportIDCache) reserve(key string, now time.Time) bool {
	if c == nil || key == "" {
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.evictExpiredLocked(now)
	if exp, ok := c.entries[key]; ok && now.Before(exp) {
		return false
	}
	c.entries[key] = now.Add(c.ttl)
	return true
}

// release removes a key recorded by reserve, used to roll back a reservation when
// processing fails so the report can be retried.
func (c *reportIDCache) release(key string) {
	if c == nil || key == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
}

func (c *reportIDCache) evictExpiredLocked(now time.Time) {
	for k, exp := range c.entries {
		if !now.Before(exp) {
			delete(c.entries, k)
		}
	}
}
