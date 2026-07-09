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

func TestMatchDomain(t *testing.T) {
	cases := []struct {
		name     string
		patterns []string
		host     string
		want     bool
	}{
		{"exact match", []string{"example.com"}, "example.com", true},
		{"exact case-insensitive", []string{"Example.COM"}, "example.com", true},
		{"exact host case-insensitive", []string{"example.com"}, "EXAMPLE.com", true},
		{"exact does not cover subdomain", []string{"example.com"}, "api.example.com", false},
		{"exact ignores port", []string{"example.com"}, "example.com:443", true},
		{"no match", []string{"example.com"}, "evil.com", false},
		{"empty patterns", nil, "example.com", false},
		{"empty host", []string{"example.com"}, "", false},

		{"wildcard covers one label", []string{"*.example.com"}, "api.example.com", true},
		{"wildcard covers nested labels", []string{"*.example.com"}, "a.b.example.com", true},
		{"wildcard excludes apex", []string{"*.example.com"}, "example.com", false},
		{"wildcard ignores port", []string{"*.example.com"}, "api.example.com:8443", true},
		{"wildcard case-insensitive", []string{"*.Example.com"}, "API.example.com", true},
		{"wildcard not a suffix-substring", []string{"*.example.com"}, "notexample.com", false},
		{"wildcard rejects evil suffix trick", []string{"*.example.com"}, "example.com.evil.com", false},

		{"mixed list matches exact", []string{"*.example.com", "foo.test"}, "foo.test", true},
		{"mixed list matches wildcard", []string{"foo.test", "*.example.com"}, "x.example.com", true},

		{"pattern whitespace trimmed", []string{"  example.com  "}, "example.com", true},
		{"blank pattern ignored", []string{"", "example.com"}, "example.com", true},
		{"trailing dot on host normalized", []string{"example.com"}, "example.com.", true},
	}
	for _, tc := range cases {
		if got := MatchDomain(tc.patterns, tc.host); got != tc.want {
			t.Errorf("%s: MatchDomain(%v, %q) = %v, want %v", tc.name, tc.patterns, tc.host, got, tc.want)
		}
	}
}

// #103: domain patterns feed three brittle carriers (single-quoted Envoy RBAC regex,
// comma-joined env round-trip, MatchDomain) — the shared validator must reject every
// character class that breaks any of them, with an actionable message.
func TestValidateDomainPattern(t *testing.T) {
	valid := []string{
		"example.com",
		"*.example.com",
		"api-v2.example.co.uk",
		"localhost",
		"a",
		"*.a",
		"0-0.example",
		"  example.com  ", // surrounding whitespace is trimmed, matching MatchDomain
	}
	for _, p := range valid {
		if err := ValidateDomainPattern(p); err != nil {
			t.Errorf("ValidateDomainPattern(%q) = %v, want nil", p, err)
		}
	}

	invalid := []struct {
		pattern string
		wantSub string // actionable fragment the error must contain
	}{
		{"", "empty"},
		{"   ", "empty"},
		{"evil'co.example", "'"},           // breaks the single-quoted RBAC YAML
		{"a,b.example", ","},               // splits into two patterns in the CSV env
		{"example.com:8080", "port"},       // enforces in RBAC, never matches in evidence
		{"exa mple.com", "whitespace"},     // inner whitespace
		{"Example.com", "lowercase"},       // MatchDomain lowercases; carriers do not
		{"*", "wildcard"},                  // bare star
		{"*.", "wildcard"},                 // wildcard with nothing to match
		{"a.*.example.com", "wildcard"},    // star not leading
		{"foo.*", "wildcard"},              // star not leading
		{"**.example.com", "wildcard"},     // double star
		{"..example.com", "empty label"},   // empty label
		{"example..com", "empty label"},    // empty label
		{".example.com", "empty label"},    // leading dot
		{"example.com.", "empty label"},    // trailing dot
		{"evil\nco.example", "whitespace"}, // newline breaks the YAML scalar
		{"under_score.example", `"_"`},     // outside the allowlist
		{strings.Repeat("a", 254), "253"},  // too long for a hostname
	}
	for _, tc := range invalid {
		err := ValidateDomainPattern(tc.pattern)
		if err == nil {
			t.Errorf("ValidateDomainPattern(%q) = nil, want error containing %q", tc.pattern, tc.wantSub)
			continue
		}
		if !strings.Contains(err.Error(), tc.wantSub) {
			t.Errorf("ValidateDomainPattern(%q) = %q, want it to contain %q", tc.pattern, err.Error(), tc.wantSub)
		}
	}
}

// ValidateDomainRules attributes the failing entry by field and index so the session's
// Denied message points at the exact value.
func TestValidateDomainRules(t *testing.T) {
	rules := scrutineerv1alpha1.PolicyRules{
		AllowedDomains: []string{"good.example"},
		DeniedDomains:  []string{"fine.example", "evil'co.example"},
	}
	err := ValidateDomainRules(rules)
	if err == nil {
		t.Fatal("expected an error for the quoted pattern")
	}
	for _, want := range []string{"deniedDomains[1]", "evil'co.example"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q must contain %q", err.Error(), want)
		}
	}
	if err := ValidateDomainRules(scrutineerv1alpha1.PolicyRules{
		AllowedDomains: []string{"*.example.com"},
		DeniedDomains:  []string{"tracker.example"},
	}); err != nil {
		t.Fatalf("valid rules rejected: %v", err)
	}
}
