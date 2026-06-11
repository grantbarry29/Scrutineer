/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package workspace

import (
	"testing"
	"time"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
	"github.com/secureai/relay/internal/enforcement"
)

func TestRuntimeReport_enforcedDeny(t *testing.T) {
	ctx := enforcement.SessionContext{
		Mode: relayv1alpha1.PolicyModeEnforced,
		Policy: relayv1alpha1.PolicyRules{
			DeniedPaths: []string{"/etc/**"},
		},
	}
	auth := EvaluateFile(ctx, FileRequest{Path: "/etc/passwd", Operation: "read"})
	report := RuntimeReport(ctx, FileRequest{Path: "/etc/passwd", Operation: "read"}, auth, time.Unix(0, 0))
	if len(report.Decisions) != 1 || len(report.Violations) != 1 {
		t.Fatalf("decisions=%d violations=%d", len(report.Decisions), len(report.Violations))
	}
	if report.Decisions[0].Type != "file" || report.Decisions[0].Rule != "deniedPaths" {
		t.Fatalf("decision = %+v", report.Decisions[0])
	}
}

func TestRuntimeReport_allowedAndAllowlistMessages(t *testing.T) {
	ctx := enforcement.SessionContext{
		Mode: relayv1alpha1.PolicyModeEnforced,
		Policy: relayv1alpha1.PolicyRules{
			AllowedPaths: []string{"/workspace/**"},
		},
	}
	auth := EvaluateFile(ctx, FileRequest{Path: "/workspace/out.txt"})
	report := RuntimeReport(ctx, FileRequest{Path: "/workspace/out.txt"}, auth, time.Unix(0, 0))
	if report.Decisions[0].Rule != "" || len(report.Violations) != 0 {
		t.Fatalf("allowed report = %+v", report)
	}

	auth = EvaluateFile(ctx, FileRequest{Path: "/tmp/x"})
	report = RuntimeReport(ctx, FileRequest{Path: "/tmp/x"}, auth, time.Unix(0, 0))
	if report.Decisions[0].Rule != "allowedPaths" {
		t.Fatalf("rule=%q", report.Decisions[0].Rule)
	}
}

func TestRuntimeReport_emptyPathTarget(t *testing.T) {
	ctx := enforcement.SessionContext{Mode: relayv1alpha1.PolicyModeAuditOnly}
	auth := EvaluateFile(ctx, FileRequest{})
	report := RuntimeReport(ctx, FileRequest{}, auth, time.Unix(0, 0))
	if report.Decisions[0].Target != "unknown" {
		t.Fatalf("target=%q", report.Decisions[0].Target)
	}
}
