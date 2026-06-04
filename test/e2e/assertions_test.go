//go:build e2e

/*
Copyright 2026 The Relay Authors.

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

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
	"github.com/secureai/relay/internal/controller"
)

func waitForPhase(ctx context.Context, key client.ObjectKey, want []relayv1alpha1.AgentSessionPhase, timeout, poll time.Duration) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		var got relayv1alpha1.AgentSession
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

func waitForTerminalPhase(ctx context.Context, key client.ObjectKey, phase relayv1alpha1.AgentSessionPhase) {
	waitForPhase(ctx, key, []relayv1alpha1.AgentSessionPhase{phase}, terminalPhaseTimeout, terminalPhasePoll)
}

func waitForDeniedPhase(ctx context.Context, key client.ObjectKey) {
	waitForPhase(ctx, key, []relayv1alpha1.AgentSessionPhase{relayv1alpha1.PhaseDenied}, deniedPhaseTimeout, deniedPhasePoll)
}

func getSession(ctx context.Context, key client.ObjectKey) relayv1alpha1.AgentSession {
	GinkgoHelper()
	var got relayv1alpha1.AgentSession
	Expect(k8sClient.Get(ctx, key, &got)).To(Succeed())
	return got
}

func getCondition(s *relayv1alpha1.AgentSession, condType string) *metav1.Condition {
	for i := range s.Status.Conditions {
		if s.Status.Conditions[i].Type == condType {
			return &s.Status.Conditions[i]
		}
	}
	return nil
}

func expectCondition(s *relayv1alpha1.AgentSession, condType string, status metav1.ConditionStatus, reason string) {
	cond := getCondition(s, condType)
	Expect(cond).NotTo(BeNil(), "condition %s missing on %s", condType, s.Name)
	Expect(cond.Status).To(Equal(status))
	if reason != "" {
		Expect(cond.Reason).To(Equal(reason))
	}
}

func expectJobForSession(ctx context.Context, ns string, session *relayv1alpha1.AgentSession) *batchv1.Job {
	GinkgoHelper()
	name := jobNameForSession(session)
	var job batchv1.Job
	Eventually(func() error {
		return k8sClient.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, &job)
	}, 30*time.Second, 500*time.Millisecond).Should(Succeed(), "Job %s/%s never appeared", ns, name)
	return &job
}

func expectJobGoneForSession(ctx context.Context, ns string, session *relayv1alpha1.AgentSession) {
	GinkgoHelper()
	name := jobNameForSession(session)
	Eventually(func(g Gomega) {
		err := k8sClient.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, &batchv1.Job{})
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
	}, 30*time.Second, 500*time.Millisecond).Should(Succeed(), "Job %s/%s should have been deleted", ns, name)
}

func expectNoJobForSession(ctx context.Context, ns string, session *relayv1alpha1.AgentSession) {
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
	var got relayv1alpha1.AgentSession
	Expect(k8sClient.Get(ctx, key, &got)).To(Succeed())
	got.Spec.CancelRequested = true
	Expect(k8sClient.Update(ctx, &got)).To(Succeed())
}

func expectTimedOutStatus(got *relayv1alpha1.AgentSession) {
	Expect(got.Status.Phase).To(Equal(relayv1alpha1.PhaseTimedOut))
	Expect(got.Status.CompletionTime).NotTo(BeNil())
	expectCondition(got, controller.ConditionCompleted, metav1.ConditionFalse, "JobTimedOut")
	if got.Status.Result != nil {
		Expect(got.Status.Result.Outcome).To(Equal("failed"))
	}
}

func expectCancelledStatus(got *relayv1alpha1.AgentSession) {
	Expect(got.Status.Phase).To(Equal(relayv1alpha1.PhaseCancelled))
	Expect(got.Status.CompletionTime).NotTo(BeNil())
	Expect(got.Status.Result).NotTo(BeNil())
	Expect(got.Status.Result.Outcome).To(Equal("cancelled"))
	expectCondition(got, controller.ConditionCompleted, metav1.ConditionTrue, "SessionCancelled")
}

func containerEnvValue(job *batchv1.Job, envName string) string {
	for _, e := range job.Spec.Template.Spec.Containers[0].Env {
		if e.Name == envName {
			return e.Value
		}
	}
	return ""
}

func expectDeniedTask(got *relayv1alpha1.AgentSession, msgSubstring string) {
	expectCondition(got, controller.ConditionValidated, metav1.ConditionFalse, "InvalidTask")
	if msgSubstring != "" {
		cond := getCondition(got, controller.ConditionValidated)
		Expect(cond.Message).To(ContainSubstring(msgSubstring))
	}
}
