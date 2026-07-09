/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package enforcement

import (
	"fmt"
	"strings"
	"unicode"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
)

// MatchDomain reports whether host matches any pattern, using Scrutineer's shared FQDN
// semantics (issue #32) so every egress consumer agrees — the out-of-pod Envoy's
// generated RBAC and the egress-reporter's evidence classification:
//
//   - exact:    "example.com" matches only "example.com"
//   - wildcard: "*.example.com" matches any subdomain ("a.example.com", "a.b.example.com")
//     but NOT the apex "example.com"
//
// Matching is case-insensitive; a leading "www." is not special; the host's optional
// ":port" suffix and a single trailing dot are ignored; blank patterns are skipped.
func MatchDomain(patterns []string, host string) bool {
	h := normalizeHost(host)
	if h == "" {
		return false
	}
	for _, p := range patterns {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		if suffix, ok := strings.CutPrefix(p, "*."); ok {
			// "*.example.com": host must be "<label(s)>.example.com" (apex excluded).
			if suffix != "" && strings.HasSuffix(h, "."+suffix) {
				return true
			}
			continue
		}
		if h == p {
			return true
		}
	}
	return false
}

// maxDomainPatternLen is the DNS hostname length bound.
const maxDomainPatternLen = 253

// ValidateDomainPattern checks one allowedDomains/deniedDomains pattern against the
// shared charset contract: lowercase letters, digits, '.', '-', with an optional single
// leading "*." wildcard. It is the shared precondition (#103) for every carrier the
// pattern is later embedded into — the single-quoted regex in the Envoy bootstrap RBAC
// (envoy/rbac.go), the comma-joined AGENT_POLICY_* env round-trip (envoy/env.go ↔
// containerenv.SplitCSV), and MatchDomain above — each of which a hostile character
// breaks differently: a quote or newline crashloops the proxy on an invalid bootstrap,
// a comma silently splits the pattern on the evidence side only, and a ":port" enforces
// in the RBAC but never matches in evidence (MatchDomain strips host ports). Callers
// reject invalid patterns at reconcile time so the failure is a clear phase=Denied, not
// a dead or divergent data plane.
func ValidateDomainPattern(pattern string) error {
	p := strings.TrimSpace(pattern)
	if p == "" {
		return fmt.Errorf("domain pattern is empty")
	}
	if len(p) > maxDomainPatternLen {
		return fmt.Errorf("domain pattern is %d bytes, over the %d-byte hostname bound", len(p), maxDomainPatternLen)
	}
	rest, isWildcard := strings.CutPrefix(p, "*.")
	if isWildcard && rest == "" {
		return fmt.Errorf("wildcard pattern %q has nothing after \"*.\"", p)
	}
	if strings.ContainsRune(rest, '*') {
		return fmt.Errorf("domain pattern %q: wildcard \"*\" is only supported as a single leading \"*.\"", p)
	}
	for _, label := range strings.Split(rest, ".") {
		if label == "" {
			return fmt.Errorf("domain pattern %q has an empty label (leading, trailing, or doubled dot)", p)
		}
		for _, r := range label {
			switch {
			case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			case r >= 'A' && r <= 'Z':
				return fmt.Errorf("domain pattern %q must be lowercase", p)
			case r == ':':
				return fmt.Errorf("domain pattern %q: port-scoped patterns are not supported (matching strips the host port)", p)
			case unicode.IsSpace(r):
				return fmt.Errorf("domain pattern %q contains whitespace", p)
			default:
				return fmt.Errorf("domain pattern %q: character %q is not allowed (allowed: lowercase letters, digits, '.', '-', optional leading \"*.\")", p, string(r))
			}
		}
	}
	return nil
}

// ValidateDomainRules validates every domain pattern in rules, attributing a failure to
// its field and index so the surfaced error points at the exact offending value.
func ValidateDomainRules(rules scrutineerv1alpha1.PolicyRules) error {
	for i, p := range rules.AllowedDomains {
		if err := ValidateDomainPattern(p); err != nil {
			return fmt.Errorf("allowedDomains[%d] %q: %w", i, p, err)
		}
	}
	for i, p := range rules.DeniedDomains {
		if err := ValidateDomainPattern(p); err != nil {
			return fmt.Errorf("deniedDomains[%d] %q: %w", i, p, err)
		}
	}
	return nil
}

// normalizeHost lowercases host and strips its optional port and a single trailing dot.
func normalizeHost(host string) string {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "" {
		return ""
	}
	// Strip ":port" (IPv4/hostname authority). Bracketed IPv6 literals are not domains,
	// so they never match domain patterns anyway; leave them untouched.
	if !strings.HasPrefix(h, "[") {
		if i := strings.LastIndexByte(h, ':'); i >= 0 && !strings.Contains(h[i+1:], ".") {
			h = h[:i]
		}
	}
	return strings.TrimSuffix(h, ".")
}
