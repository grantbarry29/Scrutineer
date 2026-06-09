/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package dnsproxy

import (
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
	"github.com/secureai/relay/internal/enforcement"
)

const runtimeActor = "relay-dns-proxy"

// RuntimeReportForEgress evaluates an egress request and builds status evidence.
func RuntimeReportForEgress(ctx enforcement.SessionContext, req EgressRequest, now time.Time) enforcement.RuntimeReport {
	auth := EvaluateEgress(ctx, req)
	return runtimeReport(ctx, req, auth, now)
}

// RuntimeReportFromEvent re-evaluates policy for a sidecar-reported event.
func RuntimeReportFromEvent(ctx enforcement.SessionContext, ev RuntimeEvent, now time.Time) enforcement.RuntimeReport {
	return RuntimeReportForEgress(ctx, EgressRequest{Host: ev.Host, Port: ev.Port}, now)
}

func runtimeReport(ctx enforcement.SessionContext, req EgressRequest, auth EgressAuthorization, now time.Time) enforcement.RuntimeReport {
	ts := metav1.NewTime(now)
	target := req.Host
	if target == "" {
		target = "unknown"
	}

	decision := relayv1alpha1.PolicyDecision{
		Time:    ts,
		Phase:   relayv1alpha1.PolicyDecisionPhaseRuntime,
		Type:    "network",
		Action:  auth.Action,
		Actor:   runtimeActor,
		Target:  target,
		Reason:  auth.Reason,
		Message: formatEgressMessage(ctx, req, auth),
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

func formatEgressMessage(ctx enforcement.SessionContext, req EgressRequest, auth EgressAuthorization) string {
	host := req.Host
	if host == "" {
		host = "unknown"
	}
	switch auth.Reason {
	case ReasonDeniedDomains:
		return fmt.Sprintf("egress to domain %q denied by policy (mode=%s)", host, ctx.Mode)
	case ReasonNotInAllowedDomains:
		return fmt.Sprintf("egress to domain %q not in allowedDomains (mode=%s)", host, ctx.Mode)
	case ReasonDeniedCIDRs:
		return fmt.Sprintf("egress to %q denied by CIDR policy (mode=%s)", host, ctx.Mode)
	case ReasonNotInAllowedCIDRs:
		return fmt.Sprintf("egress to %q not in allowedCIDRs (mode=%s)", host, ctx.Mode)
	case ReasonAllowed:
		return fmt.Sprintf("egress to %q allowed (mode=%s)", host, ctx.Mode)
	default:
		return fmt.Sprintf("egress to %q reason=%s (mode=%s)", host, auth.Reason, ctx.Mode)
	}
}

func ruleFieldForReason(reason string) string {
	switch reason {
	case ReasonDeniedDomains:
		return "deniedDomains"
	case ReasonNotInAllowedDomains:
		return "allowedDomains"
	case ReasonDeniedCIDRs:
		return "deniedCIDRs"
	case ReasonNotInAllowedCIDRs:
		return "allowedCIDRs"
	default:
		return ""
	}
}
