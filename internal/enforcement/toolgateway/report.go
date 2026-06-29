/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package toolgateway

import (
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/enforcement"
)

// RuntimeReport builds status evidence for a tool authorization outcome.
func RuntimeReport(ctx enforcement.SessionContext, req ToolRequest, auth ToolAuthorization, now time.Time) enforcement.RuntimeReport {
	ts := metav1.NewTime(now)
	target := req.Tool
	if target == "" {
		target = req.Method
	}

	decision := scrutineerv1alpha1.PolicyDecision{
		Time:    ts,
		Phase:   scrutineerv1alpha1.PolicyDecisionPhaseRuntime,
		Type:    "tool",
		Action:  auth.Action,
		Actor:   "scrutineer-tool-gateway",
		Target:  target,
		Reason:  auth.Reason,
		Message: formatToolMessage(ctx, req, auth),
		Mode:    ctx.Mode,
		Rule:    ruleFieldForReason(auth.Reason),
	}

	report := enforcement.RuntimeReport{
		Decisions: []scrutineerv1alpha1.PolicyDecision{decision},
	}
	if v, ok := enforcement.ViolationFromDecision(decision); ok {
		report.Violations = []scrutineerv1alpha1.PolicyViolation{v}
	}
	return report
}

func formatToolMessage(ctx enforcement.SessionContext, req ToolRequest, auth ToolAuthorization) string {
	tool := req.Tool
	if tool == "" {
		tool = "unknown"
	}
	switch auth.Reason {
	case ReasonDeniedTools:
		return fmt.Sprintf("tool %q is denied by policy (mode=%s)", tool, ctx.Mode)
	case ReasonNotInAllowedTools:
		return fmt.Sprintf("tool %q is not in allowedTools (mode=%s)", tool, ctx.Mode)
	case ReasonApprovalRequired:
		return fmt.Sprintf("tool %q requires human approval (mode=%s)", tool, ctx.Mode)
	case ReasonApprovalGranted:
		return fmt.Sprintf("tool %q allowed by human approval (mode=%s)", tool, ctx.Mode)
	case ReasonApprovalDenied:
		return fmt.Sprintf("tool %q denied by human approval (mode=%s)", tool, ctx.Mode)
	case ReasonArgumentDenied:
		return fmt.Sprintf("tool %q denied by argument rule (%s; mode=%s)", tool, argMatchDetail(auth.ArgMatch), ctx.Mode)
	case ReasonArgumentNotAllowed:
		return fmt.Sprintf("tool %q arguments not in allowlist (%s; mode=%s)", tool, argMatchDetail(auth.ArgMatch), ctx.Mode)
	case ReasonAllowed:
		return fmt.Sprintf("tool %q allowed (mode=%s)", tool, ctx.Mode)
	default:
		return fmt.Sprintf("tool %q authorization reason=%s (mode=%s)", tool, auth.Reason, ctx.Mode)
	}
}

// ApprovalResolvedReport builds self-reported runtime evidence for a resolved
// mid-execution approval hold. It records that the gateway held the call and then
// allowed or denied it per the human decision. The message carries only the
// redacted argDigest — never raw arguments — preserving the redaction invariant.
func ApprovalResolvedReport(ctx enforcement.SessionContext, req ToolRequest, argDigest string, granted bool, now time.Time) enforcement.RuntimeReport {
	ts := metav1.NewTime(now)
	target := req.Tool
	if target == "" {
		target = req.Method
	}
	reason := ReasonApprovalDenied
	action := scrutineerv1alpha1.PolicyDecisionDeny
	if granted {
		reason = ReasonApprovalGranted
		action = scrutineerv1alpha1.PolicyDecisionAllow
	}
	digest := argDigest
	if digest == "" {
		digest = "none"
	}
	decision := scrutineerv1alpha1.PolicyDecision{
		Time:    ts,
		Phase:   scrutineerv1alpha1.PolicyDecisionPhaseRuntime,
		Type:    "approval",
		Action:  action,
		Actor:   "scrutineer-tool-gateway",
		Target:  target,
		Reason:  reason,
		Message: fmt.Sprintf("%s [argDigest=%s]", formatApprovalResolvedMessage(target, granted, ctx.Mode), digest),
		Mode:    ctx.Mode,
		Rule:    "requireHumanApproval",
	}
	report := enforcement.RuntimeReport{Decisions: []scrutineerv1alpha1.PolicyDecision{decision}}
	if v, ok := enforcement.ViolationFromDecision(decision); ok {
		report.Violations = []scrutineerv1alpha1.PolicyViolation{v}
	}
	return report
}

func formatApprovalResolvedMessage(tool string, granted bool, mode scrutineerv1alpha1.PolicyMode) string {
	if granted {
		return fmt.Sprintf("tool %q allowed by human approval (mode=%s)", tool, mode)
	}
	return fmt.Sprintf("tool %q denied by human approval (mode=%s)", tool, mode)
}

func ruleFieldForReason(reason string) string {
	switch reason {
	case ReasonDeniedTools:
		return "deniedTools"
	case ReasonNotInAllowedTools:
		return "allowedTools"
	case ReasonApprovalRequired, ReasonApprovalGranted, ReasonApprovalDenied:
		return "requireHumanApproval"
	case ReasonArgumentDenied, ReasonArgumentNotAllowed:
		return "argumentRules"
	default:
		return ""
	}
}
