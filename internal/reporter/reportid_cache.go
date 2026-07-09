/*
Copyright 2026 The Scrutineer Authors.

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

// reportIDState is the outcome of reserve: the caller either owns processing of a
// fresh reportId, raced a still-in-flight original, or hit an already-merged
// duplicate.
type reportIDState int

const (
	// reportIDNew: the caller now owns processing — it must commit on success or
	// release on failure.
	reportIDNew reportIDState = iota
	// reportIDInFlight: another request holds the reservation and has not committed.
	// The caller must NOT ack 202 — the original can still fail and release, which
	// would leave an acked-but-lost report (#106). Surface a transient retry instead.
	reportIDInFlight
	// reportIDCommitted: the report was merged within the TTL — safe to ack 202
	// without touching status (contract §7 idempotency).
	reportIDCommitted
)

type reportIDEntry struct {
	expiry    time.Time
	committed bool
}

// expiryRecord is one insertion in the eviction FIFO.
type expiryRecord struct {
	key    string
	expiry time.Time
}

// reportIDCache remembers recently accepted reportIds per session for cheap
// idempotency (contract §7). Entries move reserved → committed (or are released on
// failure); duplicates are only ever acked from a committed entry.
type reportIDCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]reportIDEntry
	// order is a FIFO of insertions. The TTL is a per-cache constant, so expiries
	// are monotone (wall-clock skew aside) and expired entries are always at the
	// head: each operation pops only already-expired records, making eviction
	// amortized O(1) per operation regardless of backlog (#106) — a full-map sweep
	// at the 24h TTL would rescan the whole day's reportIds on every request. A
	// record whose key was released and re-reserved no longer matches the live
	// entry's expiry and is skipped when popped.
	order []expiryRecord
}

func newReportIDCache(ttl time.Duration) *reportIDCache {
	if ttl <= 0 {
		ttl = DefaultReportIDCacheTTL
	}
	return &reportIDCache{
		ttl:     ttl,
		entries: make(map[string]reportIDEntry),
	}
}

func reportIDCacheKey(session types.NamespacedName, reportID string) string {
	return session.String() + "\x00" + reportID
}

func normalizeReportID(raw string) string {
	return strings.TrimSpace(raw)
}

// reserve atomically claims a reportId for processing. It returns reportIDNew when
// the caller now owns it (recorded until now+TTL; commit or release it),
// reportIDInFlight when an uncommitted original holds it, and reportIDCommitted when
// the report was already merged within the TTL. A nil cache or empty key always
// grants ownership (idempotency disabled).
func (c *reportIDCache) reserve(key string, now time.Time) reportIDState {
	if c == nil || key == "" {
		return reportIDNew
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.evictExpiredLocked(now)
	if e, ok := c.entries[key]; ok && now.Before(e.expiry) {
		if e.committed {
			return reportIDCommitted
		}
		return reportIDInFlight
	}
	expiry := now.Add(c.ttl)
	c.entries[key] = reportIDEntry{expiry: expiry}
	c.order = append(c.order, expiryRecord{key: key, expiry: expiry})
	return reportIDNew
}

// commit marks a reserved reportId as merged: duplicates get 202 for the rest of the
// reservation's TTL. No-op if the reservation is gone (released or expired).
func (c *reportIDCache) commit(key string) {
	if c == nil || key == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[key]; ok {
		e.committed = true
		c.entries[key] = e
	}
}

// release removes a key recorded by reserve, used to roll back a reservation when
// processing fails so the report can be retried. The FIFO record it leaves behind is
// skipped on pop (expiry mismatch with any later re-reservation).
func (c *reportIDCache) release(key string) {
	if c == nil || key == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
}

func (c *reportIDCache) evictExpiredLocked(now time.Time) {
	for len(c.order) > 0 && !now.Before(c.order[0].expiry) {
		rec := c.order[0]
		// Zero the popped slot so the key string is collectable even while the
		// backing array is still referenced by the shrinking slice header.
		c.order[0] = expiryRecord{}
		c.order = c.order[1:]
		if e, ok := c.entries[rec.key]; ok && e.expiry.Equal(rec.expiry) {
			delete(c.entries, rec.key)
		}
	}
}
