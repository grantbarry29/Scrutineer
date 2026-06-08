/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package enforcement

import relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"

// Evaluation is the backend-neutral outcome of applying effective policy mode to a
// would-deny rule check (network, tool, approval, etc.).
type Evaluation struct {
	// Allowed is whether the action may proceed at runtime.
	Allowed bool
	// Action is the policy decision action to record for audit/UI surfaces.
	Action relayv1alpha1.PolicyDecisionAction
	// WouldDeny is true when policy denies the action but mode still permits it (dry-run).
	WouldDeny bool
	// Blocked is true when mode is enforced and the action must not proceed.
	Blocked bool
}

// ActionForMode returns the decision action recorded for restrictive policy under mode.
// Backends use this when logging declared deny rules and runtime denials.
func ActionForMode(mode relayv1alpha1.PolicyMode) relayv1alpha1.PolicyDecisionAction {
	switch mode {
	case relayv1alpha1.PolicyModeEnforced:
		return relayv1alpha1.PolicyDecisionDeny
	case relayv1alpha1.PolicyModeDryRun:
		return relayv1alpha1.PolicyDecisionDryRun
	default:
		return relayv1alpha1.PolicyDecisionAudit
	}
}

// EvaluateRestrictive applies mode semantics when a rule evaluation would deny.
// If ruleWouldDeny is false, the action is always allowed.
func EvaluateRestrictive(mode relayv1alpha1.PolicyMode, ruleWouldDeny bool) Evaluation {
	if !ruleWouldDeny {
		return Evaluation{
			Allowed: true,
			Action:  relayv1alpha1.PolicyDecisionAllow,
		}
	}

	switch mode {
	case relayv1alpha1.PolicyModeEnforced:
		return Evaluation{
			Allowed:   false,
			Action:    relayv1alpha1.PolicyDecisionDeny,
			WouldDeny: true,
			Blocked:   true,
		}
	case relayv1alpha1.PolicyModeDryRun:
		return Evaluation{
			Allowed:   true,
			Action:    relayv1alpha1.PolicyDecisionDryRun,
			WouldDeny: true,
		}
	default:
		return Evaluation{
			Allowed: true,
			Action:  relayv1alpha1.PolicyDecisionAudit,
		}
	}
}

// ShouldRecordViolation reports whether a restrictive evaluation should produce a
// status.violations entry. Enforced blocks and dry-run would-denies are recorded;
// audit-only allows without a violation record.
func ShouldRecordViolation(ev Evaluation) bool {
	if ev.Blocked {
		return true
	}
	return ev.WouldDeny && ev.Action == relayv1alpha1.PolicyDecisionDryRun
}
