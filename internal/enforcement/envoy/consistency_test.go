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

// TestCIDREnforcementEvidenceMatchConsistency is the #125 twin of the FQDN guard above:
// the Envoy CIDR RBAC regex (enforcement) and enforcement.MatchIPCIDR (the
// egress-reporter's evidence classification) must agree on the same authority for the
// same patterns — including the evasion corpus (IP-prefixed hostnames, leading-zero /
// hex / integer IP forms, IPv6, trailing dots, ports).
func TestCIDREnforcementEvidenceMatchConsistency(t *testing.T) {
	patternSets := [][]string{
		{"10.0.0.0/8"},
		{"203.0.113.5"},
		{"198.51.100.32/27"},
		{"10.0.0.0/8", "203.0.113.5"},
		{"0.0.0.0/0"},
	}
	hosts := []string{
		"10.2.3.4", "10.2.3.4:5432", "10.2.3.4.", // in-range, port, trailing dot
		"10.0.0.0", "10.255.255.255", // /8 boundaries
		"9.255.255.255", "11.0.0.0", // one outside each /8 end
		"203.0.113.5", "203.0.113.5:443", "203.0.113.6",
		"198.51.100.31", "198.51.100.32", "198.51.100.63", "198.51.100.64",
		"10.2.3.4.evil.com",                    // IP-prefixed hostname must stay a hostname
		"010.2.3.4", "0x0a.2.3.4", "167772161", // non-canonical IP forms
		"db.internal", "example.com:443", // plain hostnames
		"[fd00::1]:443", "::ffff:10.2.3.4", // IPv6 forms (posture #66: never match)
		"0.0.0.0", "255.255.255.255",
	}

	for _, patterns := range patternSets {
		re := egressAuthorityRegex(nil, patterns)
		if re == "" {
			t.Fatalf("egressAuthorityRegex(nil, %v) rendered empty", patterns)
		}
		rx, err := regexp.Compile(`\A(?:` + re + `)\z`)
		if err != nil {
			t.Fatalf("egressAuthorityRegex(nil, %v) = %q: compile error %v", patterns, re, err)
		}
		for _, host := range hosts {
			viaMatcher := enforcement.MatchIPCIDR(patterns, host)
			viaRegex := rx.MatchString(host)
			if viaMatcher != viaRegex {
				t.Errorf("divergence for patterns=%v host=%q: MatchIPCIDR=%v regex=%v (re=%q)",
					patterns, host, viaMatcher, viaRegex, re)
			}
		}
	}
}
