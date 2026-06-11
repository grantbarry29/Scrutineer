/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package workspace

import (
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
	"github.com/secureai/relay/internal/enforcement"
)

// RuntimeReport builds status evidence for a file authorization outcome.
func RuntimeReport(ctx enforcement.SessionContext, req FileRequest, auth FileAuthorization, now time.Time) enforcement.RuntimeReport {
	ts := metav1.NewTime(now)
	target := normalizePath(req.Path)
	if target == "" {
		target = "unknown"
	}

	decision := relayv1alpha1.PolicyDecision{
		Time:    ts,
		Phase:   relayv1alpha1.PolicyDecisionPhaseRuntime,
		Type:    "file",
		Action:  auth.Action,
		Actor:   "relay-fs-gateway",
		Target:  target,
		Reason:  auth.Reason,
		Message: formatFileMessage(ctx, req, auth),
		Mode:    ctx.Mode,
		Rule:    ruleFieldForReason(auth.Reason),
	}

	report := enforcement.RuntimeReport{
		Decisions: []relayv1alpha1.PolicyDecision{decision},
	}
	if v, ok := enforcement.ViolationFromDecision(decision); ok {
		report.Violations = []relayv1alpha1.PolicyViolation{v}
	}
	return report
}

func formatFileMessage(ctx enforcement.SessionContext, req FileRequest, auth FileAuthorization) string {
	p := normalizePath(req.Path)
	if p == "" {
		p = "unknown"
	}
	op := strings.TrimSpace(req.Operation)
	if op != "" {
		p = fmt.Sprintf("%s %s", op, p)
	}
	switch auth.Reason {
	case ReasonDeniedPaths:
		return fmt.Sprintf("path %q is denied by policy (mode=%s)", p, ctx.Mode)
	case ReasonNotInAllowedPaths:
		return fmt.Sprintf("path %q is not in allowedPaths (mode=%s)", p, ctx.Mode)
	case ReasonAllowed:
		return fmt.Sprintf("path %q allowed (mode=%s)", p, ctx.Mode)
	default:
		return fmt.Sprintf("path %q authorization reason=%s (mode=%s)", p, auth.Reason, ctx.Mode)
	}
}

func ruleFieldForReason(reason string) string {
	switch reason {
	case ReasonDeniedPaths:
		return "deniedPaths"
	case ReasonNotInAllowedPaths:
		return "allowedPaths"
	default:
		return ""
	}
}
