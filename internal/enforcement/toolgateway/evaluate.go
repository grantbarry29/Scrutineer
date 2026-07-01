/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package toolgateway

import (
	"strings"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/enforcement"
)

const (
	ReasonAllowed            = "Allowed"
	ReasonDeniedTools        = "DeniedTools"
	ReasonNotInAllowedTools  = "NotInAllowedTools"
	ReasonApprovalRequired   = "ApprovalRequired"
	ReasonArgumentDenied     = "ArgumentDenied"
	ReasonArgumentNotAllowed = "ArgumentNotAllowed"
	// ReasonApprovalGranted / ReasonApprovalDenied record the outcome of a resolved
	// mid-execution human-approval hold (self-reported by the gateway after the
	// controller-observed decision lands).
	ReasonApprovalGranted = "ApprovalGranted"
	ReasonApprovalDenied  = "ApprovalDenied"
)

// DefaultInPodURL is the in-pod URL agents use when a tool-gateway sidecar is injected.
const DefaultInPodURL = "http://127.0.0.1:19090"

// DefaultBindAddr is the bind address for the tool-gateway HTTP server.
const DefaultBindAddr = "127.0.0.1:19090"

// HasToolPolicy reports whether effective policy contains tool governance hints.
func HasToolPolicy(rules scrutineerv1alpha1.PolicyRules) bool {
	return len(rules.AllowedTools) > 0 ||
		len(rules.DeniedTools) > 0 ||
		len(rules.RequireHumanApproval) > 0 ||
		len(rules.ArgumentRules) > 0 ||
		rules.MaxToolCalls != nil ||
		rules.MaxCallsPerMinute != nil
}

// HasEnabledSidecar reports whether the session context includes an enabled tool-gateway sidecar.
func HasEnabledSidecar(ctx enforcement.SessionContext) bool {
	for _, s := range ctx.Sidecars {
		if s.Type != SidecarType {
			continue
		}
		if s.Enabled == nil || *s.Enabled {
			return true
		}
	}
	return false
}

// Applicable reports whether tool gateway desired config should be produced.
func Applicable(ctx enforcement.SessionContext) bool {
	return HasToolPolicy(ctx.Policy) || HasEnabledSidecar(ctx)
}

// EvaluateTool applies effective tool policy and mode semantics to a tool request.
// Rate limits and human approval gates are identified but not enforced in slice 6.
func EvaluateTool(ctx enforcement.SessionContext, req ToolRequest) ToolAuthorization {
	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		return ToolAuthorization{
			Evaluation: enforcement.Evaluation{
				Allowed: false,
				Action:  scrutineerv1alpha1.PolicyDecisionDeny,
				Blocked: ctx.Mode == scrutineerv1alpha1.PolicyModeEnforced,
			},
			Reason: "EmptyTool",
		}
	}

	rules := ctx.Policy
	if containsString(rules.DeniedTools, tool) {
		return authorize(ctx.Mode, true, ReasonDeniedTools)
	}
	if len(rules.AllowedTools) > 0 && !containsString(rules.AllowedTools, tool) {
		return authorize(ctx.Mode, true, ReasonNotInAllowedTools)
	}
	// Argument constraints apply only to calls that passed the name gate, and run
	// BEFORE the human-approval gate: an auto-denied call must never be escalated
	// to a person.
	if reason, match := evaluateArgumentRules(rules.ArgumentRules, req); reason != "" {
		auth := authorize(ctx.Mode, true, reason)
		auth.ArgMatch = match
		return auth
	}
	if containsString(rules.RequireHumanApproval, tool) {
		// A call that passed all automatic checks but matches requireHumanApproval is
		// held for a human (mid-execution gate). Under enforced mode this blocks until
		// a scoped grant (the gateway turns this into a hold-and-ask); under audit/
		// dry-run it is recorded as a would-require-approval and allowed through.
		return authorize(ctx.Mode, true, ReasonApprovalRequired)
	}
	return ToolAuthorization{
		Evaluation: enforcement.Evaluation{
			Allowed: true,
			Action:  scrutineerv1alpha1.PolicyDecisionAllow,
		},
		Reason: ReasonAllowed,
	}
}

func authorize(mode scrutineerv1alpha1.PolicyMode, ruleWouldDeny bool, reason string) ToolAuthorization {
	return ToolAuthorization{
		Evaluation: enforcement.EvaluateRestrictive(mode, ruleWouldDeny),
		Reason:     reason,
	}
}

func containsString(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}
