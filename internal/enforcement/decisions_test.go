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
