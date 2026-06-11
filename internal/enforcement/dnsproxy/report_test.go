/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package dnsproxy

import (
	"testing"
	"time"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
)

func TestRuntimeReportFromEvent_enforcedViolation(t *testing.T) {
	ctx := baseCtx(relayv1alpha1.PolicyModeEnforced, relayv1alpha1.PolicyRules{
		DeniedDomains: []string{"evil.example"},
	})
	report := RuntimeReportFromEvent(ctx, RuntimeEvent{Host: "evil.example"}, time.Unix(0, 0))
	if len(report.Decisions) != 1 || len(report.Violations) != 1 {
		t.Fatalf("decisions=%d violations=%d", len(report.Decisions), len(report.Violations))
	}
	if report.Decisions[0].Type != "network" {
		t.Fatalf("type=%q", report.Decisions[0].Type)
	}
}

func TestRuntimeReportFromEvent_allowedMessage(t *testing.T) {
	ctx := baseCtx(relayv1alpha1.PolicyModeEnforced, relayv1alpha1.PolicyRules{
		AllowedDomains: []string{"github.com"},
	})
	report := RuntimeReportFromEvent(ctx, RuntimeEvent{Host: "github.com"}, time.Unix(0, 0))
	if len(report.Decisions) != 1 {
		t.Fatalf("decisions=%d", len(report.Decisions))
	}
	if report.Decisions[0].Rule != "" {
		t.Fatalf("rule=%q", report.Decisions[0].Rule)
	}
	if len(report.Violations) != 0 {
		t.Fatalf("violations=%d", len(report.Violations))
	}
}

func TestRuntimeReportFromEvent_cidrReasons(t *testing.T) {
	ctx := baseCtx(relayv1alpha1.PolicyModeEnforced, relayv1alpha1.PolicyRules{
		DeniedCIDRs: []string{"10.0.0.0/8"},
	})
	report := RuntimeReportFromEvent(ctx, RuntimeEvent{Host: "10.1.2.3"}, time.Unix(0, 0))
	if report.Decisions[0].Rule != "deniedCIDRs" {
		t.Fatalf("rule=%q", report.Decisions[0].Rule)
	}
}

func TestRuntimeReportFromEvent_auditNoViolation(t *testing.T) {
	ctx := baseCtx(relayv1alpha1.PolicyModeAuditOnly, relayv1alpha1.PolicyRules{
		DeniedDomains: []string{"evil.example"},
	})
	report := RuntimeReportFromEvent(ctx, RuntimeEvent{Host: "evil.example"}, time.Unix(0, 0))
	if len(report.Violations) != 0 {
		t.Fatalf("violations=%+v", report.Violations)
	}
}
