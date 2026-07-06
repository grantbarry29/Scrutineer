/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package agentsession

import (
	"context"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/enforcement"
	"github.com/grantbarry29/scrutineer/internal/policy"
)

// ApplyPolicyStatus writes merged policy fields and merge-time decisions, then re-appends
// runtime-phase decisions from priorDecisions so reconcile does not wipe enforcement evidence.
func ApplyPolicyStatus(session *scrutineerv1alpha1.AgentSession, resolved policy.Resolved, priorDecisions []scrutineerv1alpha1.PolicyDecision) {
	policy.ApplyStatus(session, resolved)
	runtime := RuntimePolicyDecisions(priorDecisions)
	if len(runtime) == 0 {
		return
	}
	session.Status.PolicyDecisions = enforcement.AppendRuntimeDecisions(
		session.Status.PolicyDecisions,
		runtime,
		enforcement.MaxPolicyDecisions,
	)
}

// AppendRuntimePolicyDecisions appends new runtime-phase decisions onto session status
// without dropping existing merge-time entries. Duplicate decisions (same policyDecisionKey)
// are skipped so reporter re-delivery is idempotent. Returns the novel decisions appended.
func AppendRuntimePolicyDecisions(session *scrutineerv1alpha1.AgentSession, incoming []scrutineerv1alpha1.PolicyDecision) []scrutineerv1alpha1.PolicyDecision {
	novel := novelRuntimePolicyDecisions(session, incoming)
	if session == nil || len(novel) == 0 {
		return novel
	}
	session.Status.PolicyDecisions = enforcement.AppendRuntimeDecisions(
		session.Status.PolicyDecisions,
		novel,
		enforcement.MaxPolicyDecisions,
	)
	return novel
}

func novelRuntimePolicyDecisions(session *scrutineerv1alpha1.AgentSession, incoming []scrutineerv1alpha1.PolicyDecision) []scrutineerv1alpha1.PolicyDecision {
	if session == nil || len(incoming) == 0 {
		return nil
	}
	keys := make(map[string]struct{}, len(session.Status.PolicyDecisions))
	for _, d := range session.Status.PolicyDecisions {
		keys[policyDecisionKey(d)] = struct{}{}
	}
	var novel []scrutineerv1alpha1.PolicyDecision
	for _, d := range incoming {
		if d.Phase == "" {
			d.Phase = scrutineerv1alpha1.PolicyDecisionPhaseRuntime
		}
		if _, ok := keys[policyDecisionKey(d)]; ok {
			continue
		}
		novel = append(novel, d)
		keys[policyDecisionKey(d)] = struct{}{}
	}
	return novel
}

// ApplyRuntimePolicyReport merges runtime evidence from a data-plane backend into status.
// ctx is the reconcile/request context, threaded down to audit emission (#59).
func ApplyRuntimePolicyReport(ctx context.Context, session *scrutineerv1alpha1.AgentSession, report enforcement.RuntimeReport) {
	novelDecisions := AppendRuntimePolicyDecisions(session, report.Decisions)

	violations := append([]scrutineerv1alpha1.PolicyViolation(nil), report.Violations...)
	derived := enforcement.ViolationsFromDecisions(report.Decisions)
	if len(derived) > 0 {
		keys := make(map[string]struct{}, len(violations))
		for _, v := range violations {
			keys[violationKey(v)] = struct{}{}
		}
		for _, v := range derived {
			if _, ok := keys[violationKey(v)]; !ok {
				violations = append(violations, v)
			}
		}
	}
	AppendRuntimeViolations(ctx, session, violations)
	AppendSessionEvents(session, report.Events)
	ApplyUsageFromReport(session, usageFromRuntimeReport(report), novelDecisions, len(report.Decisions))
}

// RuntimePolicyDecisions returns only phase=runtime entries from a decision list.
func RuntimePolicyDecisions(decisions []scrutineerv1alpha1.PolicyDecision) []scrutineerv1alpha1.PolicyDecision {
	if len(decisions) == 0 {
		return nil
	}
	out := make([]scrutineerv1alpha1.PolicyDecision, 0, len(decisions))
	for _, d := range decisions {
		if d.Phase == scrutineerv1alpha1.PolicyDecisionPhaseRuntime {
			out = append(out, d)
		}
	}
	return out
}

func policyDecisionKey(d scrutineerv1alpha1.PolicyDecision) string {
	return d.Time.String() + "\x00" + string(d.Phase) + "\x00" + d.Type + "\x00" + d.Reason + "\x00" + d.Target + "\x00" + string(d.Action)
}

// mergeRuntimePolicyDecisionsInPlace appends runtime-phase decisions from preserve that are
// absent from dst. Merge-time entries in dst are never modified.
func mergeRuntimePolicyDecisionsInPlace(dst *[]scrutineerv1alpha1.PolicyDecision, preserve []scrutineerv1alpha1.PolicyDecision) {
	if dst == nil {
		return
	}
	keys := make(map[string]struct{}, len(*dst))
	for _, d := range *dst {
		keys[policyDecisionKey(d)] = struct{}{}
	}
	var missing []scrutineerv1alpha1.PolicyDecision
	for _, d := range RuntimePolicyDecisions(preserve) {
		if _, ok := keys[policyDecisionKey(d)]; !ok {
			missing = append(missing, d)
		}
	}
	if len(missing) == 0 {
		return
	}
	*dst = enforcement.AppendRuntimeDecisions(*dst, missing, enforcement.MaxPolicyDecisions)
}
