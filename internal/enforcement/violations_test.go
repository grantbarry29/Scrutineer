/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package enforcement

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
)

func TestViolationFromDecision_enforcedDeny(t *testing.T) {
	ts := metav1.NewTime(time.Unix(0, 0))
	v, ok := ViolationFromDecision(scrutineerv1alpha1.PolicyDecision{
		Time:   ts,
		Type:   "network",
		Action: scrutineerv1alpha1.PolicyDecisionDeny,
		Target: "10.0.0.1",
	})
	if !ok {
		t.Fatal("expected violation")
	}
	if v.Type != "network" || v.Target != "10.0.0.1" {
		t.Fatalf("got %+v", v)
	}
}

func TestViolationFromDecision_dryRun(t *testing.T) {
	ts := metav1.NewTime(time.Unix(0, 0))
	v, ok := ViolationFromDecision(scrutineerv1alpha1.PolicyDecision{
		Time:    ts,
		Type:    "tool",
		Action:  scrutineerv1alpha1.PolicyDecisionDryRun,
		Target:  "kubectl",
		Message: "would deny tool call",
	})
	if !ok || v.Message != "would deny tool call" {
		t.Fatalf("got %+v, ok=%v", v, ok)
	}
}

func TestViolationFromDecision_auditOnlySkipped(t *testing.T) {
	_, ok := ViolationFromDecision(scrutineerv1alpha1.PolicyDecision{
		Time:   metav1.Now(),
		Type:   "network",
		Action: scrutineerv1alpha1.PolicyDecisionAudit,
	})
	if ok {
		t.Fatal("audit action should not produce violation")
	}
}

func TestViolationsFromDecisions_filtersActions(t *testing.T) {
	ts := metav1.NewTime(time.Unix(0, 0))
	got := ViolationsFromDecisions([]scrutineerv1alpha1.PolicyDecision{
		{Time: ts, Type: "network", Action: scrutineerv1alpha1.PolicyDecisionDeny, Target: "a"},
		{Time: ts, Type: "network", Action: scrutineerv1alpha1.PolicyDecisionAudit, Target: "b"},
		{Time: ts, Type: "tool", Action: scrutineerv1alpha1.PolicyDecisionDryRun, Target: "c"},
	})
	if len(got) != 2 {
		t.Fatalf("len = %d", len(got))
	}
}

func TestAppendViolations_truncatesWithSummary(t *testing.T) {
	ts := metav1.NewTime(time.Unix(0, 0))
	existing := make([]scrutineerv1alpha1.PolicyViolation, MaxViolations)
	for i := range existing {
		existing[i] = scrutineerv1alpha1.PolicyViolation{Time: ts, Type: "network", Message: "blocked"}
	}
	got := AppendViolations(existing, []scrutineerv1alpha1.PolicyViolation{{
		Time: ts, Type: "network", Message: "one more",
	}}, MaxViolations)
	if len(got) != MaxViolations {
		t.Fatalf("len = %d", len(got))
	}
	if got[len(got)-1].Type != "summary" {
		t.Fatalf("last = %+v", got[len(got)-1])
	}
}
