/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package policy

import (
	"fmt"
	"strconv"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
)

const (
	// MaxMergePolicyDecisions caps merge-time policyDecisions written to status.
	MaxMergePolicyDecisions = 64

	mergeDecisionActor = "relay-controller"
)

// BuildMergeDecisions returns merge-time policy decisions for the resolved effective policy.
func BuildMergeDecisions(resolved Resolved, now time.Time) []relayv1alpha1.PolicyDecision {
	ts := metav1.NewTime(now)
	mode := resolved.Mode
	out := []relayv1alpha1.PolicyDecision{{
		Time:    ts,
		Phase:   relayv1alpha1.PolicyDecisionPhaseMerge,
		Type:    "mode",
		Action:  actionForMode(mode),
		Actor:   mergeDecisionActor,
		Reason:  "StrictestMode",
		Message: fmt.Sprintf("Effective policy mode is %s", mode),
		Mode:    mode,
	}}

	for i := range resolved.Matched {
		ref := resolved.Matched[i]
		refCopy := ref
		out = append(out, relayv1alpha1.PolicyDecision{
			Time:      ts,
			Phase:     relayv1alpha1.PolicyDecisionPhaseMerge,
			Type:      "policy",
			Action:    relayv1alpha1.PolicyDecisionAllow,
			Actor:     mergeDecisionActor,
			Target:    ref.Name,
			Reason:    "PolicyMatched",
			Message:   fmt.Sprintf("Matched %s %q", ref.Kind, ref.Name),
			Mode:      mode,
			PolicyRef: &refCopy,
		})
	}

	out = append(out, ruleListDecisions(ts, mode, "network", "allowedDomains", resolved.Rules.AllowedDomains, relayv1alpha1.PolicyDecisionAllow, "AllowedDomains")...)
	out = append(out, ruleListDecisions(ts, mode, "network", "deniedDomains", resolved.Rules.DeniedDomains, restrictiveAction(mode), "DeniedDomains")...)
	out = append(out, ruleListDecisions(ts, mode, "network", "allowedCIDRs", resolved.Rules.AllowedCIDRs, relayv1alpha1.PolicyDecisionAllow, "AllowedCIDRs")...)
	out = append(out, ruleListDecisions(ts, mode, "network", "deniedCIDRs", resolved.Rules.DeniedCIDRs, restrictiveAction(mode), "DeniedCIDRs")...)
	out = append(out, ruleListDecisions(ts, mode, "tool", "allowedTools", resolved.Rules.AllowedTools, relayv1alpha1.PolicyDecisionAllow, "AllowedTools")...)
	out = append(out, ruleListDecisions(ts, mode, "tool", "deniedTools", resolved.Rules.DeniedTools, restrictiveAction(mode), "DeniedTools")...)
	out = append(out, ruleListDecisions(ts, mode, "approval", "requireHumanApproval", resolved.Rules.RequireHumanApproval, relayv1alpha1.PolicyDecisionAudit, "RequireHumanApproval")...)

	if resolved.Rules.MaxNetworkRequests != nil {
		out = append(out, capDecision(ts, mode, "maxNetworkRequests", *resolved.Rules.MaxNetworkRequests))
	}
	if resolved.Rules.MaxToolCalls != nil {
		out = append(out, capDecision(ts, mode, "maxToolCalls", *resolved.Rules.MaxToolCalls))
	}
	if resolved.Rules.MaxCallsPerMinute != nil {
		out = append(out, capDecision(ts, mode, "maxCallsPerMinute", *resolved.Rules.MaxCallsPerMinute))
	}

	if len(out) > MaxMergePolicyDecisions {
		omitted := len(out) - (MaxMergePolicyDecisions - 1)
		out = out[:MaxMergePolicyDecisions-1]
		out = append(out, relayv1alpha1.PolicyDecision{
			Time:    ts,
			Phase:   relayv1alpha1.PolicyDecisionPhaseMerge,
			Type:    "summary",
			Action:  relayv1alpha1.PolicyDecisionAudit,
			Actor:   mergeDecisionActor,
			Reason:  "DecisionsTruncated",
			Message: fmt.Sprintf("%d additional merge-time decisions omitted (max %d)", omitted, MaxMergePolicyDecisions),
			Mode:    mode,
		})
	}
	return out
}

func ruleListDecisions(ts metav1.Time, mode relayv1alpha1.PolicyMode, typ, rule string, targets []string, action relayv1alpha1.PolicyDecisionAction, reason string) []relayv1alpha1.PolicyDecision {
	if len(targets) == 0 {
		return nil
	}
	out := make([]relayv1alpha1.PolicyDecision, 0, len(targets))
	for _, target := range targets {
		out = append(out, relayv1alpha1.PolicyDecision{
			Time:    ts,
			Phase:   relayv1alpha1.PolicyDecisionPhaseMerge,
			Type:    typ,
			Action:  action,
			Actor:   mergeDecisionActor,
			Target:  target,
			Reason:  reason,
			Message: fmt.Sprintf("Effective policy %s includes %q", rule, target),
			Mode:    mode,
			Rule:    rule,
		})
	}
	return out
}

func capDecision(ts metav1.Time, mode relayv1alpha1.PolicyMode, rule string, value int32) relayv1alpha1.PolicyDecision {
	return relayv1alpha1.PolicyDecision{
		Time:    ts,
		Phase:   relayv1alpha1.PolicyDecisionPhaseMerge,
		Type:    "cap",
		Action:  relayv1alpha1.PolicyDecisionAllow,
		Actor:   mergeDecisionActor,
		Target:  strconv.FormatInt(int64(value), 10),
		Reason:  "CapDeclared",
		Message: fmt.Sprintf("Effective policy declares %s=%d", rule, value),
		Mode:    mode,
		Rule:    rule,
	}
}

func restrictiveAction(mode relayv1alpha1.PolicyMode) relayv1alpha1.PolicyDecisionAction {
	switch mode {
	case relayv1alpha1.PolicyModeEnforced:
		return relayv1alpha1.PolicyDecisionDeny
	case relayv1alpha1.PolicyModeDryRun:
		return relayv1alpha1.PolicyDecisionDryRun
	default:
		return relayv1alpha1.PolicyDecisionAudit
	}
}

func actionForMode(mode relayv1alpha1.PolicyMode) relayv1alpha1.PolicyDecisionAction {
	switch mode {
	case relayv1alpha1.PolicyModeEnforced:
		return relayv1alpha1.PolicyDecisionDeny
	case relayv1alpha1.PolicyModeDryRun:
		return relayv1alpha1.PolicyDecisionDryRun
	default:
		return relayv1alpha1.PolicyDecisionAudit
	}
}
