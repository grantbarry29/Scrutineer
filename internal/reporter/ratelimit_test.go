/*
Copyright 2026 The Scrutineer Authors.

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

// Contract §8: the per-session limit is one request per interval sustained, with a
// burst allowance so a backlogged client can send several batches back-to-back
// before falling to the sustained pace (#100).
func TestSessionRateLimiter_allowsBurstThenSustainedRate(t *testing.T) {
	l := newSessionRateLimiter(time.Second, 5)
	base := time.Unix(0, 0)

	for i := 0; i < 5; i++ {
		if !l.allow("a", base) {
			t.Fatalf("burst request %d should be allowed", i+1)
		}
	}
	if l.allow("a", base) {
		t.Fatal("request beyond the burst should be denied")
	}

	// Sustained pace: one slot refills per interval.
	if !l.allow("a", base.Add(time.Second)) {
		t.Fatal("request one interval later should be allowed")
	}
	if l.allow("a", base.Add(time.Second)) {
		t.Fatal("second request in the same interval should be denied once the burst is spent")
	}

	// A long idle period restores the full burst.
	later := base.Add(time.Minute)
	for i := 0; i < 5; i++ {
		if !l.allow("a", later) {
			t.Fatalf("post-idle burst request %d should be allowed", i+1)
		}
	}
}

// The burst is per (namespace, session): one session spending its burst must not
// throttle another.
func TestSessionRateLimiter_burstIsPerSession(t *testing.T) {
	l := newSessionRateLimiter(time.Second, 2)
	base := time.Unix(0, 0)

	if !l.allow("a", base) || !l.allow("a", base) {
		t.Fatal("session a's burst should be allowed")
	}
	if l.allow("a", base) {
		t.Fatal("session a should be throttled past its burst")
	}
	if !l.allow("b", base) || !l.allow("b", base) {
		t.Fatal("session b must have its own burst")
	}
}

// Regression for #38: the per-session map must not grow without bound. An entry
// whose theoretical arrival time has passed decides exactly like a missing one,
// so it is swept on each allow() — the map stays bounded to sessions active
// within the last burst×interval.
func TestSessionRateLimiter_evictsStaleEntries(t *testing.T) {
	l := newSessionRateLimiter(time.Second, 5)
	base := time.Unix(0, 0)

	for i := 0; i < 5; i++ {
		if !l.allow("a", base) {
			t.Fatal("burst for session a should be allowed")
		}
	}
	if !l.allow("b", base) {
		t.Fatal("first request for session b should be allowed")
	}
	if len(l.tat) != 2 {
		t.Fatalf("tracked sessions = %d, want 2", len(l.tat))
	}

	// burst×interval later even the fully-spent session a is stale; a request for
	// a new session sweeps both before recording c.
	if !l.allow("c", base.Add(5*time.Second)) {
		t.Fatal("request past the refill horizon should be allowed")
	}
	if len(l.tat) != 1 {
		t.Fatalf("tracked sessions after eviction = %d, want 1: %v", len(l.tat), l.tat)
	}
	if _, ok := l.tat["a"]; ok {
		t.Fatal("stale key 'a' should have been evicted")
	}
	if _, ok := l.tat["c"]; !ok {
		t.Fatal("active key 'c' should be present")
	}
}

// Eviction must not weaken the rate limit: a session that keeps reporting within
// the interval is still throttled (its entry is retained, not swept).
func TestSessionRateLimiter_stillThrottlesActiveSession(t *testing.T) {
	l := newSessionRateLimiter(time.Second, 1)
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
