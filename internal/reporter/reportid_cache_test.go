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

	"k8s.io/apimachinery/pkg/types"
)

func TestReportIDCache_seenAndExpiry(t *testing.T) {
	cache := newReportIDCache(time.Minute)
	key := reportIDCacheKey(types.NamespacedName{Namespace: "ns", Name: "s"}, "rid-1")
	now := time.Unix(100, 0)

	if cache.contains(key, now) {
		t.Fatal("expected miss before mark")
	}
	cache.mark(key, now)
	if !cache.contains(key, now.Add(30*time.Second)) {
		t.Fatal("expected hit within TTL")
	}
	if cache.contains(key, now.Add(2*time.Minute)) {
		t.Fatal("expected miss after TTL")
	}
}

func TestReportIDCache_nilSafe(t *testing.T) {
	var cache *reportIDCache
	key := reportIDCacheKey(types.NamespacedName{Namespace: "ns", Name: "s"}, "x")
	if cache.contains(key, time.Now()) {
		t.Fatal("nil cache should miss")
	}
	cache.mark(key, time.Now())
}
