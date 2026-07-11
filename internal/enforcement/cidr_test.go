/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package enforcement

import (
	"strings"
	"testing"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
)

// #125: CIDR patterns feed the same brittle carriers as domain patterns (#103) — the
// single-quoted Envoy RBAC regex, the comma-joined AGENT_POLICY_*_CIDRS env round-trip,
// and MatchIPCIDR — so the validator admits only canonical IPv4 forms every carrier
// renders identically.
func TestValidateCIDRPattern(t *testing.T) {
	valid := []string{
		"203.0.113.5", // bare IP is an exact-address rule (/32)
		"10.0.0.0/8",
		"192.168.0.0/16",
		"198.51.100.32/27",
		"203.0.113.5/32",
		"0.0.0.0/0",
		"  10.0.0.0/8  ", // surrounding whitespace trimmed, matching domain patterns
	}
	for _, p := range valid {
		if err := ValidateCIDRPattern(p); err != nil {
			t.Errorf("ValidateCIDRPattern(%q) = %v, want nil", p, err)
		}
	}

	invalid := []struct {
		pattern string
		wantSub string // actionable fragment the error must contain
	}{
		{"", "empty"},
		{"   ", "empty"},
		{"fd00::/8", "IPv6"},     // posture #66: the egress path is IPv4-only
		{"2001:db8::1", "IPv6"},  // ditto for bare v6 addresses
		{"::ffff:10.2.3.4", "IPv6"},
		{"010.2.3.4", "canonical"},   // leading zero (octal ambiguity)
		{"10.020.3.4/8", "canonical"},
		{"0x0a.0.0.1", "not allowed"}, // hex form is outside the [0-9./] charset
		{"167772161", "canonical"},    // single-integer form of 10.0.0.1
		{"db.example.com", "not allowed"}, // hostname — belongs in the domain fields
		{"10.0.0.0 /8", "whitespace"},     // inner whitespace
		{"10.1.2.3/8", "network address"}, // host bits set: must be the masked form
		{"10.0.0.0/33", "CIDR"},
		{"10.0.0.256", "canonical"},
		{"10.0.0.0/", "CIDR"},
		{"/8", "CIDR"},
		{"10.0.0.0/8/8", "CIDR"},
		{"1.2.3.4,5.6.7.8", "not allowed"}, // ',' would split the CSV env round-trip
		{"10.0.0.0'/8", "not allowed"},     // ''' would break the single-quoted YAML
	}
	for _, tc := range invalid {
		err := ValidateCIDRPattern(tc.pattern)
		if err == nil {
			t.Errorf("ValidateCIDRPattern(%q) = nil, want error containing %q", tc.pattern, tc.wantSub)
			continue
		}
		if !strings.Contains(err.Error(), tc.wantSub) {
			t.Errorf("ValidateCIDRPattern(%q) = %q, want it to contain %q", tc.pattern, err.Error(), tc.wantSub)
		}
	}
}

// ValidateCIDRRules attributes the failing entry by field and index so the session's
// Denied message points at the exact value, mirroring ValidateDomainRules.
func TestValidateCIDRRules(t *testing.T) {
	rules := scrutineerv1alpha1.PolicyRules{
		AllowedCIDRs: []string{"10.0.0.0/8"},
		DeniedCIDRs:  []string{"192.168.0.0/16", "fd00::/8"},
	}
	err := ValidateCIDRRules(rules)
	if err == nil {
		t.Fatal("expected an error for the IPv6 entry")
	}
	for _, want := range []string{"deniedCIDRs[1]", "fd00::/8"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q must contain %q", err.Error(), want)
		}
	}
	if err := ValidateCIDRRules(scrutineerv1alpha1.PolicyRules{
		AllowedCIDRs: []string{"203.0.113.5", "10.0.0.0/8"},
		DeniedCIDRs:  []string{"169.254.0.0/16"},
	}); err != nil {
		t.Fatalf("valid rules rejected: %v", err)
	}
}

// MatchIPCIDR is the evidence-side twin of the Envoy CIDR RBAC (#125): an authority that
// is an IPv4 literal matches exact-IP and CIDR-containment rules; anything that is not a
// canonical IPv4 literal — hostnames, IPv6, octal/hex/integer forms — never matches.
func TestMatchIPCIDR(t *testing.T) {
	cases := []struct {
		name     string
		patterns []string
		host     string
		want     bool
	}{
		{"exact match", []string{"203.0.113.5"}, "203.0.113.5", true},
		{"exact ignores port", []string{"203.0.113.5"}, "203.0.113.5:443", true},
		{"exact no match", []string{"203.0.113.5"}, "203.0.113.6", false},

		{"cidr contains", []string{"10.0.0.0/8"}, "10.2.3.4", true},
		{"cidr contains with port", []string{"10.0.0.0/8"}, "10.2.3.4:5432", true},
		{"cidr broadcast edge", []string{"10.0.0.0/8"}, "10.255.255.255", true},
		{"cidr network edge", []string{"10.0.0.0/8"}, "10.0.0.0", true},
		{"cidr below range", []string{"10.0.0.0/8"}, "9.255.255.255", false},
		{"cidr above range", []string{"10.0.0.0/8"}, "11.0.0.0", false},
		{"partial-octet cidr inside", []string{"198.51.100.32/27"}, "198.51.100.63", true},
		{"partial-octet cidr outside", []string{"198.51.100.32/27"}, "198.51.100.64", false},

		{"trailing dot normalized", []string{"10.0.0.0/8"}, "10.2.3.4.", true},
		{"mixed list exact", []string{"10.0.0.0/8", "203.0.113.5"}, "203.0.113.5", true},
		{"blank pattern ignored", []string{"", "10.0.0.0/8"}, "10.1.1.1", true},

		// The evil-suffix trick: an IP-prefixed hostname is a hostname, not an IP.
		{"ip-prefixed hostname", []string{"10.0.0.0/8"}, "10.2.3.4.evil.com", false},
		{"hostname never matches", []string{"10.0.0.0/8"}, "db.internal", false},
		{"hostname with port", []string{"10.0.0.0/8"}, "db.internal:5432", false},

		// Non-canonical IP forms are not IP literals (Envoy treats them as DNS names).
		{"leading-zero form", []string{"10.0.0.0/8"}, "010.2.3.4", false},
		{"hex form", []string{"10.0.0.0/8"}, "0x0a.2.3.4", false},
		{"integer form", []string{"10.0.0.0/8"}, "167772161", false},

		// IPv6 never matches (posture #66), including the v4-mapped form.
		{"bracketed ipv6", []string{"10.0.0.0/8"}, "[fd00::1]:443", false},
		{"v4-mapped ipv6", []string{"10.0.0.0/8"}, "::ffff:10.2.3.4", false},

		{"empty patterns", nil, "10.2.3.4", false},
		{"empty host", []string{"10.0.0.0/8"}, "", false},

		// Invalid patterns are skipped (validation runs upstream), never matched loosely.
		{"invalid pattern ignored", []string{"10.2.3.4:443"}, "10.2.3.4", false},
		{"unmasked pattern ignored", []string{"10.1.2.3/8"}, "10.2.3.4", false},
	}
	for _, tc := range cases {
		if got := MatchIPCIDR(tc.patterns, tc.host); got != tc.want {
			t.Errorf("%s: MatchIPCIDR(%v, %q) = %v, want %v", tc.name, tc.patterns, tc.host, got, tc.want)
		}
	}
}
