/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package agentsession

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/enforcement"
)

func TestApplyRuntimePolicyReport_incrementsNetworkUsageFromNovelDecision(t *testing.T) {
	ts := metav1.NewTime(time.Unix(1_700_000_000, 0))
	session := &scrutineerv1alpha1.AgentSession{}
	report := enforcement.RuntimeReport{
		Decisions: []scrutineerv1alpha1.PolicyDecision{{
			Time:   ts,
			Phase:  scrutineerv1alpha1.PolicyDecisionPhaseRuntime,
			Type:   "network",
			Action: scrutineerv1alpha1.PolicyDecisionDeny,
			Target: "evil.example",
		}},
	}

	ApplyRuntimePolicyReport(session, report)
	if session.Status.Usage == nil || session.Status.Usage.NetworkRequests != 1 {
		t.Fatalf("network requests = %+v", session.Status.Usage)
	}

	ApplyRuntimePolicyReport(session, report)
	if session.Status.Usage.NetworkRequests != 1 {
		t.Fatalf("network requests after re-delivery = %d", session.Status.Usage.NetworkRequests)
	}
}

func TestApplyRuntimePolicyReport_incrementsToolUsageFromNovelDecision(t *testing.T) {
	ts := metav1.NewTime(time.Unix(1_700_000_001, 0))
	session := &scrutineerv1alpha1.AgentSession{}
	ApplyRuntimePolicyReport(session, enforcement.RuntimeReport{
		Decisions: []scrutineerv1alpha1.PolicyDecision{{
			Time:   ts,
			Phase:  scrutineerv1alpha1.PolicyDecisionPhaseRuntime,
			Type:   "tool",
			Action: scrutineerv1alpha1.PolicyDecisionDeny,
			Target: "kubectl",
		}},
	})
	if session.Status.Usage == nil || session.Status.Usage.ToolCalls != 1 {
		t.Fatalf("tool calls = %+v", session.Status.Usage)
	}
}

func TestApplyRuntimePolicyReport_appliesExplicitTokenUsageDelta(t *testing.T) {
	session := &scrutineerv1alpha1.AgentSession{}
	ApplyRuntimePolicyReport(session, enforcement.RuntimeReport{
		Usage: &scrutineerv1alpha1.SessionUsage{
			InputTokens:  100,
			OutputTokens: 40,
		},
	})
	if session.Status.Usage == nil {
		t.Fatal("usage nil")
	}
	if session.Status.Usage.InputTokens != 100 || session.Status.Usage.OutputTokens != 40 {
		t.Fatalf("usage = %+v", session.Status.Usage)
	}
}

func TestApplyRuntimePolicyReport_skipsUsageDeltaWhenDecisionsAreDuplicates(t *testing.T) {
	ts := metav1.NewTime(time.Unix(1_700_000_002, 0))
	session := &scrutineerv1alpha1.AgentSession{}
	report := enforcement.RuntimeReport{
		Decisions: []scrutineerv1alpha1.PolicyDecision{{
			Time:   ts,
			Phase:  scrutineerv1alpha1.PolicyDecisionPhaseRuntime,
			Type:   "network",
			Action: scrutineerv1alpha1.PolicyDecisionDeny,
			Target: "evil.example",
		}},
		Usage: &scrutineerv1alpha1.SessionUsage{InputTokens: 50},
	}
	ApplyRuntimePolicyReport(session, report)
	if session.Status.Usage.InputTokens != 50 {
		t.Fatalf("first input tokens = %d", session.Status.Usage.InputTokens)
	}

	ApplyRuntimePolicyReport(session, report)
	if session.Status.Usage.InputTokens != 50 {
		t.Fatalf("input tokens after re-delivery = %d, want 50", session.Status.Usage.InputTokens)
	}
	if session.Status.Usage.NetworkRequests != 1 {
		t.Fatalf("network requests = %d", session.Status.Usage.NetworkRequests)
	}
}

func TestApplyRuntimePolicyReport_incrementsFileUsageFromNovelDecision(t *testing.T) {
	ts := metav1.NewTime(time.Unix(1_700_000_003, 0))
	session := &scrutineerv1alpha1.AgentSession{}
	ApplyRuntimePolicyReport(session, enforcement.RuntimeReport{
		Decisions: []scrutineerv1alpha1.PolicyDecision{{
			Time:   ts,
			Phase:  scrutineerv1alpha1.PolicyDecisionPhaseRuntime,
			Type:   "file",
			Action: scrutineerv1alpha1.PolicyDecisionDeny,
			Target: "/etc/passwd",
		}},
	})
	if session.Status.Usage == nil || session.Status.Usage.FileOperations != 1 {
		t.Fatalf("file operations = %+v", session.Status.Usage)
	}

	ApplyRuntimePolicyReport(session, enforcement.RuntimeReport{
		Decisions: []scrutineerv1alpha1.PolicyDecision{{
			Time:   ts,
			Phase:  scrutineerv1alpha1.PolicyDecisionPhaseRuntime,
			Type:   "file",
			Action: scrutineerv1alpha1.PolicyDecisionDeny,
			Target: "/etc/passwd",
		}},
	})
	if session.Status.Usage.FileOperations != 1 {
		t.Fatalf("file operations after re-delivery = %d", session.Status.Usage.FileOperations)
	}
}

func TestMergeUsageInPlace_monotonic(t *testing.T) {
	dst := &scrutineerv1alpha1.SessionUsage{ToolCalls: 3}
	preserve := &scrutineerv1alpha1.SessionUsage{ToolCalls: 5, NetworkRequests: 2, FileOperations: 4}
	mergeUsageInPlace(&dst, preserve)
	if dst.ToolCalls != 5 || dst.NetworkRequests != 2 || dst.FileOperations != 4 {
		t.Fatalf("merged = %+v", dst)
	}
}
