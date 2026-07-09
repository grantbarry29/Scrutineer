/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package reporter

import (
	"sync"
	"time"
)

// sessionRateLimiter enforces a per-session token-bucket rate limit: one request
// per interval sustained, up to burst back-to-back (contract §8, #100). It is the
// generic cell rate algorithm — the whole bucket state is a single theoretical
// arrival time (TAT) per session, which preserves the #38 map-eviction property:
// an entry whose TAT has passed decides exactly like a missing one and can be
// swept.
type sessionRateLimiter struct {
	mu       sync.Mutex
	interval time.Duration
	burst    int
	tat      map[string]time.Time
}

func newSessionRateLimiter(interval time.Duration, burst int) *sessionRateLimiter {
	if burst < 1 {
		burst = 1
	}
	return &sessionRateLimiter{
		interval: interval,
		burst:    burst,
		tat:      make(map[string]time.Time),
	}
}

// allow admits the request when the session's TAT is no more than burst-1
// intervals ahead of now, then advances the TAT one interval per admission. A
// fresh (or swept) session starts with TAT=now, so it can spend the full burst
// immediately; a session at the sustained pace keeps its TAT ≈ now. On a denial
// the residual wait is at most one interval, so a constant "Retry-After: 1" (for
// interval = 1s) is always a correct ceiling.
func (l *sessionRateLimiter) allow(sessionKey string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.evictExpiredLocked(now)
	tat, ok := l.tat[sessionKey]
	if !ok || tat.Before(now) {
		tat = now
	}
	if tat.Sub(now) > time.Duration(l.burst-1)*l.interval {
		return false
	}
	l.tat[sessionKey] = tat.Add(l.interval)
	return true
}

// evictExpiredLocked drops sessions whose TAT has passed. Such entries no longer
// affect any rate decision (a fresh entry starts at TAT=now, deciding identically),
// so removing them keeps the map bounded to sessions active within the last
// burst×interval rather than growing without limit (#38). Caller holds l.mu.
func (l *sessionRateLimiter) evictExpiredLocked(now time.Time) {
	for k, t := range l.tat {
		if !t.After(now) {
			delete(l.tat, k)
		}
	}
}
