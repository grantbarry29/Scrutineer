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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
)

func sessionWithCondition(condType, reason, msg string, status metav1.ConditionStatus) *scrutineerv1alpha1.AgentSession {
	s := &scrutineerv1alpha1.AgentSession{}
	if condType != "" {
		setCondition(s, condType, status, reason, msg)
	}
	return s
}

func TestConditionChanged(t *testing.T) {
	const ct = ConditionPolicyResolved

	cases := []struct {
		name           string
		snapshot, curr *scrutineerv1alpha1.AgentSession
		want           bool
	}{
		{
			name:     "absent on current is never a change",
			snapshot: sessionWithCondition(ct, "PoliciesMerged", "merged 1", metav1.ConditionTrue),
			curr:     sessionWithCondition("", "", "", metav1.ConditionTrue),
			want:     false,
		},
		{
			name:     "newly added condition is a change",
			snapshot: sessionWithCondition("", "", "", metav1.ConditionTrue),
			curr:     sessionWithCondition(ct, "PoliciesMerged", "merged 1", metav1.ConditionTrue),
			want:     true,
		},
		{
			name:     "identical condition is not a change",
			snapshot: sessionWithCondition(ct, "PoliciesMerged", "merged 1", metav1.ConditionTrue),
			curr:     sessionWithCondition(ct, "PoliciesMerged", "merged 1", metav1.ConditionTrue),
			want:     false,
		},
		{
			name:     "message change is a change",
			snapshot: sessionWithCondition(ct, "PoliciesMerged", "merged 1", metav1.ConditionTrue),
			curr:     sessionWithCondition(ct, "PoliciesMerged", "merged 2", metav1.ConditionTrue),
			want:     true,
		},
		{
			name:     "reason change is a change",
			snapshot: sessionWithCondition(ct, "PoliciesMerged", "merged 1", metav1.ConditionTrue),
			curr:     sessionWithCondition(ct, "NoPolicies", "merged 1", metav1.ConditionTrue),
			want:     true,
		},
		{
			name:     "status change is a change",
			snapshot: sessionWithCondition(ct, "PoliciesMerged", "merged 1", metav1.ConditionTrue),
			curr:     sessionWithCondition(ct, "PoliciesMerged", "merged 1", metav1.ConditionFalse),
			want:     true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := conditionChanged(tc.snapshot, tc.curr, ct); got != tc.want {
				t.Fatalf("conditionChanged = %v, want %v", got, tc.want)
			}
		})
	}
}
