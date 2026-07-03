/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package enforcement

import (
	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
)

// MaxPolicyDecisions is the total cap for status.policyDecisions (merge + runtime).
// Matches policy.MaxMergePolicyDecisions until a dedicated runtime budget is introduced.
const MaxPolicyDecisions = 64

const (
	decisionSummaryType   = "summary"
	decisionSummaryReason = "DecisionsTruncated"
	decisionSummaryActor  = "scrutineer-enforcement"
)

// AppendRuntimeDecisions merges incoming runtime-phase decisions into existing decisions
// without exceeding maxTotal, choosing what to keep by value rather than arrival order (#67):
//
//   - Merge-time decisions (phase != runtime) are authoritative and kept first.
//   - Among runtime decisions, non-allow records (deny / dry-run / audit) are kept before
//     allow records, and the most recent are kept within each class. This stops a burst of
//     observed `allow` egress (recorded per request since #62) from evicting the higher-
//     value `deny`/`dry-run` evidence the routing lock + FQDN policy produce.
//   - On truncation a single `DecisionsTruncated` summary is always appended; any stale
//     summary from a prior pass is dropped so summaries never accumulate or get re-counted.
//
// Kept runtime decisions preserve chronological (input) order. Returns existing unchanged
// when there is nothing to add.
func AppendRuntimeDecisions(existing, incoming []scrutineerv1alpha1.PolicyDecision, maxTotal int) []scrutineerv1alpha1.PolicyDecision {
	if maxTotal <= 0 || len(incoming) == 0 {
		return existing
	}

	// Partition existing into authoritative merge-time entries and prior runtime entries,
	// dropping any stale truncation summary.
	var merge, runtimeAll []scrutineerv1alpha1.PolicyDecision
	for _, d := range existing {
		switch {
		case d.Phase != scrutineerv1alpha1.PolicyDecisionPhaseRuntime:
			merge = append(merge, d)
		case isTruncationSummary(d):
			// drop: a fresh summary is re-derived below if still needed.
		default:
			runtimeAll = append(runtimeAll, d)
		}
	}
	for _, d := range incoming {
		d.Phase = scrutineerv1alpha1.PolicyDecisionPhaseRuntime
		runtimeAll = append(runtimeAll, d)
	}

	if len(merge)+len(runtimeAll) <= maxTotal {
		return append(append(make([]scrutineerv1alpha1.PolicyDecision, 0, len(merge)+len(runtimeAll)), merge...), runtimeAll...)
	}

	// Truncation: always reserve one slot for the summary.
	mergeBudget := len(merge)
	if mergeBudget > maxTotal-1 {
		mergeBudget = maxTotal - 1
	}
	runtimeBudget := maxTotal - 1 - mergeBudget
	keptRuntime := selectRuntimeToKeep(runtimeAll, runtimeBudget)

	omitted := (len(merge) + len(runtimeAll)) - (mergeBudget + len(keptRuntime))
	out := make([]scrutineerv1alpha1.PolicyDecision, 0, mergeBudget+len(keptRuntime)+1)
	out = append(out, merge[:mergeBudget]...)
	out = append(out, keptRuntime...)
	out = append(out, scrutineerv1alpha1.PolicyDecision{
		Time:    incoming[len(incoming)-1].Time,
		Phase:   scrutineerv1alpha1.PolicyDecisionPhaseRuntime,
		Type:    decisionSummaryType,
		Action:  scrutineerv1alpha1.PolicyDecisionAudit,
		Actor:   decisionSummaryActor,
		Reason:  decisionSummaryReason,
		Message: formatTruncationMessage(omitted, maxTotal),
	})
	return out
}

// selectRuntimeToKeep returns up to budget runtime decisions, preferring non-allow records
// and, within each class, the most recent — while preserving chronological (input) order.
func selectRuntimeToKeep(runtime []scrutineerv1alpha1.PolicyDecision, budget int) []scrutineerv1alpha1.PolicyDecision {
	if budget <= 0 {
		return nil
	}
	if len(runtime) <= budget {
		return runtime
	}
	keep := make([]bool, len(runtime))
	remaining := budget
	// Pass 1: non-allow, most-recent first.
	for i := len(runtime) - 1; i >= 0 && remaining > 0; i-- {
		if runtime[i].Action != scrutineerv1alpha1.PolicyDecisionAllow {
			keep[i] = true
			remaining--
		}
	}
	// Pass 2: fill remaining budget with allow records, most-recent first.
	for i := len(runtime) - 1; i >= 0 && remaining > 0; i-- {
		if !keep[i] && runtime[i].Action == scrutineerv1alpha1.PolicyDecisionAllow {
			keep[i] = true
			remaining--
		}
	}
	out := make([]scrutineerv1alpha1.PolicyDecision, 0, budget)
	for i := range runtime {
		if keep[i] {
			out = append(out, runtime[i])
		}
	}
	return out
}

func isTruncationSummary(d scrutineerv1alpha1.PolicyDecision) bool {
	return d.Type == decisionSummaryType && d.Reason == decisionSummaryReason
}

func formatTruncationMessage(omitted, maxTotal int) string {
	return "runtime policy decisions truncated: omitted " + itoa(omitted) + " entries (max " + itoa(maxTotal) + ")"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
