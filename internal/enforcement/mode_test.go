/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package enforcement

import (
	"testing"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
)

func TestActionForMode(t *testing.T) {
	tests := []struct {
		mode relayv1alpha1.PolicyMode
		want relayv1alpha1.PolicyDecisionAction
	}{
		{relayv1alpha1.PolicyModeAuditOnly, relayv1alpha1.PolicyDecisionAudit},
		{relayv1alpha1.PolicyModeDryRun, relayv1alpha1.PolicyDecisionDryRun},
		{relayv1alpha1.PolicyModeEnforced, relayv1alpha1.PolicyDecisionDeny},
		{"", relayv1alpha1.PolicyDecisionAudit},
	}
	for _, tc := range tests {
		if got := ActionForMode(tc.mode); got != tc.want {
			t.Fatalf("ActionForMode(%q) = %q, want %q", tc.mode, got, tc.want)
		}
	}
}

func TestEvaluateRestrictive_allowedWhenRulePasses(t *testing.T) {
	for _, mode := range []relayv1alpha1.PolicyMode{
		relayv1alpha1.PolicyModeAuditOnly,
		relayv1alpha1.PolicyModeDryRun,
		relayv1alpha1.PolicyModeEnforced,
	} {
		ev := EvaluateRestrictive(mode, false)
		if !ev.Allowed || ev.Blocked || ev.WouldDeny {
			t.Fatalf("mode %q: expected allow, got %+v", mode, ev)
		}
		if ev.Action != relayv1alpha1.PolicyDecisionAllow {
			t.Fatalf("mode %q: action = %q, want allow", mode, ev.Action)
		}
	}
}

func TestEvaluateRestrictive_enforcedBlocks(t *testing.T) {
	ev := EvaluateRestrictive(relayv1alpha1.PolicyModeEnforced, true)
	if ev.Allowed || !ev.Blocked || !ev.WouldDeny {
		t.Fatalf("got %+v, want blocked deny", ev)
	}
	if ev.Action != relayv1alpha1.PolicyDecisionDeny {
		t.Fatalf("action = %q", ev.Action)
	}
	if !ShouldRecordViolation(ev) {
		t.Fatal("expected violation record for enforced block")
	}
}

func TestEvaluateRestrictive_dryRunAllowsWithWouldDeny(t *testing.T) {
	ev := EvaluateRestrictive(relayv1alpha1.PolicyModeDryRun, true)
	if !ev.Allowed || ev.Blocked || !ev.WouldDeny {
		t.Fatalf("got %+v, want dry-run would-deny", ev)
	}
	if ev.Action != relayv1alpha1.PolicyDecisionDryRun {
		t.Fatalf("action = %q", ev.Action)
	}
	if !ShouldRecordViolation(ev) {
		t.Fatal("expected violation record for dry-run would-deny")
	}
}

func TestEvaluateRestrictive_auditOnlyAllows(t *testing.T) {
	ev := EvaluateRestrictive(relayv1alpha1.PolicyModeAuditOnly, true)
	if !ev.Allowed || ev.Blocked || ev.WouldDeny {
		t.Fatalf("got %+v, want audit allow-through", ev)
	}
	if ev.Action != relayv1alpha1.PolicyDecisionAudit {
		t.Fatalf("action = %q", ev.Action)
	}
	if ShouldRecordViolation(ev) {
		t.Fatal("audit-only should not record violation for would-deny")
	}
}
