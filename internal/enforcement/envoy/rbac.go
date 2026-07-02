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
	"strings"
)

// BootstrapConfig parameterizes the rendered Envoy bootstrap. AllowedDomains/DeniedDomains
// are the session's effective FQDN policy; Enforce is true only in enforced mode. In audit
// mode Enforce is false, so no RBAC is generated (Envoy forwards freely) and the
// egress-reporter records would-be-denials as dry-run evidence (#32).
type BootstrapConfig struct {
	Port           int
	Enforce        bool
	AllowedDomains []string
	DeniedDomains  []string
}

// hasFQDNPolicy reports whether enforced FQDN rules should generate RBAC.
func (c BootstrapConfig) hasFQDNPolicy() bool {
	return c.Enforce && (len(c.DeniedDomains) > 0 || len(c.AllowedDomains) > 0)
}

// authorityRegex renders an RE2 pattern (for Envoy safe_regex, which full-matches) that
// matches a request :authority against the FQDN patterns, mirroring enforcement.MatchDomain:
// exact hosts, "*." wildcards (subdomains only, apex excluded), case-insensitive, with an
// optional ":port" suffix. Returns "" when no usable pattern is present.
func authorityRegex(patterns []string) string {
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
			// one or more subdomain labels, then the literal suffix (apex excluded).
			alts = append(alts, `([^.]+\.)+`+regexp.QuoteMeta(suffix))
		} else {
			alts = append(alts, regexp.QuoteMeta(p))
		}
	}
	if len(alts) == 0 {
		return ""
	}
	return `(?i)(` + strings.Join(alts, "|") + `)(:[0-9]+)?`
}

// rbacHTTPFilters renders the http_filters RBAC block (already indented for the filter
// list) that enforces the FQDN policy, or "" when nothing to enforce. Deny wins over
// allow: a DENY filter (denied patterns) is chained before an ALLOW filter (allowed
// patterns, default-deny others), so a host on both lists is denied.
func rbacHTTPFilters(cfg BootstrapConfig) string {
	if !cfg.hasFQDNPolicy() {
		return ""
	}
	var b strings.Builder
	if re := authorityRegex(cfg.DeniedDomains); re != "" {
		b.WriteString(rbacFilter("scrutineer-egress-deny", "DENY", "scrutineer-denied", re))
	}
	if re := authorityRegex(cfg.AllowedDomains); re != "" {
		b.WriteString(rbacFilter("scrutineer-egress-allow", "ALLOW", "scrutineer-allowed", re))
	}
	return b.String()
}

// rbacFilter renders one envoy.filters.http.rbac filter matching :authority by regex.
// An ALLOW filter is default-deny for non-matching requests; a DENY filter blocks matches
// and passes the rest to the next filter. Single-quoted YAML keeps regex backslashes literal.
func rbacFilter(filterName, action, policyName, authorityRE string) string {
	return `          - name: ` + filterName + `
            typed_config:
              "@type": type.googleapis.com/envoy.extensions.filters.http.rbac.v3.RBAC
              rules:
                action: ` + action + `
                policies:
                  ` + policyName + `:
                    permissions:
                    - header:
                        name: ":authority"
                        string_match:
                          safe_regex:
                            regex: '` + authorityRE + `'
                    principals:
                    - any: true
`
}
