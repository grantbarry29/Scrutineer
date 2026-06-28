/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package reporter

import (
	"testing"
	"time"
)

// Regression for #38: the per-session map must not grow without bound. Stale
// entries (older than the interval) are swept on each allow() so the map stays
// bounded to sessions seen within the last interval.
func TestSessionRateLimiter_evictsStaleEntries(t *testing.T) {
	l := newSessionRateLimiter(time.Second)
	base := time.Unix(0, 0)

	if !l.allow("a", base) || !l.allow("b", base) {
		t.Fatal("first request per session should be allowed")
	}
	if len(l.last) != 2 {
		t.Fatalf("tracked sessions = %d, want 2", len(l.last))
	}

	// A request well past the interval for a new session sweeps the now-stale
	// a/b entries before recording c.
	if !l.allow("c", base.Add(5*time.Second)) {
		t.Fatal("request past the interval should be allowed")
	}
	if len(l.last) != 1 {
		t.Fatalf("tracked sessions after eviction = %d, want 1: %v", len(l.last), l.last)
	}
	if _, ok := l.last["a"]; ok {
		t.Fatal("stale key 'a' should have been evicted")
	}
	if _, ok := l.last["c"]; !ok {
		t.Fatal("active key 'c' should be present")
	}
}

// Eviction must not weaken the rate limit: a session that keeps reporting within
// the interval is still throttled (its entry is retained, not swept).
func TestSessionRateLimiter_stillThrottlesActiveSession(t *testing.T) {
	l := newSessionRateLimiter(time.Second)
	base := time.Unix(0, 0)

	if !l.allow("a", base) {
		t.Fatal("first request should be allowed")
	}
	if l.allow("a", base.Add(500*time.Millisecond)) {
		t.Fatal("second request within the interval should be denied")
	}
	if !l.allow("a", base.Add(1500*time.Millisecond)) {
		t.Fatal("request after the interval should be allowed")
	}
}
