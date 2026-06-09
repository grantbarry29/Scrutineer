/*
Copyright 2026 The Relay Authors.

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

// sessionRateLimiter enforces a simple per-session request interval.
type sessionRateLimiter struct {
	mu       sync.Mutex
	interval time.Duration
	last     map[string]time.Time
}

func newSessionRateLimiter(interval time.Duration) *sessionRateLimiter {
	return &sessionRateLimiter{
		interval: interval,
		last:     make(map[string]time.Time),
	}
}

func (l *sessionRateLimiter) allow(sessionKey string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	prev, ok := l.last[sessionKey]
	if ok && now.Sub(prev) < l.interval {
		return false
	}
	l.last[sessionKey] = now
	return true
}
