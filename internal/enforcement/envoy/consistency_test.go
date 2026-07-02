/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package envoy

import (
	"regexp"
	"testing"

	"github.com/grantbarry29/scrutineer/internal/enforcement"
)

// TestEnforcementEvidenceMatchConsistency is the #32 invariant guard: the Envoy RBAC
// (authorityRegex, enforcement) and the egress-reporter's classification
// (enforcement.MatchDomain, evidence) must agree on the same authority for the same
// patterns — otherwise a request could be blocked without a deny record, or recorded as a
// deny while it was allowed. Both are exercised here against a shared case table so the two
// implementations can never silently drift.
func TestEnforcementEvidenceMatchConsistency(t *testing.T) {
	patternSets := [][]string{
		{"example.com"},
		{"*.example.com"},
		{"example.com", "*.example.com"},
		{"api.github.com", "*.githubusercontent.com"},
		{"*.evil.example"},
	}
	hosts := []string{
		"example.com",
		"example.com:443",
		"example.com.", // trailing dot (rare, but must classify the same either way)
		"api.example.com",
		"a.b.example.com",
		"api.example.com:8443",
		"notexample.com",
		"example.com.evil.com",
		"api.github.com",
		"raw.githubusercontent.com",
		"github.com",
		"c2.evil.example",
		"evil.example",
	}

	for _, patterns := range patternSets {
		re := authorityRegex(patterns)
		var rx *regexp.Regexp
		if re != "" {
			// Anchored full match, mirroring Envoy safe_regex.
			compiled, err := regexp.Compile(`\A(?:` + re + `)\z`)
			if err != nil {
				t.Fatalf("authorityRegex(%v) = %q: compile error %v", patterns, re, err)
			}
			rx = compiled
		}
		for _, host := range hosts {
			viaMatcher := enforcement.MatchDomain(patterns, host)
			viaRegex := rx != nil && rx.MatchString(host)
			if viaMatcher != viaRegex {
				t.Errorf("divergence for patterns=%v host=%q: MatchDomain=%v regex=%v (re=%q)",
					patterns, host, viaMatcher, viaRegex, re)
			}
		}
	}
}
