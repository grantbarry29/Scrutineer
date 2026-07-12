/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package envoy

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// mustCompileRE2 compiles re anchored for a full-string match, mirroring how Envoy's
// safe_regex evaluates :authority (the entire value must match).
func mustCompileRE2(t *testing.T, re string) *regexp.Regexp {
	t.Helper()
	rx, err := regexp.Compile(`\A(?:` + re + `)\z`)
	if err != nil {
		t.Fatalf("compile %q: %v", re, err)
	}
	return rx
}

// Structural checks on the generated bootstrap. Semantic validity is verified out-of-band
// with `envoy --mode validate` (Envoy 1.31) and, end to end, by the Slice A e2e (#60).
func TestBootstrapYAML(t *testing.T) {
	cfg := BootstrapYAML(BootstrapConfig{Port: ProxyPort})

	must := []string{
		"port_value: 15001",                        // listens on the proxy port
		"connect_matcher",                          // terminates HTTPS CONNECT
		"upgrade_type: CONNECT",                    // CONNECT upgrade enabled
		"envoy.filters.http.dynamic_forward_proxy", // resolves + forwards by name
		"envoy.clusters.dynamic_forward_proxy",     // dynamic forward proxy cluster
		"envoy.filters.http.router",                // router terminal filter
		"address: 127.0.0.1",                       // admin bound to loopback only
		"envoy.access_loggers.stdout",              // stdout access log (traversal evidence)
		"scrutineer-egress %REQ(:METHOD)%",         // rendered format has single %, not %%
		"%REQ(:AUTHORITY)%",                        // logs the requested host / CONNECT target
	}
	for _, s := range must {
		if !strings.Contains(cfg, s) {
			t.Fatalf("BootstrapYAML missing %q", s)
		}
	}

	// The listen port is parameterized.
	if !strings.Contains(BootstrapYAML(BootstrapConfig{Port: 19000}), "port_value: 19000") {
		t.Fatalf("BootstrapYAML did not honor the port argument")
	}

	// With no policy (or in audit mode) there is no RBAC filter — Envoy forwards freely
	// and the egress-reporter records evidence (dry-run classification happens there).
	if strings.Contains(cfg, "filters.http.rbac.v3.RBAC") {
		t.Fatalf("BootstrapYAML must not emit RBAC without an enforced policy")
	}
}

// The stats listener (#55) exposes ONLY /stats/prometheus by routing that single path to
// the loopback admin cluster — the admin API itself (config dump, quitquitquit, …) must
// never be reachable from off-pod.
func TestBootstrapYAML_statsListener(t *testing.T) {
	cfg := BootstrapYAML(BootstrapConfig{Port: ProxyPort})

	must := []string{
		fmt.Sprintf("port_value: %d", StatsPort), // scrape endpoint bound on the pod IP
		`path: "/stats/prometheus"`,              // exact-path route, nothing else
		"cluster: envoy_admin",                   // routed to the loopback admin
	}
	for _, s := range must {
		if !strings.Contains(cfg, s) {
			t.Fatalf("BootstrapYAML missing %q", s)
		}
	}
	// Admin stays loopback-bound; the stats listener must not widen it.
	if !strings.Contains(cfg, "address: 127.0.0.1") {
		t.Fatal("admin must remain bound to loopback")
	}
	// No prefix route to the admin cluster — only the exact /stats/prometheus path.
	if strings.Contains(cfg, `prefix: "/stats`) {
		t.Fatal("stats route must be exact-path, not prefix (admin surface leak)")
	}
}

func TestBootstrapYAML_deniedDomainsRBAC(t *testing.T) {
	cfg := BootstrapYAML(BootstrapConfig{
		Port:          ProxyPort,
		Enforce:       true,
		DeniedDomains: []string{"evil.example", "*.tracker.example"},
	})
	if !strings.Contains(cfg, "filters.http.rbac.v3.RBAC") {
		t.Fatalf("expected an RBAC filter for denied domains")
	}
	if !strings.Contains(cfg, "action: DENY") {
		t.Fatalf("denied domains must produce a DENY rule")
	}
	// The RBAC must run before the forward proxy so a blocked request never egresses.
	rbacAt := strings.Index(cfg, "filters.http.rbac.v3.RBAC")
	fwdAt := strings.Index(cfg, "envoy.filters.http.dynamic_forward_proxy")
	if rbacAt < 0 || fwdAt < 0 || rbacAt > fwdAt {
		t.Fatalf("RBAC filter must precede the dynamic_forward_proxy filter (rbac=%d fwd=%d)", rbacAt, fwdAt)
	}
	// Matches on :authority via a regex derived from the patterns.
	if !strings.Contains(cfg, `name: ":authority"`) {
		t.Fatalf("RBAC must match the :authority header")
	}
	if !strings.Contains(cfg, `evil\.example`) {
		t.Fatalf("denied exact pattern must appear escaped in the regex; cfg:\n%s", cfg)
	}
	if !strings.Contains(cfg, `tracker\.example`) {
		t.Fatalf("denied wildcard suffix must appear in the regex")
	}
}

func TestBootstrapYAML_allowedDomainsDefaultDeny(t *testing.T) {
	cfg := BootstrapYAML(BootstrapConfig{
		Port:           ProxyPort,
		Enforce:        true,
		AllowedDomains: []string{"api.github.com"},
	})
	if !strings.Contains(cfg, "action: ALLOW") {
		t.Fatalf("allow-list must produce an ALLOW rule (default-deny others)")
	}
	if strings.Contains(cfg, "action: DENY") {
		t.Fatalf("allow-list only must not emit a DENY rule")
	}
	if !strings.Contains(cfg, `api\.github\.com`) {
		t.Fatalf("allowed pattern must appear escaped in the regex")
	}
}

// Deny wins over allow: both lists ⇒ a DENY filter chained before an ALLOW filter.
func TestBootstrapYAML_denyBeforeAllow(t *testing.T) {
	cfg := BootstrapYAML(BootstrapConfig{
		Port:           ProxyPort,
		Enforce:        true,
		AllowedDomains: []string{"*.github.com"},
		DeniedDomains:  []string{"gist.github.com"},
	})
	denyAt := strings.Index(cfg, "action: DENY")
	allowAt := strings.Index(cfg, "action: ALLOW")
	if denyAt < 0 || allowAt < 0 || denyAt > allowAt {
		t.Fatalf("DENY filter must precede ALLOW filter (deny=%d allow=%d)", denyAt, allowAt)
	}
}

// Audit mode never enforces at Envoy even when domains are set (the reporter records
// would-be-denials as dry-run instead).
func TestBootstrapYAML_auditModeNoRBAC(t *testing.T) {
	cfg := BootstrapYAML(BootstrapConfig{
		Port:          ProxyPort,
		Enforce:       false,
		DeniedDomains: []string{"evil.example"},
	})
	if strings.Contains(cfg, "filters.http.rbac.v3.RBAC") {
		t.Fatalf("audit mode must not emit RBAC")
	}
}

func TestAuthorityRegex(t *testing.T) {
	cases := []struct {
		patterns  []string
		match     []string
		notMatch  []string
		wantEmpty bool
	}{
		{
			patterns: []string{"example.com"},
			match:    []string{"example.com", "example.com:443", "EXAMPLE.com"},
			notMatch: []string{"api.example.com", "notexample.com", "example.com.evil.com"},
		},
		{
			patterns: []string{"*.example.com"},
			match:    []string{"api.example.com", "a.b.example.com", "api.example.com:8443"},
			notMatch: []string{"example.com", "example.com.evil.com"},
		},
		{
			// #123: the CONNECT-tunnel escape hatch reaches non-HTTP TCP services
			// (databases, SSH, custom TCP) on arbitrary ports. A CONNECT authority is
			// always host:port, so the FQDN RBAC must police these by host,
			// port-insensitively — an allow-listed host matches on its service port,
			// while an unlisted host on the same port must not slip through.
			patterns: []string{"db.internal"},
			match:    []string{"db.internal", "db.internal:5432", "db.internal:22"},
			notMatch: []string{"evil.internal:5432", "notdb.internal:5432", "db.internal.evil.com:5432"},
		},
		{patterns: nil, wantEmpty: true},
		{patterns: []string{"", "   "}, wantEmpty: true},
	}
	for _, tc := range cases {
		re := authorityRegex(tc.patterns)
		if tc.wantEmpty {
			if re != "" {
				t.Fatalf("authorityRegex(%v) = %q, want empty", tc.patterns, re)
			}
			continue
		}
		rx := mustCompileRE2(t, re)
		for _, m := range tc.match {
			if !rx.MatchString(m) {
				t.Errorf("regex %q should match %q", re, m)
			}
		}
		for _, n := range tc.notMatch {
			if rx.MatchString(n) {
				t.Errorf("regex %q should NOT match %q", re, n)
			}
		}
	}
}
