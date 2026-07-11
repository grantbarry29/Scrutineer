/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package envoy

import (
	"encoding/binary"
	"net/netip"
	"regexp"
	"strings"
	"testing"
)

// compileAuthorityRegex anchors a rendered authority regex the way Envoy safe_regex does
// (full match).
func compileAuthorityRegex(t *testing.T, re string) *regexp.Regexp {
	t.Helper()
	if re == "" {
		t.Fatal("expected a non-empty authority regex")
	}
	rx, err := regexp.Compile(`\A(?:` + re + `)\z`)
	if err != nil {
		t.Fatalf("compile %q: %v", re, err)
	}
	return rx
}

func addrFromUint32(v uint32) netip.Addr {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	return netip.AddrFrom4(b)
}

func addrToUint32(a netip.Addr) uint32 {
	b := a.As4()
	return binary.BigEndian.Uint32(b[:])
}

// #125 property: for every prefix width we enforce, the generated RE2 must agree with
// netip.Prefix.Contains on the range boundaries (network, broadcast, one outside each
// end) and on sampled inside/outside addresses — with and without a :port suffix.
func TestCIDRAuthorityRegex_matchesPrefixContains(t *testing.T) {
	for _, cidr := range []string{
		"10.0.0.0/8",
		"192.168.0.0/16",
		"203.0.113.0/24",
		"198.51.100.32/27",
		"203.0.113.5/32",
	} {
		prefix := netip.MustParsePrefix(cidr)
		rx := compileAuthorityRegex(t, egressAuthorityRegex(nil, []string{cidr}))

		first := addrToUint32(prefix.Masked().Addr())
		size := uint64(1) << (32 - prefix.Bits())
		last := first + uint32(size-1)

		candidates := []uint32{first, last}
		if first > 0 {
			candidates = append(candidates, first-1)
		}
		if last < 0xffffffff {
			candidates = append(candidates, last+1)
		}
		step := uint32(size / 16)
		if step == 0 {
			step = 1
		}
		for v := uint64(first); v <= uint64(last); v += uint64(step) {
			candidates = append(candidates, uint32(v))
		}
		// Fixed far-away probes on both sides of the numeric space.
		for _, s := range []string{"0.0.0.0", "8.8.8.8", "172.20.1.2", "255.255.255.255"} {
			candidates = append(candidates, addrToUint32(netip.MustParseAddr(s)))
		}

		for _, v := range candidates {
			addr := addrFromUint32(v)
			want := prefix.Contains(addr)
			for _, host := range []string{addr.String(), addr.String() + ":5432"} {
				if got := rx.MatchString(host); got != want {
					t.Errorf("cidr %s host %q: regex=%v, Prefix.Contains=%v", cidr, host, got, want)
				}
			}
		}
	}
}

// The union regex must match hostname patterns and CIDR patterns alike, keep the FQDN
// tolerances (port, trailing dot), and reject the classic evasions.
func TestEgressAuthorityRegex_union(t *testing.T) {
	rx := compileAuthorityRegex(t, egressAuthorityRegex(
		[]string{"good.example", "*.api.example"},
		[]string{"10.0.0.0/8", "203.0.113.5"},
	))
	for _, host := range []string{
		"good.example", "good.example:443",
		"x.api.example", "a.b.api.example:8443",
		"10.2.3.4", "10.2.3.4:15001", "10.2.3.4.", // trailing-dot authority
		"203.0.113.5", "203.0.113.5:5432",
	} {
		if !rx.MatchString(host) {
			t.Errorf("union regex must match %q", host)
		}
	}
	for _, host := range []string{
		"bad.example", "api.example", // apex excluded by the wildcard
		"11.0.0.1", "9.255.255.255", "203.0.113.6",
		"10.2.3.4.evil.com", // IP-prefixed hostname
		"010.2.3.4", "0x0a.2.3.4", "167772161", // non-canonical IP forms
	} {
		if rx.MatchString(host) {
			t.Errorf("union regex must NOT match %q", host)
		}
	}
}

// CIDR-only policies generate RBAC too (the gate is any enforced egress rule, not just
// FQDNs), domains and CIDRs merge into ONE deny + ONE allow filter (deny first), and
// audit mode still renders none.
func TestRBACHTTPFilters_cidrs(t *testing.T) {
	cidrOnly := BootstrapConfig{Enforce: true, DeniedCIDRs: []string{"10.0.0.0/8"}}
	out := rbacHTTPFilters(cidrOnly)
	if strings.Count(out, "- name: scrutineer-egress-deny") != 1 {
		t.Fatalf("CIDR-only denied policy must render exactly one deny filter:\n%s", out)
	}
	if strings.Contains(out, "scrutineer-egress-allow") {
		t.Fatalf("deny-only policy must not render an allow filter:\n%s", out)
	}

	union := BootstrapConfig{
		Enforce:        true,
		DeniedDomains:  []string{"evil.example"},
		DeniedCIDRs:    []string{"169.254.0.0/16"},
		AllowedDomains: []string{"good.example"},
		AllowedCIDRs:   []string{"203.0.113.0/24"},
	}
	out = rbacHTTPFilters(union)
	if strings.Count(out, "- name: scrutineer-egress-deny") != 1 ||
		strings.Count(out, "- name: scrutineer-egress-allow") != 1 {
		t.Fatalf("union policy must render exactly one deny and one allow filter:\n%s", out)
	}
	if strings.Index(out, "scrutineer-egress-deny") > strings.Index(out, "scrutineer-egress-allow") {
		t.Fatalf("deny filter must precede the allow filter (deny wins):\n%s", out)
	}

	audit := union
	audit.Enforce = false
	if got := rbacHTTPFilters(audit); got != "" {
		t.Fatalf("audit mode must render no RBAC, got:\n%s", got)
	}
}

// #125 regression: a full-range /8 is already a sizeable RE2 program and several unioned
// into ONE safe_regex exceed Envoy's max_program_size (default 100) — which rejects the
// bootstrap and crashloops the proxy. Each CIDR must render as its own RBAC permission
// (OR-combined) so no single regex approaches the cap, regardless of list length.
func TestRBACHTTPFilters_cidrSplitPerPermission(t *testing.T) {
	cfg := BootstrapConfig{
		Enforce:       true,
		DeniedDomains: []string{"evil.example"},
		DeniedCIDRs:   []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"},
	}
	out := rbacHTTPFilters(cfg)

	// One deny filter, holding one permission for the domains plus one per CIDR (4 total).
	if got := strings.Count(out, "- name: scrutineer-egress-deny"); got != 1 {
		t.Fatalf("want exactly one deny filter, got %d:\n%s", got, out)
	}
	if got := strings.Count(out, `name: ":authority"`); got != 4 {
		t.Fatalf("want 4 authority permissions (1 domains + 3 CIDRs), got %d:\n%s", got, out)
	}

	// No single rendered regex may union multiple CIDRs (that is what overflows the RE2
	// program cap). Each safe_regex line carries at most one CIDR's leading octet anchor.
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "regex:") {
			continue
		}
		if n := strings.Count(line, `\.(`) + strings.Count(line, `\.[`); n > 4 {
			t.Fatalf("a single safe_regex unions too many octet ranges (%d), risking the RE2 cap:\n%s", n, line)
		}
	}
}

// #125: live edits to the CIDR lists must change Hash(), or the controller's config-hash
// drift detection would never re-render the bootstrap / recreate the proxy pod.
func TestBootstrapConfigHash_coversCIDRs(t *testing.T) {
	base := BootstrapConfig{Enforce: true, AllowedDomains: []string{"good.example"}}
	withAllowed := base
	withAllowed.AllowedCIDRs = []string{"10.0.0.0/8"}
	withDenied := base
	withDenied.DeniedCIDRs = []string{"10.0.0.0/8"}

	if base.Hash() == withAllowed.Hash() {
		t.Fatal("adding allowedCIDRs must change the hash")
	}
	if base.Hash() == withDenied.Hash() {
		t.Fatal("adding deniedCIDRs must change the hash")
	}
	if withAllowed.Hash() == withDenied.Hash() {
		t.Fatal("the same list on the allow vs deny side must hash differently")
	}

	grown := withAllowed
	grown.AllowedCIDRs = []string{"10.0.0.0/8", "192.168.0.0/16"}
	if withAllowed.Hash() == grown.Hash() {
		t.Fatal("growing a CIDR list must change the hash")
	}

	reordered := grown
	reordered.AllowedCIDRs = []string{"192.168.0.0/16", "10.0.0.0/8"}
	if grown.Hash() != reordered.Hash() {
		t.Fatal("hash must be order-insensitive, matching the domain lists")
	}
}

// The CIDR lists round-trip through the egress-reporter env exactly like the domain
// lists, so evidence classification always sees the same policy the RBAC enforces.
func TestPolicyEnvRoundTrip_cidrs(t *testing.T) {
	cfg := BootstrapConfig{
		Enforce:        true,
		AllowedDomains: []string{"good.example"},
		DeniedDomains:  []string{"evil.example"},
		AllowedCIDRs:   []string{"203.0.113.0/24", "10.0.0.0/8"},
		DeniedCIDRs:    []string{"169.254.0.0/16"},
	}
	for _, ev := range policyEnv(cfg) {
		t.Setenv(ev.Name, ev.Value)
	}
	p := PolicyFromEnv()
	if !p.Enforce {
		t.Fatal("Enforce must round-trip")
	}
	assertList := func(name string, got, want []string) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s = %v, want %v", name, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s = %v, want %v", name, got, want)
			}
		}
	}
	assertList("AllowedDomains", p.AllowedDomains, cfg.AllowedDomains)
	assertList("DeniedDomains", p.DeniedDomains, cfg.DeniedDomains)
	assertList("AllowedCIDRs", p.AllowedCIDRs, cfg.AllowedCIDRs)
	assertList("DeniedCIDRs", p.DeniedCIDRs, cfg.DeniedCIDRs)
}
