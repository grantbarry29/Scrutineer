//go:build e2e

/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package e2e

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	batchv1 "k8s.io/api/batch/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/controller/agentsession"
)

func waitForPhase(ctx context.Context, key client.ObjectKey, want []scrutineerv1alpha1.AgentSessionPhase, timeout, poll time.Duration) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		var got scrutineerv1alpha1.AgentSession
		g.Expect(k8sClient.Get(ctx, key, &got)).To(Succeed())
		for _, p := range want {
			if got.Status.Phase == p {
				return
			}
		}
		g.Expect(got.Status.Phase).To(BeElementOf(want),
			"AgentSession %s has phase %q (want one of %v); status: %+v",
			key, got.Status.Phase, want, got.Status)
	}, timeout, poll).Should(Succeed())
}

func waitForTerminalPhase(ctx context.Context, key client.ObjectKey, phase scrutineerv1alpha1.AgentSessionPhase) {
	waitForPhase(ctx, key, []scrutineerv1alpha1.AgentSessionPhase{phase}, terminalPhaseTimeout, terminalPhasePoll)
}

func waitForDeniedPhase(ctx context.Context, key client.ObjectKey) {
	waitForPhase(ctx, key, []scrutineerv1alpha1.AgentSessionPhase{scrutineerv1alpha1.PhaseDenied}, deniedPhaseTimeout, deniedPhasePoll)
}

func getSession(ctx context.Context, key client.ObjectKey) scrutineerv1alpha1.AgentSession {
	GinkgoHelper()
	var got scrutineerv1alpha1.AgentSession
	Expect(k8sClient.Get(ctx, key, &got)).To(Succeed())
	return got
}

func getCondition(s *scrutineerv1alpha1.AgentSession, condType string) *metav1.Condition {
	for i := range s.Status.Conditions {
		if s.Status.Conditions[i].Type == condType {
			return &s.Status.Conditions[i]
		}
	}
	return nil
}

func expectCondition(s *scrutineerv1alpha1.AgentSession, condType string, status metav1.ConditionStatus, reason string) {
	cond := getCondition(s, condType)
	Expect(cond).NotTo(BeNil(), "condition %s missing on %s", condType, s.Name)
	Expect(cond.Status).To(Equal(status))
	if reason != "" {
		Expect(cond.Reason).To(Equal(reason))
	}
}

func expectJobForSession(ctx context.Context, ns string, session *scrutineerv1alpha1.AgentSession) *batchv1.Job {
	GinkgoHelper()
	name := jobNameForSession(session)
	var job batchv1.Job
	Eventually(func() error {
		return k8sClient.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, &job)
	}, 30*time.Second, 500*time.Millisecond).Should(Succeed(), "Job %s/%s never appeared", ns, name)
	return &job
}

func expectJobGoneForSession(ctx context.Context, ns string, session *scrutineerv1alpha1.AgentSession) {
	GinkgoHelper()
	name := jobNameForSession(session)
	Eventually(func(g Gomega) {
		err := k8sClient.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, &batchv1.Job{})
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
	}, 30*time.Second, 500*time.Millisecond).Should(Succeed(), "Job %s/%s should have been deleted", ns, name)
}

func expectNoJobForSession(ctx context.Context, ns string, session *scrutineerv1alpha1.AgentSession) {
	GinkgoHelper()
	name := jobNameForSession(session)
	Consistently(func() bool {
		err := k8sClient.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, &batchv1.Job{})
		return apierrors.IsNotFound(err)
	}, 5*time.Second, 500*time.Millisecond).Should(BeTrue(),
		"Job %s/%s should not exist for AgentSession %s", ns, name, session.Name)
}

func requestCancellation(ctx context.Context, key client.ObjectKey) {
	GinkgoHelper()
	// The in-process reconciler updates status concurrently; a bare Get+Update races
	// on resourceVersion and returns 409 Conflict on slow CI.
	Eventually(func(g Gomega) {
		var got scrutineerv1alpha1.AgentSession
		g.Expect(k8sClient.Get(ctx, key, &got)).To(Succeed())
		if got.Spec.CancelRequested {
			return
		}
		got.Spec.CancelRequested = true
		g.Expect(k8sClient.Update(ctx, &got)).To(Succeed())
	}, 15*time.Second, 200*time.Millisecond).Should(Succeed())
}

func expectTimedOutStatus(got *scrutineerv1alpha1.AgentSession) {
	Expect(got.Status.Phase).To(Equal(scrutineerv1alpha1.PhaseTimedOut))
	Expect(got.Status.CompletionTime).NotTo(BeNil())
	expectCondition(got, agentsession.ConditionCompleted, metav1.ConditionFalse, "JobTimedOut")
	if got.Status.Result != nil {
		Expect(got.Status.Result.Outcome).To(Equal("failed"))
	}
}

func expectCancelledStatus(got *scrutineerv1alpha1.AgentSession) {
	Expect(got.Status.Phase).To(Equal(scrutineerv1alpha1.PhaseCancelled))
	Expect(got.Status.CompletionTime).NotTo(BeNil())
	Expect(got.Status.Result).NotTo(BeNil())
	Expect(got.Status.Result.Outcome).To(Equal("cancelled"))
	expectCondition(got, agentsession.ConditionCompleted, metav1.ConditionTrue, "SessionCancelled")
}

func containerEnvValue(job *batchv1.Job, envName string) string {
	for _, e := range job.Spec.Template.Spec.Containers[0].Env {
		if e.Name == envName {
			return e.Value
		}
	}
	return ""
}

func expectDeniedTask(got *scrutineerv1alpha1.AgentSession, msgSubstring string) {
	expectCondition(got, agentsession.ConditionValidated, metav1.ConditionFalse, "InvalidTask")
	if msgSubstring != "" {
		cond := getCondition(got, agentsession.ConditionValidated)
		Expect(cond.Message).To(ContainSubstring(msgSubstring))
	}
}
