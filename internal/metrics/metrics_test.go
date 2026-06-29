/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
)

func TestAgentSessionCollector_updatesGauges(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	utilruntime.Must(scrutineerv1alpha1.AddToScheme(scheme))

	session := scrutineerv1alpha1.AgentSession{
		ObjectMeta: metav1.ObjectMeta{Name: "s1", Namespace: "team-a"},
		Status: scrutineerv1alpha1.AgentSessionStatus{
			Phase: scrutineerv1alpha1.PhaseRunning,
			Violations: []scrutineerv1alpha1.PolicyViolation{{
				Type: "network", Target: "evil.example",
			}},
		},
	}
	// One pending ApprovalRequest counts toward the queue; a granted one does not.
	pending := scrutineerv1alpha1.ApprovalRequest{
		ObjectMeta: metav1.ObjectMeta{Name: "s1", Namespace: "team-a"},
		Status:     scrutineerv1alpha1.ApprovalRequestStatus{State: scrutineerv1alpha1.ApprovalStatePending},
	}
	granted := scrutineerv1alpha1.ApprovalRequest{
		ObjectMeta: metav1.ObjectMeta{Name: "s2", Namespace: "team-a"},
		Status:     scrutineerv1alpha1.ApprovalRequestStatus{State: scrutineerv1alpha1.ApprovalStateGranted},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&scrutineerv1alpha1.AgentSession{}, &scrutineerv1alpha1.ApprovalRequest{}).
		WithLists(
			&scrutineerv1alpha1.AgentSessionList{Items: []scrutineerv1alpha1.AgentSession{session}},
			&scrutineerv1alpha1.ApprovalRequestList{Items: []scrutineerv1alpha1.ApprovalRequest{pending, granted}},
		).
		Build()

	reg := prometheus.NewRegistry()
	reg.MustRegister(&AgentSessionCollector{Client: cl})

	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}

	if got := gaugeValue(families, "scrutineer_agentsessions", map[string]string{"namespace": "team-a", "phase": "Running"}); got != 1 {
		t.Fatalf("running sessions = %v, want 1", got)
	}
	if got := gaugeValue(families, "scrutineer_agentsession_violations", map[string]string{"namespace": "team-a"}); got != 1 {
		t.Fatalf("violations = %v, want 1", got)
	}
	if got := gaugeValue(families, "scrutineer_approval_queue_depth", nil); got != 1 {
		t.Fatalf("approval queue = %v, want 1", got)
	}
}

func TestIsPendingApproval(t *testing.T) {
	t.Parallel()
	for _, s := range []scrutineerv1alpha1.ApprovalState{scrutineerv1alpha1.ApprovalStatePending, ""} {
		if !isPendingApproval(s) {
			t.Fatalf("state %q should be pending", s)
		}
	}
	for _, s := range []scrutineerv1alpha1.ApprovalState{
		scrutineerv1alpha1.ApprovalStateGranted,
		scrutineerv1alpha1.ApprovalStateDenied,
		scrutineerv1alpha1.ApprovalStateExpired,
	} {
		if isPendingApproval(s) {
			t.Fatalf("state %q should not be pending", s)
		}
	}
}

func TestObserveHelpers_doNotPanic(t *testing.T) {
	ObserveRuntimeReport("accepted", 0)
	ObserveNovelViolations("ns1", []string{"network"})
}

func gaugeValue(families []*dto.MetricFamily, name string, labels map[string]string) float64 {
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			if labelsMatch(metric, labels) {
				return metric.GetGauge().GetValue()
			}
		}
	}
	return 0
}

func labelsMatch(metric *dto.Metric, want map[string]string) bool {
	if len(want) == 0 && len(metric.GetLabel()) == 0 {
		return true
	}
	got := make(map[string]string, len(metric.GetLabel()))
	for _, lp := range metric.GetLabel() {
		got[lp.GetName()] = lp.GetValue()
	}
	if len(got) != len(want) {
		return false
	}
	for k, v := range want {
		if got[k] != v {
			return false
		}
	}
	return true
}
