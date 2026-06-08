/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package toolgateway

import (
	"testing"
	"time"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
)

func TestRuntimeReport_enforcedDenyIncludesViolation(t *testing.T) {
	ctx := baseCtx(relayv1alpha1.PolicyModeEnforced, relayv1alpha1.PolicyRules{
		DeniedTools: []string{"kubectl"},
	})
	auth := EvaluateTool(ctx, ToolRequest{Tool: "kubectl", RequestID: "req-1"})
	report := RuntimeReport(ctx, ToolRequest{Tool: "kubectl"}, auth, time.Unix(0, 0))

	if len(report.Decisions) != 1 {
		t.Fatalf("decisions=%d", len(report.Decisions))
	}
	if report.Decisions[0].Phase != relayv1alpha1.PolicyDecisionPhaseRuntime {
		t.Fatalf("phase=%q", report.Decisions[0].Phase)
	}
	if len(report.Violations) != 1 {
		t.Fatalf("violations=%d", len(report.Violations))
	}
}

func TestRuntimeReport_dryRunIncludesViolation(t *testing.T) {
	ctx := baseCtx(relayv1alpha1.PolicyModeDryRun, relayv1alpha1.PolicyRules{
		DeniedTools: []string{"kubectl"},
	})
	auth := EvaluateTool(ctx, ToolRequest{Tool: "kubectl"})
	report := RuntimeReport(ctx, ToolRequest{Tool: "kubectl"}, auth, time.Unix(0, 0))
	if len(report.Violations) != 1 {
		t.Fatalf("violations=%d", len(report.Violations))
	}
}

func TestRuntimeReport_auditNoViolation(t *testing.T) {
	ctx := baseCtx(relayv1alpha1.PolicyModeAuditOnly, relayv1alpha1.PolicyRules{
		DeniedTools: []string{"kubectl"},
	})
	auth := EvaluateTool(ctx, ToolRequest{Tool: "kubectl"})
	report := RuntimeReport(ctx, ToolRequest{Tool: "kubectl"}, auth, time.Unix(0, 0))
	if len(report.Violations) != 0 {
		t.Fatalf("violations=%+v", report.Violations)
	}
}
