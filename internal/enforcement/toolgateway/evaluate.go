/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package toolgateway

import (
	"strings"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
	"github.com/secureai/relay/internal/enforcement"
)

const (
	ReasonAllowed           = "Allowed"
	ReasonDeniedTools       = "DeniedTools"
	ReasonNotInAllowedTools = "NotInAllowedTools"
	ReasonApprovalRequired  = "ApprovalRequired"
)

// DefaultListenAddr is the in-pod URL agents use when a tool-gateway sidecar is injected.
const DefaultListenAddr = "http://127.0.0.1:19090"

// DefaultListenHost is the bind address for the tool-gateway HTTP server.
const DefaultListenHost = "127.0.0.1:19090"

// HasToolPolicy reports whether effective policy contains tool governance hints.
func HasToolPolicy(rules relayv1alpha1.PolicyRules) bool {
	return len(rules.AllowedTools) > 0 ||
		len(rules.DeniedTools) > 0 ||
		len(rules.RequireHumanApproval) > 0 ||
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
				Action:  relayv1alpha1.PolicyDecisionDeny,
				Blocked: ctx.Mode == relayv1alpha1.PolicyModeEnforced,
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
	if containsString(rules.RequireHumanApproval, tool) {
		// Approval workflows are Phase 5; surface as would-deny under restrictive modes.
		return authorize(ctx.Mode, true, ReasonApprovalRequired)
	}
	return ToolAuthorization{
		Evaluation: enforcement.Evaluation{
			Allowed: true,
			Action:  relayv1alpha1.PolicyDecisionAllow,
		},
		Reason: ReasonAllowed,
	}
}

func authorize(mode relayv1alpha1.PolicyMode, ruleWouldDeny bool, reason string) ToolAuthorization {
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
