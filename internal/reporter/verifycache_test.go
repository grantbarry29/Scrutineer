/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package reporter

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// countingVerifier stands in for KubeIdentityVerifier and counts apiserver-costing
// verification attempts.
type countingVerifier struct {
	calls    int
	identity CallerIdentity
	err      error
}

func (v *countingVerifier) Verify(context.Context, *http.Request, SessionRef) (CallerIdentity, error) {
	v.calls++
	if v.err != nil {
		return CallerIdentity{}, v.err
	}
	return v.identity, nil
}

func verifyRequest(token, pod string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/v1/report", nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if pod != "" {
		r.Header.Set(headerScrutineerPod, pod)
	}
	return r
}

func newTestCachingVerifier(inner IdentityVerifier, now *time.Time) *cachingVerifier {
	c := newCachingVerifier(inner, DefaultIdentityCacheTTL)
	c.now = func() time.Time { return *now }
	return c
}

// #104: repeated reports from the same caller within the TTL cost exactly one
// TokenReview + ownership lookup.
func TestCachingVerifier_cachesRepeatedCaller(t *testing.T) {
	inner := &countingVerifier{identity: CallerIdentity{Namespace: "ns", PodName: "p", Class: CallerEgressProxy}}
	now := time.Unix(0, 0)
	c := newTestCachingVerifier(inner, &now)
	session := SessionRef{Namespace: "ns", Name: "s"}

	for i := 0; i < 5; i++ {
		id, err := c.Verify(context.Background(), verifyRequest("tok", "p"), session)
		if err != nil {
			t.Fatalf("verify %d: %v", i, err)
		}
		if id != inner.identity {
			t.Fatalf("identity = %+v, want %+v", id, inner.identity)
		}
	}
	if inner.calls != 1 {
		t.Fatalf("inner verifications = %d, want 1 (cache hits after the first)", inner.calls)
	}
}

// The cache TTL is the revocation-staleness bound: past it the caller is re-verified.
func TestCachingVerifier_expiresAfterTTL(t *testing.T) {
	inner := &countingVerifier{identity: CallerIdentity{Namespace: "ns", PodName: "p"}}
	now := time.Unix(0, 0)
	c := newTestCachingVerifier(inner, &now)
	session := SessionRef{Namespace: "ns", Name: "s"}

	if _, err := c.Verify(context.Background(), verifyRequest("tok", "p"), session); err != nil {
		t.Fatal(err)
	}
	now = now.Add(DefaultIdentityCacheTTL + time.Second)
	if _, err := c.Verify(context.Background(), verifyRequest("tok", "p"), session); err != nil {
		t.Fatal(err)
	}
	if inner.calls != 2 {
		t.Fatalf("inner verifications = %d, want 2 (expired entry re-verified)", inner.calls)
	}
}

// A cached identity is scoped to (token, pod header, session): a token verified for
// one session must not skip the ownership check for another.
func TestCachingVerifier_scopesCacheToTokenPodAndSession(t *testing.T) {
	inner := &countingVerifier{identity: CallerIdentity{Namespace: "ns", PodName: "p"}}
	now := time.Unix(0, 0)
	c := newTestCachingVerifier(inner, &now)

	mustVerify := func(token, pod string, session SessionRef) {
		t.Helper()
		if _, err := c.Verify(context.Background(), verifyRequest(token, pod), session); err != nil {
			t.Fatal(err)
		}
	}
	mustVerify("tok", "p", SessionRef{Namespace: "ns", Name: "a"})
	mustVerify("tok", "p", SessionRef{Namespace: "ns", Name: "b"})  // other session
	mustVerify("tok2", "p", SessionRef{Namespace: "ns", Name: "a"}) // other token
	mustVerify("tok", "q", SessionRef{Namespace: "ns", Name: "a"})  // other pod claim

	if inner.calls != 4 {
		t.Fatalf("inner verifications = %d, want 4 (no cross-key cache hits)", inner.calls)
	}
}

// Failures are never cached: a just-created proxy pod must not stay pinned forbidden,
// and a garbage token must not earn a cache slot.
func TestCachingVerifier_doesNotCacheFailures(t *testing.T) {
	inner := &countingVerifier{err: fmt.Errorf("%w: nope", ErrForbidden)}
	now := time.Unix(0, 0)
	c := newTestCachingVerifier(inner, &now)
	session := SessionRef{Namespace: "ns", Name: "s"}

	for i := 0; i < 3; i++ {
		if _, err := c.Verify(context.Background(), verifyRequest("tok", "p"), session); !errors.Is(err, ErrForbidden) {
			t.Fatalf("verify %d err = %v, want ErrForbidden", i, err)
		}
	}
	if inner.calls != 3 {
		t.Fatalf("inner verifications = %d, want 3 (failures not cached)", inner.calls)
	}
}

// #104: a flood of distinct garbage tokens reaches the inner verifier (and thus the
// apiserver) at most burst-then-sustained-rate times; the excess is rejected with
// ErrVerifyThrottled before any TokenReview.
func TestCachingVerifier_boundsVerificationFloods(t *testing.T) {
	inner := &countingVerifier{err: fmt.Errorf("%w: bad token", ErrUnauthorized)}
	now := time.Unix(0, 0)
	c := newTestCachingVerifier(inner, &now)
	session := SessionRef{Namespace: "ns", Name: "s"}

	throttled := 0
	const flood = 500
	for i := 0; i < flood; i++ {
		_, err := c.Verify(context.Background(), verifyRequest(fmt.Sprintf("garbage-%d", i), "p"), session)
		if errors.Is(err, ErrVerifyThrottled) {
			throttled++
		} else if !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("verify %d err = %v", i, err)
		}
	}
	if inner.calls != defaultVerifyRateBurst {
		t.Fatalf("inner verifications = %d, want %d (global pre-auth bound)", inner.calls, defaultVerifyRateBurst)
	}
	if throttled != flood-defaultVerifyRateBurst {
		t.Fatalf("throttled = %d, want %d", throttled, flood-defaultVerifyRateBurst)
	}
}

// A missing/malformed bearer header is rejected locally: no inner verification and no
// spend from the global verification budget.
func TestCachingVerifier_rejectsMissingBearerLocally(t *testing.T) {
	inner := &countingVerifier{}
	now := time.Unix(0, 0)
	c := newTestCachingVerifier(inner, &now)

	if _, err := c.Verify(context.Background(), verifyRequest("", "p"), SessionRef{Namespace: "ns", Name: "s"}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	if inner.calls != 0 {
		t.Fatalf("inner verifications = %d, want 0", inner.calls)
	}
}

// The cache is bounded: entries beyond the cap are simply not cached (correctness is
// unaffected — the next request re-verifies).
func TestCachingVerifier_boundsCacheSize(t *testing.T) {
	inner := &countingVerifier{identity: CallerIdentity{Namespace: "ns", PodName: "p"}}
	now := time.Unix(0, 0)
	c := newTestCachingVerifier(inner, &now)
	c.limiter = nil // exercise the cache bound alone, without the flood limiter
	session := SessionRef{Namespace: "ns", Name: "s"}

	for i := 0; i < identityCacheMaxEntries+50; i++ {
		if _, err := c.Verify(context.Background(), verifyRequest(fmt.Sprintf("tok-%d", i), "p"), session); err != nil {
			t.Fatal(err)
		}
	}
	if len(c.entries) > identityCacheMaxEntries {
		t.Fatalf("cache entries = %d, want ≤ %d", len(c.entries), identityCacheMaxEntries)
	}
}

// End-to-end mapping: a throttled verification surfaces as 503 + Retry-After (the
// tailer already classifies 5xx as transient — evidence waits and retries).
func TestHandler_verifyThrottledMapsTo503(t *testing.T) {
	cl := newFakeClient()
	h := &Handler{
		Writer:   cl.Status(),
		Reader:   cl,
		Verifier: stubVerifier{err: ErrVerifyThrottled},
	}
	rec := postReport(t, h, ReportRequest{
		Session: SessionRef{Namespace: "ns", Name: "s"},
		Backend: "egress-proxy",
	}, "Bearer ok")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("missing Retry-After on throttled verification")
	}
}
