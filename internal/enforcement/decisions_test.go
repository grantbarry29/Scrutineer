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

func TestAppendRuntimeDecisions_preservesMergeAndSetsPhase(t *testing.T) {
	ts := metav1.NewTime(time.Unix(0, 0))
	merge := []scrutineerv1alpha1.PolicyDecision{{
		Time:   ts,
		Phase:  scrutineerv1alpha1.PolicyDecisionPhaseMerge,
		Type:   "mode",
		Action: scrutineerv1alpha1.PolicyDecisionAudit,
		Reason: "StrictestMode",
	}}
	runtime := []scrutineerv1alpha1.PolicyDecision{{
		Time:   ts,
		Type:   "network",
		Action: scrutineerv1alpha1.PolicyDecisionDeny,
		Reason: "DeniedDomains",
		Target: "evil.example",
	}}

	got := AppendRuntimeDecisions(merge, runtime, MaxPolicyDecisions)

	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Phase != scrutineerv1alpha1.PolicyDecisionPhaseMerge {
		t.Fatalf("merge phase = %q", got[0].Phase)
	}
	if got[1].Phase != scrutineerv1alpha1.PolicyDecisionPhaseRuntime {
		t.Fatalf("runtime phase = %q", got[1].Phase)
	}
}

func TestAppendRuntimeDecisions_truncatesWithSummary(t *testing.T) {
	ts := metav1.NewTime(time.Unix(0, 0))
	merge := make([]scrutineerv1alpha1.PolicyDecision, 0, MaxPolicyDecisions)
	for i := 0; i < MaxPolicyDecisions; i++ {
		merge = append(merge, scrutineerv1alpha1.PolicyDecision{
			Time:   ts,
			Phase:  scrutineerv1alpha1.PolicyDecisionPhaseMerge,
			Type:   "tool",
			Action: scrutineerv1alpha1.PolicyDecisionAudit,
			Target: "tool-" + string(rune('a'+i%26)),
		})
	}
	runtime := []scrutineerv1alpha1.PolicyDecision{{
		Time:   ts,
		Type:   "network",
		Action: scrutineerv1alpha1.PolicyDecisionDeny,
		Reason: "DeniedDomains",
	}}

	got := AppendRuntimeDecisions(merge, runtime, MaxPolicyDecisions)

	if len(got) != MaxPolicyDecisions {
		t.Fatalf("len = %d, want %d", len(got), MaxPolicyDecisions)
	}
	last := got[len(got)-1]
	if last.Reason != "DecisionsTruncated" {
		t.Fatalf("last reason = %q", last.Reason)
	}
	if last.Phase != scrutineerv1alpha1.PolicyDecisionPhaseRuntime {
		t.Fatalf("summary phase = %q", last.Phase)
	}
}

func netDecision(action scrutineerv1alpha1.PolicyDecisionAction, target string, sec int64) scrutineerv1alpha1.PolicyDecision {
	return scrutineerv1alpha1.PolicyDecision{
		Time:   metav1.NewTime(time.Unix(sec, 0)),
		Type:   "network",
		Action: action,
		Target: target,
	}
}

func targets(decisions []scrutineerv1alpha1.PolicyDecision) map[string]scrutineerv1alpha1.PolicyDecisionAction {
	out := map[string]scrutineerv1alpha1.PolicyDecisionAction{}
	for _, d := range decisions {
		out[d.Target] = d.Action
	}
	return out
}

// #67: a burst of observed allow egress must not evict the higher-value deny/dry-run
// records — non-allow decisions are kept before allows when truncating.
func TestAppendRuntimeDecisions_prefersNonAllowOverAllow(t *testing.T) {
	const cap = 5
	runtime := []scrutineerv1alpha1.PolicyDecision{
		netDecision(scrutineerv1alpha1.PolicyDecisionAllow, "a1.example", 1),
		netDecision(scrutineerv1alpha1.PolicyDecisionAllow, "a2.example", 2),
		netDecision(scrutineerv1alpha1.PolicyDecisionDeny, "deny1.example", 3),
		netDecision(scrutineerv1alpha1.PolicyDecisionAllow, "a3.example", 4),
		netDecision(scrutineerv1alpha1.PolicyDecisionAllow, "a4.example", 5),
		netDecision(scrutineerv1alpha1.PolicyDecisionDryRun, "dryrun1.example", 6),
		netDecision(scrutineerv1alpha1.PolicyDecisionAllow, "a5.example", 7),
	}

	got := AppendRuntimeDecisions(nil, runtime, cap)

	if len(got) != cap {
		t.Fatalf("len = %d, want %d", len(got), cap)
	}
	if got[len(got)-1].Reason != "DecisionsTruncated" {
		t.Fatalf("expected truncation summary last, got %+v", got[len(got)-1])
	}
	tg := targets(got)
	// Both non-allow records survive regardless of allow volume around them.
	if tg["deny1.example"] != scrutineerv1alpha1.PolicyDecisionDeny {
		t.Fatalf("deny evicted by allow volume: %+v", tg)
	}
	if tg["dryrun1.example"] != scrutineerv1alpha1.PolicyDecisionDryRun {
		t.Fatalf("dry-run evicted by allow volume: %+v", tg)
	}
	// Chronological order preserved among kept records.
	var lastSec int64 = -1
	for _, d := range got[:len(got)-1] { // exclude summary
		if s := d.Time.Unix(); s < lastSec {
			t.Fatalf("kept decisions out of chronological order: %+v", got)
		} else {
			lastSec = s
		}
	}
}

// When denies alone exceed the budget, the most recent denies are kept.
func TestAppendRuntimeDecisions_keepsMostRecentDenies(t *testing.T) {
	const cap = 3 // 2 runtime slots + summary
	var runtime []scrutineerv1alpha1.PolicyDecision
	for i := int64(1); i <= 5; i++ {
		runtime = append(runtime, netDecision(scrutineerv1alpha1.PolicyDecisionDeny, "deny"+itoa(int(i))+".example", i))
	}

	got := AppendRuntimeDecisions(nil, runtime, cap)
	tg := targets(got)
	if _, ok := tg["deny5.example"]; !ok {
		t.Fatalf("most recent deny must be kept: %+v", tg)
	}
	if _, ok := tg["deny4.example"]; !ok {
		t.Fatalf("second most recent deny must be kept: %+v", tg)
	}
	if _, ok := tg["deny1.example"]; ok {
		t.Fatalf("oldest deny should have been dropped: %+v", tg)
	}
}

// A stale truncation summary in existing must not accumulate across passes.
func TestAppendRuntimeDecisions_dropsStaleSummary(t *testing.T) {
	existing := []scrutineerv1alpha1.PolicyDecision{
		netDecision(scrutineerv1alpha1.PolicyDecisionAllow, "old.example", 1),
		{
			Time:   metav1.NewTime(time.Unix(2, 0)),
			Phase:  scrutineerv1alpha1.PolicyDecisionPhaseRuntime,
			Type:   "summary",
			Reason: "DecisionsTruncated",
		},
	}
	got := AppendRuntimeDecisions(existing, []scrutineerv1alpha1.PolicyDecision{
		netDecision(scrutineerv1alpha1.PolicyDecisionDeny, "new.example", 3),
	}, MaxPolicyDecisions)

	summaries := 0
	for _, d := range got {
		if isTruncationSummary(d) {
			summaries++
		}
	}
	if summaries != 0 {
		t.Fatalf("stale summary not dropped (no truncation this pass): %+v", got)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (old allow + new deny, stale summary removed)", len(got))
	}
}
