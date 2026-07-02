/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package enforcement

import "strings"

// MatchDomain reports whether host matches any pattern, using Scrutineer's shared FQDN
// semantics (issue #32) so every egress backend agrees — the cooperative dns-proxy, the
// out-of-pod Envoy's generated RBAC, and the egress-reporter's evidence classification:
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
