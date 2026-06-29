/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package policy

import (
	"fmt"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
)

// MergeRules combines base and overlay policy rules. List fields are unioned in order;
// numeric caps take the minimum non-nil value (strictest wins). Argument rules are
// concatenated (constraints only tighten; see docs/design/phase-3-tool-argument-constraints.md).
func MergeRules(base, overlay scrutineerv1alpha1.PolicyRules) scrutineerv1alpha1.PolicyRules {
	return scrutineerv1alpha1.PolicyRules{
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
		AllowedPaths:         unionStrings(base.AllowedPaths, overlay.AllowedPaths),
		DeniedPaths:          unionStrings(base.DeniedPaths, overlay.DeniedPaths),
		MaxWorkspaceBytes:    minInt64Ptr(base.MaxWorkspaceBytes, overlay.MaxWorkspaceBytes),
		ArgumentRules:        concatArgumentRules(base.ArgumentRules, overlay.ArgumentRules),
	}
}

// concatArgumentRules appends overlay rules after base rules, dropping structurally
// identical duplicates so the same rule referenced by multiple layers appears once.
// Order is preserved (base first); argument rules can only tighten, so concatenation is
// always safe.
func concatArgumentRules(base, overlay []scrutineerv1alpha1.ToolArgumentRule) []scrutineerv1alpha1.ToolArgumentRule {
	if len(base) == 0 && len(overlay) == 0 {
		return nil
	}
	out := make([]scrutineerv1alpha1.ToolArgumentRule, 0, len(base)+len(overlay))
	seen := make(map[string]struct{}, len(base)+len(overlay))
	for _, rule := range append(append([]scrutineerv1alpha1.ToolArgumentRule(nil), base...), overlay...) {
		key := argumentRuleKey(rule)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, rule)
	}
	return out
}

// argumentRuleKey is a stable structural key for dedupe. Order within a rule is
// significant (constraints are evaluated in order), so it is preserved in the key.
func argumentRuleKey(rule scrutineerv1alpha1.ToolArgumentRule) string {
	key := "s=" + rule.Server + ";t=" + fmt.Sprintf("%q", rule.Tools)
	for _, c := range rule.Constraints {
		key += fmt.Sprintf(";c=%s|%s|%q|%s", c.Arg, c.Op, c.Values, c.Effect)
	}
	return key
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

func minInt64Ptr(a, b *int64) *int64 {
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
