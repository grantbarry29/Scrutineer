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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
	"github.com/secureai/relay/internal/controller"
)

var _ = Describe("AgentSession e2e against kind", func() {

	Context("happy path", func() {
		It("drives a session through Pending -> Running -> Succeeded", func(ctx SpecContext) {
			ns := newTestNamespace("relay-e2e-happy")
			session := newAgentSession(ns, "happy", withCommand("sh", "-c", "echo running; exit 0"))
			key := createAgentSession(ctx, session)

			waitForTerminalPhase(ctx, key, relayv1alpha1.PhaseSucceeded)

			got := getSession(ctx, key)
			Expect(got.Status.JobName).To(Equal(jobNameForSession(session)))
			Expect(got.Status.PodName).NotTo(BeEmpty())

			var pod corev1.Pod
			Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: ns, Name: got.Status.PodName}, &pod)).To(Succeed())
			Expect(pod.Labels[controller.LabelSessionRef]).To(Equal(session.Name))

			Expect(got.Status.StartTime).NotTo(BeNil())
			Expect(got.Status.CompletionTime).NotTo(BeNil())
			Expect(got.Status.Result).NotTo(BeNil())
			Expect(got.Status.Result.Outcome).To(Equal("completed"))

			expectCondition(&got, controller.ConditionValidated, metav1.ConditionTrue, "SpecValid")
			expectCondition(&got, controller.ConditionRuntimeCreated, metav1.ConditionTrue, "JobCreated")
			expectCondition(&got, controller.ConditionCompleted, metav1.ConditionTrue, "JobSucceeded")

			job := expectJobForSession(ctx, ns, session)
			Expect(job.OwnerReferences[0].UID).To(Equal(got.UID))
			Expect(job.OwnerReferences[0].Kind).To(Equal("AgentSession"))
		})
	})

	Context("timeout path", func() {
		It("marks Phase=TimedOut when the Job exceeds activeDeadlineSeconds", func(ctx SpecContext) {
			ns := newTestNamespace("relay-e2e-timeout")
			session := newAgentSession(ns, "timed-out",
				withTimeoutSeconds(5),
				withSleepExceedingTimeout(),
			)
			key := createAgentSession(ctx, session)

			waitForTerminalPhase(ctx, key, relayv1alpha1.PhaseTimedOut)

			got := getSession(ctx, key)
			expectTimedOutStatus(&got)
			expectJobForSession(ctx, ns, session)
		})
	})

	Context("failure path", func() {
		It("marks a non-zero exit as Phase=Failed", func(ctx SpecContext) {
			ns := newTestNamespace("relay-e2e-fail")
			session := newAgentSession(ns, "fails", withExitCommand(1))
			key := createAgentSession(ctx, session)

			waitForTerminalPhase(ctx, key, relayv1alpha1.PhaseFailed)

			got := getSession(ctx, key)
			Expect(got.Status.Result.Outcome).To(Equal("failed"))
			Expect(got.Status.PodName).NotTo(BeEmpty())
			expectCondition(&got, controller.ConditionCompleted, metav1.ConditionFalse, "JobFailed")
		})
	})

	Context("denied path (controller-side validation)", func() {
		It("rejects a spec with empty task and never creates a Job", func(ctx SpecContext) {
			ns := newTestNamespace("relay-e2e-deny")
			session := newAgentSession(ns, "denied", withoutTask())
			key := createAgentSession(ctx, session)

			waitForDeniedPhase(ctx, key)

			got := getSession(ctx, key)
			expectCondition(&got, controller.ConditionValidated, metav1.ConditionFalse, "InvalidSpec")
			Expect(getCondition(&got, controller.ConditionValidated).Message).
				To(ContainSubstring("task.description or spec.task.prompt"))

			expectNoJobForSession(ctx, ns, session)
		})

		It("denies when promptConfigMapRef points to a missing ConfigMap", func(ctx SpecContext) {
			ns := newTestNamespace("relay-e2e-deny-cm")
			session := newAgentSession(ns, "denied-missing-cm",
				withPromptConfigMapRef("does-not-exist", "instructions"))
			key := createAgentSession(ctx, session)

			waitForDeniedPhase(ctx, key)

			got := getSession(ctx, key)
			expectDeniedTask(&got, "ConfigMap")
			expectNoJobForSession(ctx, ns, session)
		})

		It("denies when promptConfigMapRef key is missing from the ConfigMap", func(ctx SpecContext) {
			ns := newTestNamespace("relay-e2e-deny-cm-key")
			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "agent-prompt", Namespace: ns},
				Data:       map[string]string{"other": "value"},
			}
			Expect(k8sClient.Create(ctx, cm)).To(Succeed())

			session := newAgentSession(ns, "denied-missing-key",
				withPromptConfigMapRef("agent-prompt", "instructions"))
			key := createAgentSession(ctx, session)

			waitForDeniedPhase(ctx, key)

			got := getSession(ctx, key)
			expectDeniedTask(&got, "instructions")
			expectNoJobForSession(ctx, ns, session)
		})
	})

	Context("admission-time validation (CRD pattern)", func() {
		It("rejects an out-of-range temperature at apiserver Create", func(ctx SpecContext) {
			ns := newTestNamespace("relay-e2e-admit")
			session := newAgentSession(ns, "bad-temp", withTemperature("2.5"))

			err := k8sClient.Create(ctx, session)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("spec.model.temperature"))
		})

		It("loads the task prompt from a ConfigMap when promptConfigMapRef is set", func(ctx SpecContext) {
			ns := newTestNamespace("relay-e2e-prompt-cm")
			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "agent-prompt", Namespace: ns},
				Data:       map[string]string{"instructions": "prompt from configmap"},
			}
			Expect(k8sClient.Create(ctx, cm)).To(Succeed())

			session := newAgentSession(ns, "prompt-cm",
				withPromptConfigMapRef("agent-prompt", "instructions"),
				withExitCommand(0),
			)
			key := createAgentSession(ctx, session)

			waitForTerminalPhase(ctx, key, relayv1alpha1.PhaseSucceeded)

			job := expectJobForSession(ctx, ns, session)
			Expect(containerEnvValue(job, controller.EnvTaskPrompt)).To(Equal("prompt from configmap"))
		})

		It("accepts a valid string-encoded temperature", func(ctx SpecContext) {
			ns := newTestNamespace("relay-e2e-temp-ok")
			session := newAgentSession(ns, "good-temp", withTemperature("0.7"), withExitCommand(0))
			key := createAgentSession(ctx, session)

			waitForTerminalPhase(ctx, key, relayv1alpha1.PhaseSucceeded)

			got := getSession(ctx, key)
			Expect(got.Spec.Model.Temperature).NotTo(BeNil())
			Expect(*got.Spec.Model.Temperature).To(Equal("0.7"))
		})
	})

	Context("cancellation", func() {
		It("deletes the owned Job and reaches Phase=Cancelled when cancelRequested is set", func(ctx SpecContext) {
			ns := newTestNamespace("relay-e2e-cancel")
			session := newAgentSession(ns, "cancelled", withLongRunningCommand())
			key := createAgentSession(ctx, session)

			expectJobForSession(ctx, ns, session)
			requestCancellation(ctx, key)

			waitForTerminalPhase(ctx, key, relayv1alpha1.PhaseCancelled)
			expectJobGoneForSession(ctx, ns, session)

			got := getSession(ctx, key)
			expectCancelledStatus(&got)
		})

		It("reaches Phase=Cancelled immediately when created with cancelRequested", func(ctx SpecContext) {
			ns := newTestNamespace("relay-e2e-cancel-at-create")
			session := newAgentSession(ns, "cancel-at-create", withCancelRequested())
			key := createAgentSession(ctx, session)

			waitForTerminalPhase(ctx, key, relayv1alpha1.PhaseCancelled)
			expectNoJobForSession(ctx, ns, session)

			got := getSession(ctx, key)
			expectCancelledStatus(&got)
		})
	})
})
