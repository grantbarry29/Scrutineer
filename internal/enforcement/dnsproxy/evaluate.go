/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package dnsproxy

import (
	"net"
	"strings"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
	"github.com/secureai/relay/internal/enforcement"
)

const (
	ReasonAllowed             = "Allowed"
	ReasonDeniedDomains       = "DeniedDomains"
	ReasonNotInAllowedDomains = "NotInAllowedDomains"
	ReasonDeniedCIDRs         = "DeniedCIDRs"
	ReasonNotInAllowedCIDRs   = "NotInAllowedCIDRs"
	ReasonEmptyHost           = "EmptyHost"
)

// HasEgressPolicy reports whether policy contains domain or CIDR hints this backend can enforce.
func HasEgressPolicy(rules relayv1alpha1.PolicyRules) bool {
	return len(rules.AllowedDomains) > 0 ||
		len(rules.DeniedDomains) > 0 ||
		len(rules.AllowedCIDRs) > 0 ||
		len(rules.DeniedCIDRs) > 0
}

// HasEnabledSidecar reports whether the session context includes an enabled dns-proxy sidecar.
func HasEnabledSidecar(ctx enforcement.SessionContext) bool {
	for _, s := range ctx.Sidecars {
		if s.Type != SidecarType {
			continue
		}
		if s.Enabled == nil || *s.Enabled {
			return true
		}
	}
	return false
}

// Applicable reports whether egress proxy config should be produced.
func Applicable(ctx enforcement.SessionContext) bool {
	return HasEgressPolicy(ctx.Policy) || HasEnabledSidecar(ctx)
}

// EvaluateEgress applies effective network policy and mode semantics to an egress request.
func EvaluateEgress(ctx enforcement.SessionContext, req EgressRequest) EgressAuthorization {
	host := strings.TrimSpace(strings.ToLower(req.Host))
	if host == "" {
		return EgressAuthorization{
			Evaluation: enforcement.Evaluation{
				Allowed: false,
				Action:  relayv1alpha1.PolicyDecisionDeny,
				Blocked: ctx.Mode == relayv1alpha1.PolicyModeEnforced,
			},
			Reason: ReasonEmptyHost,
		}
	}

	if ip := net.ParseIP(host); ip != nil {
		return evaluateIP(ctx, ip)
	}
	return evaluateDomain(ctx, host)
}

func evaluateDomain(ctx enforcement.SessionContext, domain string) EgressAuthorization {
	rules := ctx.Policy
	if containsStringFold(rules.DeniedDomains, domain) {
		return authorize(ctx.Mode, true, ReasonDeniedDomains)
	}
	if len(rules.AllowedDomains) > 0 && !containsStringFold(rules.AllowedDomains, domain) {
		return authorize(ctx.Mode, true, ReasonNotInAllowedDomains)
	}
	return allowed()
}

func evaluateIP(ctx enforcement.SessionContext, ip net.IP) EgressAuthorization {
	rules := ctx.Policy
	if matchesAnyCIDR(ip, rules.DeniedCIDRs) {
		return authorize(ctx.Mode, true, ReasonDeniedCIDRs)
	}
	if len(rules.AllowedCIDRs) > 0 && !matchesAnyCIDR(ip, rules.AllowedCIDRs) {
		return authorize(ctx.Mode, true, ReasonNotInAllowedCIDRs)
	}
	return allowed()
}

func authorize(mode relayv1alpha1.PolicyMode, ruleWouldDeny bool, reason string) EgressAuthorization {
	return EgressAuthorization{
		Evaluation: enforcement.EvaluateRestrictive(mode, ruleWouldDeny),
		Reason:     reason,
	}
}

func allowed() EgressAuthorization {
	return EgressAuthorization{
		Evaluation: enforcement.Evaluation{
			Allowed: true,
			Action:  relayv1alpha1.PolicyDecisionAllow,
		},
		Reason: ReasonAllowed,
	}
}

func containsStringFold(list []string, value string) bool {
	for _, item := range list {
		if strings.EqualFold(strings.TrimSpace(item), value) {
			return true
		}
	}
	return false
}

func matchesAnyCIDR(ip net.IP, cidrs []string) bool {
	for _, cidr := range cidrs {
		cidr = normalizeCIDR(strings.TrimSpace(cidr))
		if cidr == "" {
			continue
		}
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func normalizeCIDR(cidr string) string {
	if cidr == "" {
		return cidr
	}
	if strings.Contains(cidr, "/") {
		return cidr
	}
	if strings.Contains(cidr, ":") {
		return cidr + "/128"
	}
	return cidr + "/32"
}
