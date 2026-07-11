/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package envoy

import (
	"crypto/sha256"
	"encoding/hex"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/grantbarry29/scrutineer/internal/enforcement"
)

// ConfigHashAnnotation stamps the egress ConfigMap and Pod with a hash of the effective
// FQDN policy, so the controller detects a policy change and re-renders/recreates them
// (Envoy has no live reload of a mounted bootstrap). See egress_envoy.go.
const ConfigHashAnnotation = "scrutineer.sh/egress-config-hash"

// Hash is a stable digest of the policy-affecting fields (Port excluded — it is constant).
// The same value stamped on the ConfigMap and the Pod lets the controller detect drift.
// Every policy-carrying field MUST be written here: a field the hash misses is a field
// whose live edits never propagate to the proxy.
func (c BootstrapConfig) Hash() string {
	h := sha256.New()
	if c.Enforce {
		h.Write([]byte("enforce\n"))
	}
	writeSorted := func(tag string, in []string) {
		vals := append([]string(nil), in...)
		for i := range vals {
			vals[i] = strings.ToLower(strings.TrimSpace(vals[i]))
		}
		sort.Strings(vals)
		h.Write([]byte(tag))
		for _, v := range vals {
			h.Write([]byte(v))
			h.Write([]byte{0})
		}
	}
	writeSorted("allow\n", c.AllowedDomains)
	writeSorted("deny\n", c.DeniedDomains)
	writeSorted("allowcidr\n", c.AllowedCIDRs)
	writeSorted("denycidr\n", c.DeniedCIDRs)
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// BootstrapConfig parameterizes the rendered Envoy bootstrap. AllowedDomains/DeniedDomains
// are the session's effective FQDN policy; AllowedCIDRs/DeniedCIDRs are its effective
// IP/CIDR policy, enforced as authority-string matching against IPv4-literal dials
// (#125). Enforce is true only in enforced mode. In audit mode Enforce is false, so no
// RBAC is generated (Envoy forwards freely) and the egress-reporter records would-be
// denials as dry-run evidence (#32).
type BootstrapConfig struct {
	Port           int
	Enforce        bool
	AllowedDomains []string
	DeniedDomains  []string
	AllowedCIDRs   []string
	DeniedCIDRs    []string
}

// hasEgressPolicy reports whether enforced egress rules (FQDN or CIDR) should generate RBAC.
func (c BootstrapConfig) hasEgressPolicy() bool {
	return c.Enforce && (len(c.DeniedDomains) > 0 || len(c.AllowedDomains) > 0 ||
		len(c.DeniedCIDRs) > 0 || len(c.AllowedCIDRs) > 0)
}

// hasCIDRPolicy reports whether the session expresses IP-level intent (allow or deny
// CIDRs). When it does, non-canonical numeric authorities are refused (#126) so they
// cannot evade the CIDR rules via a resolver-expanded spelling.
func (c BootstrapConfig) hasCIDRPolicy() bool {
	return len(c.DeniedCIDRs) > 0 || len(c.AllowedCIDRs) > 0
}

// authorityRegex renders an RE2 pattern (for Envoy safe_regex, which full-matches) that
// matches a request :authority against the FQDN patterns alone, mirroring
// enforcement.MatchDomain. See egressAuthorityRegex for the tolerances.
func authorityRegex(patterns []string) string {
	return egressAuthorityRegex(patterns, nil)
}

// egressAuthorityRegex renders a SINGLE combined RE2 pattern matching a request
// :authority against the union of the FQDN alternations (mirroring
// enforcement.MatchDomain — exact hosts, "*." wildcards with the apex excluded,
// case-insensitive) and the CIDR alternations (mirroring enforcement.MatchIPCIDR — exact
// IPv4 addresses and per-octet range expansions of CIDR prefixes), with an optional
// ":port" suffix and a tolerated trailing dot. Returns "" when no usable pattern is
// present.
//
// This combined form is the semantic reference the evidence-parity tests compile; the
// deployed RBAC filter instead uses authorityMatchRegexes (one regex per CIDR) to keep
// each RE2 program under Envoy's cap — the two are equivalent because RBAC OR-combines a
// policy's permissions.
//
// Precondition (#103/#125): patterns passed the shared enforcement validators at
// reconcile time. The rendered regex is embedded in a SINGLE-QUOTED YAML scalar in the
// bootstrap and regexp.QuoteMeta does not escape quotes or newlines — an unvalidated
// pattern containing either would produce an invalid bootstrap and a crashlooping proxy.
func egressAuthorityRegex(domains, cidrs []string) string {
	return wrapAuthority(append(domainAlternations(domains), cidrAlternations(cidrs)...))
}

// wrapAuthority wraps alternation fragments into a full authority-match regex. `\.?`
// tolerates a trailing-dot (root) authority so "example.com." or "10.2.3.4." can't slip
// past a match the undotted form catches, and `(:[0-9]+)?` tolerates the port — matching
// the normalization in enforcement.MatchDomain/MatchIPCIDR. Returns "" for no fragments.
func wrapAuthority(alts []string) string {
	if len(alts) == 0 {
		return ""
	}
	return `(?i)(` + strings.Join(alts, "|") + `)\.?(:[0-9]+)?`
}

// authorityMatchRegexes returns the set of RE2 patterns for ONE RBAC filter, split so no
// single pattern approaches Envoy's RE2 max_program_size (default 100, above which Envoy
// rejects the bootstrap and the proxy crashloops). All FQDN patterns share one compact
// regex; each CIDR gets its own, because a full-range /8 alone is already a sizeable RE2
// program and a few unioned exceed the cap. RBAC OR-combines a policy's permissions, so
// the split is semantically identical to the union egressAuthorityRegex renders.
func authorityMatchRegexes(domains, cidrs []string) []string {
	var out []string
	if d := wrapAuthority(domainAlternations(domains)); d != "" {
		out = append(out, d)
	}
	for _, alt := range cidrAlternations(cidrs) {
		out = append(out, wrapAuthority([]string{alt}))
	}
	return out
}

// nonCanonicalNumericAuthorityRegexes matches all-numeric dotted authorities that are NOT
// canonical IPv4 dotted-quads — the forms a permissive resolver expands into an address the
// canonical CIDR match cannot see: inet_aton short forms (1/2/3 parts), 5+ parts, and
// 4-quads with a leading-zero octet (#126). It deliberately does NOT match canonical
// quads (handled by the CIDR permissions), >255-octet quads, or empty-part forms (both
// fail closed at the resolver) — keeping it exactly equal to
// enforcement.IsNonCanonicalNumericAuthority (consistency test).
//
// Returned as TWO regexes (OR-combined as separate RBAC permissions, like the per-CIDR
// split) because unioning all the alternatives into one safe_regex exceeds Envoy's RE2
// max_program_size and crashloops the proxy.
func nonCanonicalNumericAuthorityRegexes() []string {
	return []string{
		wrapAuthority([]string{
			`[0-9]+(\.[0-9]+){0,2}`, // 1, 2, or 3 parts (short forms; 1-part = integer)
			`[0-9]+(\.[0-9]+){4,}`,  // 5+ parts
		}),
		wrapAuthority([]string{
			`0[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+`, // 4 parts, leading-zero octet 1
			`[0-9]+\.0[0-9]+\.[0-9]+\.[0-9]+`, // octet 2
			`[0-9]+\.[0-9]+\.0[0-9]+\.[0-9]+`, // octet 3
			`[0-9]+\.[0-9]+\.[0-9]+\.0[0-9]+`, // octet 4
		}),
	}
}

// domainAlternations renders one RE2 alternative per FQDN pattern: a quoted literal for
// exact hosts, and one-or-more-subdomain-labels + the literal suffix for "*." wildcards
// (apex excluded), mirroring enforcement.MatchDomain.
func domainAlternations(patterns []string) []string {
	var alts []string
	for _, p := range patterns {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		if suffix, ok := strings.CutPrefix(p, "*."); ok {
			if suffix == "" {
				continue
			}
			alts = append(alts, `([^.]+\.)+`+regexp.QuoteMeta(suffix))
		} else {
			alts = append(alts, regexp.QuoteMeta(p))
		}
	}
	return alts
}

// cidrAlternations renders one RE2 alternative per allowedCIDRs/deniedCIDRs pattern,
// matching exactly the canonical dotted-quad spellings of the addresses the prefix
// contains (#125). The pattern grammar is shared with the evidence matcher via
// enforcement.CIDRPatternPrefix, so both sides skip an invalid pattern identically
// (validation upstream makes that unreachable in practice).
//
// Non-canonical authority spellings of an in-range address (leading zeros, hex, single
// integer) deliberately do NOT match: they are not IPv4 literals to the matcher either,
// and Envoy's dynamic_forward_proxy treats them as DNS names — which fail resolution,
// so they fail closed rather than slipping through.
func cidrAlternations(patterns []string) []string {
	var alts []string
	for _, p := range patterns {
		prefix, ok := enforcement.CIDRPatternPrefix(p)
		if !ok {
			continue
		}
		alts = append(alts, cidrRegex(prefix))
	}
	return alts
}

// cidrRegex renders the RE2 alternative for one masked IPv4 prefix. The per-octet cross
// product is exact for a masked CIDR: octets fully above the prefix boundary are fixed,
// the boundary octet spans one contiguous range, and octets below span 0-255.
func cidrRegex(prefix netip.Prefix) string {
	lo := prefix.Masked().Addr().As4()
	hi := lo
	for i := range hi {
		hostBits := 8 - min(max(prefix.Bits()-i*8, 0), 8)
		hi[i] |= byte(1<<hostBits - 1)
	}
	parts := make([]string, len(lo))
	for i := range lo {
		parts[i] = octetRegex(int(lo[i]), int(hi[i]))
	}
	return strings.Join(parts, `\.`)
}

// octetRegex matches exactly the canonical decimal spellings of lo..hi (0 ≤ lo ≤ hi ≤ 255).
func octetRegex(lo, hi int) string {
	if lo == hi {
		return strconv.Itoa(lo)
	}
	return "(" + strings.Join(octetAlternatives(lo, hi), "|") + ")"
}

// octetAlternatives decomposes lo..hi into digit-width segments — single digits, two
// digits (no leading zero), and the 1xx/2xx hundreds — so no alternative ever admits a
// leading-zero spelling. Correctness is pinned by the #125 property test (regex match ==
// netip.Prefix.Contains across boundaries and samples).
func octetAlternatives(lo, hi int) []string {
	var alts []string
	if lo <= 9 {
		alts = append(alts, digitRange(lo, min(hi, 9)))
	}
	if lo <= 99 && hi >= 10 {
		alts = append(alts, twoDigitAlternatives(max(lo, 10), min(hi, 99))...)
	}
	for _, base := range []int{100, 200} {
		if hi >= base && lo <= base+99 {
			l := max(lo, base) - base
			h := min(hi, base+99) - base
			for _, body := range twoDigitAlternatives(l, h) {
				alts = append(alts, strconv.Itoa(base/100)+body)
			}
		}
	}
	return alts
}

// twoDigitAlternatives matches the fixed-width two-digit strings for lo..hi
// (0 ≤ lo ≤ hi ≤ 99): the shared body of standalone two-digit numbers (callers pass
// lo ≥ 10, so no leading zero) and of the last two digits of a hundreds block (where a
// zero tens digit is legitimate).
func twoDigitAlternatives(lo, hi int) []string {
	lt, ht := lo/10, hi/10
	if lt == ht {
		return []string{strconv.Itoa(lt) + digitRange(lo%10, hi%10)}
	}
	var alts []string
	if lo%10 != 0 {
		alts = append(alts, strconv.Itoa(lt)+digitRange(lo%10, 9))
		lt++
	}
	last := ""
	if hi%10 != 9 {
		last = strconv.Itoa(ht) + digitRange(0, hi%10)
		ht--
	}
	if lt <= ht {
		alts = append(alts, digitRange(lt, ht)+"[0-9]")
	}
	if last != "" {
		alts = append(alts, last)
	}
	return alts
}

// digitRange matches one decimal digit in lo..hi.
func digitRange(lo, hi int) string {
	if lo == hi {
		return strconv.Itoa(lo)
	}
	return "[" + strconv.Itoa(lo) + "-" + strconv.Itoa(hi) + "]"
}

// rbacHTTPFilters renders the http_filters RBAC block (already indented for the filter
// list) that enforces the egress policy, or "" when nothing to enforce. Deny wins over
// allow: ONE DENY filter (deniedDomains ∪ deniedCIDRs) is chained before ONE ALLOW
// filter (allowedDomains ∪ allowedCIDRs, default-deny others), so an authority on both
// sides is denied, and an authority passing EITHER allow-list is allowed (union
// semantics — a hostname can never match a CIDR entry, so under a CIDR-only allow-list
// hostname dials are default-denied).
func rbacHTTPFilters(cfg BootstrapConfig) string {
	if !cfg.hasEgressPolicy() {
		return ""
	}
	var b strings.Builder
	deny := authorityMatchRegexes(cfg.DeniedDomains, cfg.DeniedCIDRs)
	if cfg.hasCIDRPolicy() {
		// Refuse resolver-expandable numeric spellings so they cannot slip past the CIDR
		// rules (#126). Placed in the DENY filter: it wins over the allow-union, so even a
		// non-canonical form of an allow-listed address is refused (use the canonical form).
		deny = append(deny, nonCanonicalNumericAuthorityRegexes()...)
	}
	if len(deny) > 0 {
		b.WriteString(rbacFilter("scrutineer-egress-deny", "DENY", "scrutineer-denied", deny))
	}
	if res := authorityMatchRegexes(cfg.AllowedDomains, cfg.AllowedCIDRs); len(res) > 0 {
		b.WriteString(rbacFilter("scrutineer-egress-allow", "ALLOW", "scrutineer-allowed", res))
	}
	return b.String()
}

// rbacFilter renders one envoy.filters.http.rbac filter matching :authority against a set
// of regexes (OR-combined as the policy's permission list — any match satisfies the
// permission). An ALLOW filter is default-deny for non-matching requests; a DENY filter
// blocks matches and passes the rest to the next filter. Single-quoted YAML keeps regex
// backslashes literal.
func rbacFilter(filterName, action, policyName string, authorityREs []string) string {
	var perms strings.Builder
	for _, re := range authorityREs {
		perms.WriteString(`                    - header:
                        name: ":authority"
                        string_match:
                          safe_regex:
                            regex: '` + re + `'
`)
	}
	return `          - name: ` + filterName + `
            typed_config:
              "@type": type.googleapis.com/envoy.extensions.filters.http.rbac.v3.RBAC
              rules:
                action: ` + action + `
                policies:
                  ` + policyName + `:
                    permissions:
` + perms.String() + `                    principals:
                    - any: true
`
}
