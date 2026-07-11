/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package agentsession

import (
	"context"
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

	ApplyRuntimePolicyReport(context.Background(), session, report)
	if session.Status.Usage == nil || session.Status.Usage.NetworkRequests != 1 {
		t.Fatalf("network requests = %+v", session.Status.Usage)
	}

	ApplyRuntimePolicyReport(context.Background(), session, report)
	if session.Status.Usage.NetworkRequests != 1 {
		t.Fatalf("network requests after re-delivery = %d", session.Status.Usage.NetworkRequests)
	}
}

// #102: usage counters are APPROXIMATE by documented contract (SessionUsage API doc,
// reporter contract §4.3): novelty is judged against the capped decision window
// (enforcement.MaxPolicyDecisions), so at-least-once re-delivery of decisions already
// evicted from that window — e.g. an egress-reporter restart re-reading its whole
// access log — counts them again. This test pins the documented behavior: if counting
// is ever made exact (durable ingest watermark), update the API docs and §4.3 in the
// same change as this test.
func TestUsage_redeliveryAfterCapEvictionOvercountsByContract(t *testing.T) {
	session := &scrutineerv1alpha1.AgentSession{}
	decisionAt := func(i int) scrutineerv1alpha1.PolicyDecision {
		return scrutineerv1alpha1.PolicyDecision{
			Time:   metav1.NewTime(time.Unix(1_700_000_000+int64(i), 0)),
			Phase:  scrutineerv1alpha1.PolicyDecisionPhaseRuntime,
			Type:   "network",
			Action: scrutineerv1alpha1.PolicyDecisionAllow,
			Target: "api.example",
		}
	}

	total := enforcement.MaxPolicyDecisions + 10
	for i := 0; i < total; i++ {
		ApplyRuntimePolicyReport(context.Background(), session, enforcement.RuntimeReport{
			Decisions: []scrutineerv1alpha1.PolicyDecision{decisionAt(i)},
		})
	}
	if got := session.Status.Usage.NetworkRequests; got != int64(total) {
		t.Fatalf("networkRequests = %d, want %d", got, total)
	}

	// Re-deliver a decision still inside the capped window: deduped, not re-counted.
	ApplyRuntimePolicyReport(context.Background(), session, enforcement.RuntimeReport{
		Decisions: []scrutineerv1alpha1.PolicyDecision{decisionAt(total - 1)},
	})
	if got := session.Status.Usage.NetworkRequests; got != int64(total) {
		t.Fatalf("networkRequests after in-window re-delivery = %d, want %d", got, total)
	}

	// Re-deliver the oldest decision, long since evicted from the 64-entry window:
	// it looks novel again and inflates the counter — the documented approximation.
	ApplyRuntimePolicyReport(context.Background(), session, enforcement.RuntimeReport{
		Decisions: []scrutineerv1alpha1.PolicyDecision{decisionAt(0)},
	})
	if got := session.Status.Usage.NetworkRequests; got != int64(total)+1 {
		t.Fatalf("networkRequests after evicted re-delivery = %d, want %d (documented over-count)", got, int64(total)+1)
	}
}

func TestApplyRuntimePolicyReport_incrementsToolUsageFromNovelDecision(t *testing.T) {
	ts := metav1.NewTime(time.Unix(1_700_000_001, 0))
	session := &scrutineerv1alpha1.AgentSession{}
	ApplyRuntimePolicyReport(context.Background(), session, enforcement.RuntimeReport{
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
	ApplyRuntimePolicyReport(context.Background(), session, enforcement.RuntimeReport{
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
	ApplyRuntimePolicyReport(context.Background(), session, report)
	if session.Status.Usage.InputTokens != 50 {
		t.Fatalf("first input tokens = %d", session.Status.Usage.InputTokens)
	}

	ApplyRuntimePolicyReport(context.Background(), session, report)
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
	ApplyRuntimePolicyReport(context.Background(), session, enforcement.RuntimeReport{
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

	ApplyRuntimePolicyReport(context.Background(), session, enforcement.RuntimeReport{
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
