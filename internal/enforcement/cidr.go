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
	"net/netip"
	"strings"
	"unicode"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
)

// MatchIPCIDR reports whether host is an IPv4-literal authority matching any
// allowedCIDRs/deniedCIDRs pattern, using Scrutineer's shared CIDR semantics (issue #125)
// so every egress consumer agrees — the out-of-pod Envoy's generated RBAC regex and the
// egress-reporter's evidence classification:
//
//   - exact: "203.0.113.5" matches only that address (an implicit /32)
//   - CIDR:  "10.0.0.0/8" matches any address the prefix contains
//
// The host's optional ":port" suffix and a single trailing dot are ignored (same
// normalization as MatchDomain). A host that is not a canonical IPv4 dotted-quad —
// a hostname, an IPv6 literal (including the v4-mapped form; posture #66), or a
// leading-zero/hex/integer IP form — never matches: only IP-literal dials are policed
// here. A hostname that *resolves to* an address inside a pattern is out of scope by
// design (see the API field docs).
func MatchIPCIDR(patterns []string, host string) bool {
	h := normalizeHost(host)
	if h == "" {
		return false
	}
	addr, err := netip.ParseAddr(h)
	if err != nil || !addr.Is4() {
		return false
	}
	for _, p := range patterns {
		if prefix, ok := CIDRPatternPrefix(p); ok && prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// CIDRPatternPrefix parses one allowedCIDRs/deniedCIDRs pattern into its IPv4 prefix (a
// bare address becomes a /32). It is the single pattern grammar shared by both consumers
// — MatchIPCIDR above and the Envoy RBAC regex generation (envoy/rbac.go) — so evidence
// and enforcement can never disagree about what a pattern covers. ok is false for
// exactly the patterns ValidateCIDRPattern rejects; callers skip those (they were
// already rejected at reconcile time).
func CIDRPatternPrefix(pattern string) (netip.Prefix, bool) {
	p := strings.TrimSpace(pattern)
	if p == "" {
		return netip.Prefix{}, false
	}
	if strings.Contains(p, "/") {
		prefix, err := netip.ParsePrefix(p)
		if err != nil || !prefix.Addr().Is4() || prefix.Masked() != prefix {
			return netip.Prefix{}, false
		}
		return prefix, true
	}
	addr, err := netip.ParseAddr(p)
	if err != nil || !addr.Is4() {
		return netip.Prefix{}, false
	}
	return netip.PrefixFrom(addr, 32), true
}

// ValidateCIDRPattern checks one allowedCIDRs/deniedCIDRs pattern against the shared
// grammar: a canonical IPv4 dotted-quad (an exact-address rule, /32) or a masked IPv4
// CIDR, characters `[0-9./]` only. It is the CIDR twin of ValidateDomainPattern and the
// same #103 precondition for every carrier the pattern is later embedded into — the
// single-quoted regex in the Envoy bootstrap RBAC (envoy/rbac.go), the comma-joined
// AGENT_POLICY_*_CIDRS env round-trip (envoy/env.go ↔ containerenv.SplitCSV), and
// MatchIPCIDR above. IPv6 is rejected by posture, not oversight (#66): the whole egress
// path is IPv4-only. Non-canonical IPv4 forms (leading zeros, hex/decimal integers) are
// rejected because clients and resolvers disagree about what they mean — the canonical
// dotted-quad is the only spelling enforcement and evidence both understand. Callers
// reject invalid patterns at reconcile time so the failure is a clear phase=Denied.
func ValidateCIDRPattern(pattern string) error {
	p := strings.TrimSpace(pattern)
	if p == "" {
		return fmt.Errorf("CIDR pattern is empty")
	}
	if strings.ContainsRune(p, ':') {
		return fmt.Errorf("CIDR pattern %q: IPv6 is not supported (the egress path is IPv4-only, #66 posture)", p)
	}
	for _, r := range p {
		switch {
		case r >= '0' && r <= '9', r == '.', r == '/':
		case unicode.IsSpace(r):
			return fmt.Errorf("CIDR pattern %q contains whitespace", p)
		default:
			return fmt.Errorf("CIDR pattern %q: character %q is not allowed (allowed: digits, '.', '/'; hostnames belong in allowedDomains/deniedDomains)", p, string(r))
		}
	}
	if strings.Contains(p, "/") {
		prefix, err := netip.ParsePrefix(p)
		if err != nil {
			return fmt.Errorf("CIDR pattern %q is not a valid IPv4 CIDR (canonical dotted-quad/prefix-length, no leading zeros)", p)
		}
		if masked := prefix.Masked(); masked != prefix {
			return fmt.Errorf("CIDR pattern %q has host bits set: use the masked network address %s", p, masked)
		}
		return nil
	}
	if addr, err := netip.ParseAddr(p); err != nil || !addr.Is4() {
		return fmt.Errorf("CIDR pattern %q is not a canonical IPv4 address (dotted-quad, no leading zeros) or CIDR", p)
	}
	return nil
}

// IsNonCanonicalNumericAuthority reports whether host is an all-numeric dotted authority
// that is NOT a canonical IPv4 dotted-quad — the class a permissive resolver (c-ares /
// inet_aton) may still expand into an address our canonical CIDR matching cannot see:
// leading-zero octets ("010.0.0.1" → 10.0.0.1) and short forms ("10.1"/"10.0.1" →
// 10.0.0.1). Such an authority is refused fail-closed at the proxy when CIDR policy is
// active (#126), so it cannot evade a deniedCIDRs deny-list.
//
// It returns false for hostnames (any character outside [0-9.]), for canonical
// dotted-quads (handled by the normal CIDR match), and for malformed numeric forms with
// an empty part or an over-255 / hex / single-integer-with-no-dots-that-parses octet —
// those fail closed at the resolver, and excluding them keeps this predicate exactly
// equal to the generated RBAC regex (envoy.nonCanonicalNumericAuthorityRegex), which is
// the invariant the consistency test guards. The one exception: a dotless all-digit
// authority (e.g. "167772161") is a 1-part inet_aton form and IS flagged.
func IsNonCanonicalNumericAuthority(host string) bool {
	h := normalizeHost(host)
	if h == "" {
		return false
	}
	for i := 0; i < len(h); i++ {
		if (h[i] < '0' || h[i] > '9') && h[i] != '.' {
			return false // a non-numeric character → a hostname, not a numeric form
		}
	}
	parts := strings.Split(h, ".")
	for _, p := range parts {
		if p == "" {
			return false // empty part: malformed, rejected by the resolver (fail-closed)
		}
	}
	if len(parts) != 4 {
		return true // 1, 2, 3, or 5+ parts: a non-canonical short/long form
	}
	for _, p := range parts {
		if len(p) > 1 && p[0] == '0' {
			return true // leading-zero octet
		}
	}
	return false // canonical-shaped 4-quad (in-range matching, or >255 → fails closed)
}

// ValidateCIDRRules validates every CIDR pattern in rules, attributing a failure to its
// field and index so the surfaced error points at the exact offending value.
func ValidateCIDRRules(rules scrutineerv1alpha1.PolicyRules) error {
	for i, p := range rules.AllowedCIDRs {
		if err := ValidateCIDRPattern(p); err != nil {
			return fmt.Errorf("allowedCIDRs[%d] %q: %w", i, p, err)
		}
	}
	for i, p := range rules.DeniedCIDRs {
		if err := ValidateCIDRPattern(p); err != nil {
			return fmt.Errorf("deniedCIDRs[%d] %q: %w", i, p, err)
		}
	}
	return nil
}
