/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package agentsession

import (
	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
	"github.com/secureai/relay/internal/enforcement"
)

// ApplyUsageFromReport updates status.usage from explicit report deltas and from
// novel runtime policy decisions (idempotent with decision dedup).
//
// Explicit usage deltas are applied when the report is usage-only or when at least
// one decision in the payload was novel, so re-delivered decision reports do not
// double-count token deltas bundled with duplicate decisions.
func ApplyUsageFromReport(session *relayv1alpha1.AgentSession, usageDelta *relayv1alpha1.SessionUsage, novelDecisions []relayv1alpha1.PolicyDecision, decisionsInReport int) {
	if session == nil {
		return
	}
	for _, d := range novelDecisions {
		incrementUsageForDecision(session, d)
	}
	if usageDelta != nil && (decisionsInReport == 0 || len(novelDecisions) > 0) {
		addUsageDelta(session, usageDelta)
	}
}

func incrementUsageForDecision(session *relayv1alpha1.AgentSession, d relayv1alpha1.PolicyDecision) {
	if d.Phase != relayv1alpha1.PolicyDecisionPhaseRuntime {
		return
	}
	u := ensureUsage(session)
	switch d.Type {
	case "network":
		u.NetworkRequests++
	case "tool":
		u.ToolCalls++
	}
}

func addUsageDelta(session *relayv1alpha1.AgentSession, delta *relayv1alpha1.SessionUsage) {
	if delta == nil {
		return
	}
	u := ensureUsage(session)
	u.InputTokens += delta.InputTokens
	u.OutputTokens += delta.OutputTokens
	u.ToolCalls += delta.ToolCalls
	u.NetworkRequests += delta.NetworkRequests
}

func ensureUsage(session *relayv1alpha1.AgentSession) *relayv1alpha1.SessionUsage {
	if session.Status.Usage == nil {
		session.Status.Usage = &relayv1alpha1.SessionUsage{}
	}
	return session.Status.Usage
}

// mergeUsageInPlace preserves monotonic counters from preserve when dst lags behind.
func mergeUsageInPlace(dst **relayv1alpha1.SessionUsage, preserve *relayv1alpha1.SessionUsage) {
	if dst == nil || preserve == nil {
		return
	}
	if *dst == nil {
		cp := *preserve
		*dst = &cp
		return
	}
	(*dst).InputTokens = max64((*dst).InputTokens, preserve.InputTokens)
	(*dst).OutputTokens = max64((*dst).OutputTokens, preserve.OutputTokens)
	(*dst).ToolCalls = max64((*dst).ToolCalls, preserve.ToolCalls)
	(*dst).NetworkRequests = max64((*dst).NetworkRequests, preserve.NetworkRequests)
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// usageFromRuntimeReport extracts optional usage delta from a runtime report.
func usageFromRuntimeReport(report enforcement.RuntimeReport) *relayv1alpha1.SessionUsage {
	return report.Usage
}
