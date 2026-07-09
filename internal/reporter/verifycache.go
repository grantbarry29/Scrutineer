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
	"crypto/sha256"
	"net/http"
	"sync"
	"time"
)

const (
	// DefaultIdentityCacheTTL bounds reuse of a verified caller identity. It is the
	// revocation-staleness window: a deleted pod or rotated ServiceAccount keeps its
	// reporting access for at most this long after revocation. Chosen well under the
	// 600s projected-token lifetime, and long enough that an egress-reporter polling
	// every 2s costs the apiserver ~1 TokenReview per TTL instead of one per post.
	DefaultIdentityCacheTTL = 15 * time.Second
	// identityCacheMaxEntries bounds the cache map. Only successful verifications
	// are cached, so filling it requires that many distinct *valid* identities;
	// overflow skips caching (correctness unaffected — the next request re-verifies).
	identityCacheMaxEntries = 4096
	// defaultVerifyRateInterval/Burst globally bound cache-miss verifications — the
	// requests that cost the apiserver a TokenReview create plus uncached pod/Job
	// GETs. A flood of garbage-token requests is capped at this rate before any
	// apiserver call (#104); cache hits bypass the bound entirely.
	defaultVerifyRateInterval = 20 * time.Millisecond // 50/s sustained
	defaultVerifyRateBurst    = 100

	// verifyLimiterKey is the single key of the global (not per-session — the caller
	// is not authenticated yet) verification rate limiter.
	verifyLimiterKey = "verify"
)

// cachingVerifier wraps an IdentityVerifier with the two apiserver-amplification
// bounds of #104:
//
//  1. a short-TTL verified-identity cache keyed by hash(token, pod claim, session),
//     so a well-behaved caller re-presenting the same token every poll costs at most
//     one TokenReview + ownership lookup per TTL;
//  2. a global rate limit on cache misses, so unauthenticated floods reach the
//     apiserver at a bounded rate. New legitimate callers compete with an active
//     flood for this budget — the documented trade-off; established (cached) callers
//     are unaffected.
//
// This deliberately does not conflict with the uncached-reads policy on
// reporter.Options (#47): that policy is about informer caches and read-after-write
// consistency for the status merge, which stays uncached. No single-flight: each
// session has one egress-reporter posting sequentially, so concurrent same-token
// misses are rare and already inside the limiter's budget.
type cachingVerifier struct {
	inner IdentityVerifier
	ttl   time.Duration
	now   func() time.Time

	// limiter bounds cache-miss verifications globally (single key); nil disables.
	limiter *sessionRateLimiter

	mu      sync.Mutex
	entries map[[sha256.Size]byte]identityCacheEntry
}

type identityCacheEntry struct {
	identity CallerIdentity
	expiry   time.Time
}

func newCachingVerifier(inner IdentityVerifier, ttl time.Duration) *cachingVerifier {
	if ttl <= 0 {
		ttl = DefaultIdentityCacheTTL
	}
	return &cachingVerifier{
		inner:   inner,
		ttl:     ttl,
		now:     time.Now,
		limiter: newSessionRateLimiter(defaultVerifyRateInterval, defaultVerifyRateBurst),
		entries: make(map[[sha256.Size]byte]identityCacheEntry),
	}
}

// Verify implements IdentityVerifier.
func (c *cachingVerifier) Verify(ctx context.Context, r *http.Request, session SessionRef) (CallerIdentity, error) {
	token, err := bearerToken(r.Header.Get("Authorization"))
	if err != nil {
		// Missing/malformed header: rejected locally — no apiserver cost, and no
		// spend from the verification budget.
		return CallerIdentity{}, err
	}
	key := identityCacheKey(token, r.Header.Get(headerScrutineerPod), session)
	now := c.now()
	if id, ok := c.lookup(key, now); ok {
		return id, nil
	}
	if c.limiter != nil && !c.limiter.allow(verifyLimiterKey, now) {
		return CallerIdentity{}, ErrVerifyThrottled
	}
	id, err := c.inner.Verify(ctx, r, session)
	if err != nil {
		// Never cache failures: they are cheap to recompute, and a negative cache
		// would pin a just-created proxy pod as forbidden for a whole TTL.
		return CallerIdentity{}, err
	}
	c.store(key, id, now)
	return id, nil
}

// identityCacheKey scopes a cached identity to exactly the inputs the inner verifier
// consumed: bearer token, pod-claim header, and session. Hashing keeps raw tokens out
// of the long-lived map (and out of anything that dumps it).
func identityCacheKey(token, podHeader string, session SessionRef) [sha256.Size]byte {
	h := sha256.New()
	for _, part := range []string{token, podHeader, session.Namespace, session.Name} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	var key [sha256.Size]byte
	copy(key[:], h.Sum(nil))
	return key
}

func (c *cachingVerifier) lookup(key [sha256.Size]byte, now time.Time) (CallerIdentity, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok || !now.Before(e.expiry) {
		return CallerIdentity{}, false
	}
	return e.identity, true
}

func (c *cachingVerifier) store(key [sha256.Size]byte, id CallerIdentity, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, e := range c.entries {
		if !now.Before(e.expiry) {
			delete(c.entries, k)
		}
	}
	if len(c.entries) >= identityCacheMaxEntries {
		return
	}
	c.entries[key] = identityCacheEntry{identity: id, expiry: now.Add(c.ttl)}
}
