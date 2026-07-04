/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package policy

import (
	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
)

// MergeRules combines base and overlay policy rules. List fields are unioned in order.
func MergeRules(base, overlay scrutineerv1alpha1.PolicyRules) scrutineerv1alpha1.PolicyRules {
	return scrutineerv1alpha1.PolicyRules{
		AllowedDomains:       unionStrings(base.AllowedDomains, overlay.AllowedDomains),
		DeniedDomains:        unionStrings(base.DeniedDomains, overlay.DeniedDomains),
		AllowedCIDRs:         unionStrings(base.AllowedCIDRs, overlay.AllowedCIDRs),
		DeniedCIDRs:          unionStrings(base.DeniedCIDRs, overlay.DeniedCIDRs),
		RequireHumanApproval: unionStrings(base.RequireHumanApproval, overlay.RequireHumanApproval),
	}
}

// StrictestMode returns the most restrictive mode across inputs (enforced > dry-run > audit-only).
func StrictestMode(modes ...scrutineerv1alpha1.PolicyMode) scrutineerv1alpha1.PolicyMode {
	best := scrutineerv1alpha1.PolicyModeAuditOnly
	for _, m := range modes {
		if modeRank(m) > modeRank(best) {
			best = m
		}
	}
	return best
}

// NormalizeMode returns audit-only when mode is empty.
func NormalizeMode(m scrutineerv1alpha1.PolicyMode) scrutineerv1alpha1.PolicyMode {
	if m == "" {
		return scrutineerv1alpha1.PolicyModeAuditOnly
	}
	return m
}

func modeRank(m scrutineerv1alpha1.PolicyMode) int {
	switch m {
	case scrutineerv1alpha1.PolicyModeEnforced:
		return 3
	case scrutineerv1alpha1.PolicyModeDryRun:
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
	// Iterate a and b separately rather than `range append(a, b...)`: that append
	// can write b's elements into a's backing array when a has spare capacity,
	// mutating a caller-owned (potentially CRD-cache-owned) slice.
	add := func(values []string) {
		for _, s := range values {
			if _, ok := seen[s]; ok {
				continue
			}
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	add(a)
	add(b)
	return out
}
