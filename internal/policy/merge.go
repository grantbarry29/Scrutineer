/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package policy

import (
	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
)

// MergeRules combines base and overlay policy rules. List fields are unioned in order;
// numeric caps take the minimum non-nil value (strictest wins).
func MergeRules(base, overlay relayv1alpha1.PolicyRules) relayv1alpha1.PolicyRules {
	return relayv1alpha1.PolicyRules{
		AllowedDomains:       unionStrings(base.AllowedDomains, overlay.AllowedDomains),
		DeniedDomains:        unionStrings(base.DeniedDomains, overlay.DeniedDomains),
		AllowedCIDRs:         unionStrings(base.AllowedCIDRs, overlay.AllowedCIDRs),
		DeniedCIDRs:          unionStrings(base.DeniedCIDRs, overlay.DeniedCIDRs),
		AllowedTools:         unionStrings(base.AllowedTools, overlay.AllowedTools),
		DeniedTools:          unionStrings(base.DeniedTools, overlay.DeniedTools),
		RequireHumanApproval: unionStrings(base.RequireHumanApproval, overlay.RequireHumanApproval),
		MaxNetworkRequests:   minInt32Ptr(base.MaxNetworkRequests, overlay.MaxNetworkRequests),
		MaxToolCalls:         minInt32Ptr(base.MaxToolCalls, overlay.MaxToolCalls),
		MaxCallsPerMinute:    minInt32Ptr(base.MaxCallsPerMinute, overlay.MaxCallsPerMinute),
	}
}

// StrictestMode returns the most restrictive mode across inputs (enforced > dry-run > audit-only).
func StrictestMode(modes ...relayv1alpha1.PolicyMode) relayv1alpha1.PolicyMode {
	best := relayv1alpha1.PolicyModeAuditOnly
	for _, m := range modes {
		if modeRank(m) > modeRank(best) {
			best = m
		}
	}
	return best
}

// NormalizeMode returns audit-only when mode is empty.
func NormalizeMode(m relayv1alpha1.PolicyMode) relayv1alpha1.PolicyMode {
	if m == "" {
		return relayv1alpha1.PolicyModeAuditOnly
	}
	return m
}

func modeRank(m relayv1alpha1.PolicyMode) int {
	switch m {
	case relayv1alpha1.PolicyModeEnforced:
		return 3
	case relayv1alpha1.PolicyModeDryRun:
		return 2
	default:
		return 1
	}
}

func unionStrings(a, b []string) []string {
	if len(a) == 0 {
		return append([]string(nil), b...)
	}
	if len(b) == 0 {
		return append([]string(nil), a...)
	}
	seen := make(map[string]struct{}, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, s := range append(a, b...) {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func minInt32Ptr(a, b *int32) *int32 {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	default:
		v := *a
		if *b < v {
			v = *b
		}
		return &v
	}
}
