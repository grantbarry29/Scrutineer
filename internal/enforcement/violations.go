/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package enforcement

import (
	"fmt"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
)

// MaxViolations caps status.violations entries per AgentSession.
const MaxViolations = 64

// ShouldRecordViolationAction reports whether a runtime policy decision action
// should surface as a status.violations entry.
func ShouldRecordViolationAction(action scrutineerv1alpha1.PolicyDecisionAction) bool {
	switch action {
	case scrutineerv1alpha1.PolicyDecisionDeny, scrutineerv1alpha1.PolicyDecisionDryRun:
		return true
	default:
		return false
	}
}

// ViolationFromDecision maps a runtime policy decision to a violation when applicable.
func ViolationFromDecision(d scrutineerv1alpha1.PolicyDecision) (scrutineerv1alpha1.PolicyViolation, bool) {
	if !ShouldRecordViolationAction(d.Action) {
		return scrutineerv1alpha1.PolicyViolation{}, false
	}
	msg := d.Message
	if msg == "" {
		if d.Action == scrutineerv1alpha1.PolicyDecisionDryRun {
			msg = "would deny policy check"
		} else {
			msg = "policy denied"
		}
		if d.Target != "" {
			msg += ": " + d.Target
		}
	}
	return scrutineerv1alpha1.PolicyViolation{
		Time:    d.Time,
		Type:    d.Type,
		Message: msg,
		Target:  d.Target,
	}, true
}

// ViolationsFromDecisions derives violations from runtime policy decisions.
func ViolationsFromDecisions(decisions []scrutineerv1alpha1.PolicyDecision) []scrutineerv1alpha1.PolicyViolation {
	if len(decisions) == 0 {
		return nil
	}
	out := make([]scrutineerv1alpha1.PolicyViolation, 0, len(decisions))
	for _, d := range decisions {
		if v, ok := ViolationFromDecision(d); ok {
			out = append(out, v)
		}
	}
	return out
}

// AppendViolations appends incoming violations without exceeding maxTotal.
// When truncated, a summary violation is appended if room allows.
func AppendViolations(existing, incoming []scrutineerv1alpha1.PolicyViolation, maxTotal int) []scrutineerv1alpha1.PolicyViolation {
	if maxTotal <= 0 || len(incoming) == 0 {
		return existing
	}

	out := append([]scrutineerv1alpha1.PolicyViolation(nil), existing...)
	for _, v := range incoming {
		out = append(out, v)
	}

	if len(out) <= maxTotal {
		return out
	}

	omitted := len(out) - maxTotal
	if maxTotal <= 1 {
		return out[:maxTotal]
	}

	out = out[:maxTotal-1]
	last := incoming[len(incoming)-1]
	out = append(out, scrutineerv1alpha1.PolicyViolation{
		Time:    last.Time,
		Type:    "summary",
		Message: formatViolationTruncationMessage(omitted, maxTotal),
	})
	return out
}

func formatViolationTruncationMessage(omitted, maxTotal int) string {
	return fmt.Sprintf("violations truncated: omitted %d entries (max %d)", omitted, maxTotal)
}
