/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package enforcement

import (
	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
)

// MaxPolicyDecisions is the total cap for status.policyDecisions (merge + runtime).
// Matches policy.MaxMergePolicyDecisions until a dedicated runtime budget is introduced.
const MaxPolicyDecisions = 64

// AppendRuntimeDecisions appends runtime-phase decisions to existing merge-time decisions
// without exceeding maxTotal. When truncated, a summary decision is appended if room allows.
// Merge-time entries are always preserved; incoming runtime entries may be dropped from the tail.
func AppendRuntimeDecisions(existing, incoming []relayv1alpha1.PolicyDecision, maxTotal int) []relayv1alpha1.PolicyDecision {
	if maxTotal <= 0 || len(incoming) == 0 {
		return existing
	}

	out := append([]relayv1alpha1.PolicyDecision(nil), existing...)
	for _, d := range incoming {
		if d.Phase != relayv1alpha1.PolicyDecisionPhaseRuntime {
			d.Phase = relayv1alpha1.PolicyDecisionPhaseRuntime
		}
		out = append(out, d)
	}

	if len(out) <= maxTotal {
		return out
	}

	omitted := len(out) - maxTotal
	if maxTotal <= 1 {
		return out[:maxTotal]
	}

	out = out[:maxTotal-1]
	out = append(out, relayv1alpha1.PolicyDecision{
		Time:    incoming[len(incoming)-1].Time,
		Phase:   relayv1alpha1.PolicyDecisionPhaseRuntime,
		Type:    "summary",
		Action:  relayv1alpha1.PolicyDecisionAudit,
		Actor:   "relay-enforcement",
		Reason:  "DecisionsTruncated",
		Message: formatTruncationMessage(omitted, maxTotal),
	})
	return out
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
