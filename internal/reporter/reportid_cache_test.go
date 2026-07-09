/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package reporter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
)

// #106: a reportId moves reserved → committed (or is released on failure). Duplicates
// are only acked once the original committed; before that they are told to retry.
func TestReportIDCache_lifecycle(t *testing.T) {
	cache := newReportIDCache(time.Minute)
	key := reportIDCacheKey(types.NamespacedName{Namespace: "ns", Name: "s"}, "rid-1")
	now := time.Unix(100, 0)

	if got := cache.reserve(key, now); got != reportIDNew {
		t.Fatalf("first reserve = %v, want reportIDNew", got)
	}
	if got := cache.reserve(key, now); got != reportIDInFlight {
		t.Fatalf("concurrent reserve = %v, want reportIDInFlight (must not be acked)", got)
	}
	cache.commit(key)
	if got := cache.reserve(key, now.Add(30*time.Second)); got != reportIDCommitted {
		t.Fatalf("post-commit reserve = %v, want reportIDCommitted", got)
	}
	if got := cache.reserve(key, now.Add(2*time.Minute)); got != reportIDNew {
		t.Fatalf("post-TTL reserve = %v, want reportIDNew", got)
	}
}

// A failed original releases the reservation: the reportId stays retriable and the
// retry owns processing again.
func TestReportIDCache_releaseMakesRetriable(t *testing.T) {
	cache := newReportIDCache(time.Minute)
	key := reportIDCacheKey(types.NamespacedName{Namespace: "ns", Name: "s"}, "rid-1")
	now := time.Unix(100, 0)

	if got := cache.reserve(key, now); got != reportIDNew {
		t.Fatalf("reserve = %v, want reportIDNew", got)
	}
	cache.release(key)
	if got := cache.reserve(key, now); got != reportIDNew {
		t.Fatalf("reserve after release = %v, want reportIDNew (failed original retriable)", got)
	}
}

// #106: eviction is amortized via the FIFO expiry queue — after everything expires,
// one operation leaves both the map and the queue drained (no 24h backlog is ever
// rescanned per request).
func TestReportIDCache_evictionDrainsMapAndQueue(t *testing.T) {
	cache := newReportIDCache(time.Minute)
	now := time.Unix(100, 0)
	session := types.NamespacedName{Namespace: "ns", Name: "s"}

	for i := 0; i < 100; i++ {
		key := reportIDCacheKey(session, fmt.Sprintf("rid-%d", i))
		if got := cache.reserve(key, now); got != reportIDNew {
			t.Fatalf("reserve %d = %v", i, got)
		}
		cache.commit(key)
	}
	later := now.Add(2 * time.Minute)
	if got := cache.reserve(reportIDCacheKey(session, "fresh"), later); got != reportIDNew {
		t.Fatalf("fresh reserve = %v", got)
	}
	if len(cache.entries) != 1 {
		t.Fatalf("entries after expiry = %d, want 1 (just the fresh reservation)", len(cache.entries))
	}
	if len(cache.order) != 1 {
		t.Fatalf("expiry queue after expiry = %d, want 1", len(cache.order))
	}
}

// A release + re-reserve leaves a stale record in the expiry queue; when it pops, it
// must not evict the newer reservation early.
func TestReportIDCache_staleQueueRecordDoesNotEvictReReservation(t *testing.T) {
	cache := newReportIDCache(time.Minute)
	key := reportIDCacheKey(types.NamespacedName{Namespace: "ns", Name: "s"}, "rid-1")
	t0 := time.Unix(100, 0)

	if got := cache.reserve(key, t0); got != reportIDNew {
		t.Fatalf("reserve = %v", got)
	}
	cache.release(key)
	if got := cache.reserve(key, t0.Add(30*time.Second)); got != reportIDNew {
		t.Fatalf("re-reserve = %v", got)
	}
	cache.commit(key)
	// t0+61s: the stale record (expiry t0+60s) pops, the live entry (t0+90s) stays.
	if got := cache.reserve(key, t0.Add(61*time.Second)); got != reportIDCommitted {
		t.Fatalf("reserve at t0+61s = %v, want reportIDCommitted (stale queue record must not evict)", got)
	}
	if got := cache.reserve(key, t0.Add(95*time.Second)); got != reportIDNew {
		t.Fatalf("reserve at t0+95s = %v, want reportIDNew (real expiry honored)", got)
	}
}

func TestReportIDCache_nilAndEmptyKeySafe(t *testing.T) {
	var cache *reportIDCache
	key := reportIDCacheKey(types.NamespacedName{Namespace: "ns", Name: "s"}, "x")
	if got := cache.reserve(key, time.Now()); got != reportIDNew {
		t.Fatalf("nil cache reserve = %v, want reportIDNew", got)
	}
	cache.commit(key)
	cache.release(key)

	real := newReportIDCache(time.Minute)
	if got := real.reserve("", time.Now()); got != reportIDNew {
		t.Fatalf("empty key reserve = %v, want reportIDNew", got)
	}
	real.commit("")
	real.release("")
}

// A duplicate arriving while the original is still in flight must NOT be acked 202 —
// the original could still fail and release, leaving an acked-but-lost report (#106).
// It gets a transient 503 + Retry-After instead.
func TestHandler_inFlightDuplicateGetsRetryLater(t *testing.T) {
	ts := metav1.NewTime(time.Unix(500, 0))
	session := &scrutineerv1alpha1.AgentSession{ObjectMeta: metav1.ObjectMeta{Name: "s", Namespace: "ns"}}
	cl := newFakeClient(session)
	cache := newReportIDCache(time.Minute)
	h := &Handler{
		Writer:    cl.Status(),
		Reader:    cl,
		Verifier:  stubVerifier{identity: CallerIdentity{Namespace: "ns", PodName: "p"}},
		ReportIDs: cache,
		Now:       func() time.Time { return ts.Time },
	}
	// Simulate the original still being processed.
	key := reportIDCacheKey(types.NamespacedName{Namespace: "ns", Name: "s"}, "rid-1")
	if got := cache.reserve(key, ts.Time); got != reportIDNew {
		t.Fatalf("setup reserve = %v", got)
	}

	rec := postReport(t, h, ReportRequest{
		Session:  SessionRef{Namespace: "ns", Name: "s"},
		Backend:  "egress-proxy",
		ReportID: "rid-1",
		Decisions: []scrutineerv1alpha1.PolicyDecision{{
			Time: ts, Phase: scrutineerv1alpha1.PolicyDecisionPhaseRuntime, Type: "network",
			Action: scrutineerv1alpha1.PolicyDecisionDeny, Reason: "DeniedDomain", Target: "a",
		}},
	}, "Bearer ok")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (never a false 202)", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("missing Retry-After on in-flight duplicate")
	}
}

// #106 acceptance: N concurrent clients retrying the same reportId, with the first
// merge attempt failing — the report is merged exactly once, every client eventually
// gets 202, and nobody holds a 202 for an unmerged report.
func TestHandler_concurrentDuplicateReportIDsMergeExactlyOnce(t *testing.T) {
	ts := metav1.NewTime(time.Unix(500, 0))
	session := &scrutineerv1alpha1.AgentSession{ObjectMeta: metav1.ObjectMeta{Name: "s", Namespace: "ns"}}

	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = scrutineerv1alpha1.AddToScheme(scheme)
	var updates atomic.Int64
	var failed atomic.Bool
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&scrutineerv1alpha1.AgentSession{}).
		WithObjects(session).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourceUpdate: func(ctx context.Context, c client.Client, subResource string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
				updates.Add(1)
				// The first status merge fails after the reportId was reserved —
				// the acked-but-lost window this issue is about.
				if failed.CompareAndSwap(false, true) {
					return fmt.Errorf("injected wire failure")
				}
				return c.SubResource(subResource).Update(ctx, obj, opts...)
			},
		}).
		Build()

	h := &Handler{
		Writer:    cl.Status(),
		Reader:    cl,
		Verifier:  stubVerifier{identity: CallerIdentity{Namespace: "ns", PodName: "p"}},
		ReportIDs: newReportIDCache(time.Minute),
		Now:       func() time.Time { return ts.Time },
	}

	body, err := json.Marshal(ReportRequest{
		Session:  SessionRef{Namespace: "ns", Name: "s"},
		Backend:  "egress-proxy",
		ReportID: "rid-1",
		Decisions: []scrutineerv1alpha1.PolicyDecision{{
			Time: ts, Phase: scrutineerv1alpha1.PolicyDecisionPhaseRuntime, Type: "network",
			Action: scrutineerv1alpha1.PolicyDecisionDeny, Reason: "DeniedDomain", Target: "a",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	const clients = 8
	var wg sync.WaitGroup
	for i := 0; i < clients; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for attempt := 0; attempt < 100; attempt++ {
				req := httptest.NewRequest(http.MethodPost, reportPath, bytes.NewReader(body))
				req.Header.Set("Authorization", "Bearer ok")
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
				switch rec.Code {
				case http.StatusAccepted:
					return
				case http.StatusServiceUnavailable, http.StatusInternalServerError, http.StatusConflict:
					time.Sleep(time.Millisecond) // transient per contract §4.4: retry
				default:
					t.Errorf("unexpected status %d: %s", rec.Code, rec.Body.String())
					return
				}
			}
			t.Error("client never reached 202")
		}()
	}
	wg.Wait()

	// Exactly-once: one failed attempt plus one successful merge; every later
	// duplicate was answered from the committed reservation without touching status.
	if got := updates.Load(); got != 2 {
		t.Fatalf("status updates = %d, want 2 (1 injected failure + 1 real merge)", got)
	}
}
