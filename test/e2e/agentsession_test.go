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
			session := newAgentSession(ns, "happy",
				// Container exits 0 quickly so the test is fast.
				withCommand("sh", "-c", "echo running; exit 0"),
			)
			key := client.ObjectKeyFromObject(session)

			By("creating the AgentSession")
			Expect(k8sClient.Create(ctx, session)).To(Succeed())

			By("waiting for Phase=Succeeded")
			waitForPhase(ctx, key, []relayv1alpha1.AgentSessionPhase{
				relayv1alpha1.PhaseSucceeded,
			}, terminalPhaseTimeout, terminalPhasePoll)

			By("inspecting the final status")
			var got relayv1alpha1.AgentSession
			Expect(k8sClient.Get(ctx, key, &got)).To(Succeed())

			Expect(got.Status.JobName).NotTo(BeEmpty())
			Expect(got.Status.PodName).NotTo(BeEmpty())

			By("verifying status.podName references an existing Pod for the Job")
			var pod corev1.Pod
			Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: ns, Name: got.Status.PodName}, &pod)).To(Succeed())
			Expect(pod.Labels[controller.LabelSessionRef]).To(Equal(session.Name))

			Expect(got.Status.StartTime).NotTo(BeNil())
			Expect(got.Status.CompletionTime).NotTo(BeNil())
			Expect(got.Status.Result).NotTo(BeNil())
			Expect(got.Status.Result.Outcome).To(Equal("completed"))

			By("checking conditions: Validated=True, RuntimeCreated=True, Completed=True")
			validated := getCondition(&got, controller.ConditionValidated)
			Expect(validated).NotTo(BeNil())
			Expect(validated.Status).To(Equal(metav1.ConditionTrue))

			runtimeCreated := getCondition(&got, controller.ConditionRuntimeCreated)
			Expect(runtimeCreated).NotTo(BeNil())
			Expect(runtimeCreated.Status).To(Equal(metav1.ConditionTrue))

			completed := getCondition(&got, controller.ConditionCompleted)
			Expect(completed).NotTo(BeNil())
			Expect(completed.Status).To(Equal(metav1.ConditionTrue))
			Expect(completed.Reason).To(Equal("JobSucceeded"))

			By("verifying the Job exists and is owned by the AgentSession")
			job := expectJobExists(ctx, ns, got.Status.JobName)
			Expect(job.OwnerReferences).NotTo(BeEmpty())
			Expect(job.OwnerReferences[0].UID).To(Equal(got.UID))
			Expect(job.OwnerReferences[0].Kind).To(Equal("AgentSession"))
		})
	})

	Context("failure path", func() {
		It("marks a non-zero exit as Phase=Failed", func(ctx SpecContext) {
			ns := newTestNamespace("relay-e2e-fail")
			session := newAgentSession(ns, "fails",
				withCommand("sh", "-c", "echo nope; exit 1"),
			)
			key := client.ObjectKeyFromObject(session)

			By("creating the AgentSession")
			Expect(k8sClient.Create(ctx, session)).To(Succeed())

			By("waiting for Phase=Failed")
			waitForPhase(ctx, key, []relayv1alpha1.AgentSessionPhase{
				relayv1alpha1.PhaseFailed,
			}, terminalPhaseTimeout, terminalPhasePoll)

			By("inspecting the final status")
			var got relayv1alpha1.AgentSession
			Expect(k8sClient.Get(ctx, key, &got)).To(Succeed())

			Expect(got.Status.Result).NotTo(BeNil())
			Expect(got.Status.Result.Outcome).To(Equal("failed"))
			Expect(got.Status.PodName).NotTo(BeEmpty())

			completed := getCondition(&got, controller.ConditionCompleted)
			Expect(completed).NotTo(BeNil())
			Expect(completed.Status).To(Equal(metav1.ConditionFalse))
			Expect(completed.Reason).To(Equal("JobFailed"))
		})
	})

	Context("denied path (controller-side validation)", func() {
		It("rejects a spec with empty task and never creates a Job", func(ctx SpecContext) {
			ns := newTestNamespace("relay-e2e-deny")
			// withoutTask() clears the Task field. apiserver-side validation
			// allows this (Task is optional at the OpenAPI level), so the
			// controller's validateSpec() is the gate that catches it.
			session := newAgentSession(ns, "denied", withoutTask())
			key := client.ObjectKeyFromObject(session)

			By("creating the AgentSession")
			Expect(k8sClient.Create(ctx, session)).To(Succeed())

			By("waiting for Phase=Denied (fast — happens on first reconcile)")
			waitForPhase(ctx, key, []relayv1alpha1.AgentSessionPhase{
				relayv1alpha1.PhaseDenied,
			}, deniedPhaseTimeout, deniedPhasePoll)

			By("inspecting the denial reason on Validated=False")
			var got relayv1alpha1.AgentSession
			Expect(k8sClient.Get(ctx, key, &got)).To(Succeed())
			validated := getCondition(&got, controller.ConditionValidated)
			Expect(validated).NotTo(BeNil())
			Expect(validated.Status).To(Equal(metav1.ConditionFalse))
			Expect(validated.Reason).To(Equal("InvalidSpec"))
			Expect(validated.Message).To(ContainSubstring("task.description or spec.task.prompt"))

			By("verifying no Job was created")
			expectNoJob(ctx, ns, "relay-session-denied")
		})

		It("denies when promptConfigMapRef points to a missing ConfigMap", func(ctx SpecContext) {
			ns := newTestNamespace("relay-e2e-deny-cm")
			session := newAgentSession(ns, "denied-missing-cm",
				withPromptConfigMapRef("does-not-exist", "instructions"),
			)
			key := client.ObjectKeyFromObject(session)

			Expect(k8sClient.Create(ctx, session)).To(Succeed())

			waitForPhase(ctx, key, []relayv1alpha1.AgentSessionPhase{
				relayv1alpha1.PhaseDenied,
			}, deniedPhaseTimeout, deniedPhasePoll)

			var got relayv1alpha1.AgentSession
			Expect(k8sClient.Get(ctx, key, &got)).To(Succeed())
			validated := getCondition(&got, controller.ConditionValidated)
			Expect(validated).NotTo(BeNil())
			Expect(validated.Status).To(Equal(metav1.ConditionFalse))
			Expect(validated.Reason).To(Equal("InvalidTask"))
			Expect(validated.Message).To(ContainSubstring("ConfigMap"))

			expectNoJob(ctx, ns, "relay-session-denied-missing-cm")
		})

		It("denies when promptConfigMapRef key is missing from the ConfigMap", func(ctx SpecContext) {
			ns := newTestNamespace("relay-e2e-deny-cm-key")
			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "agent-prompt", Namespace: ns},
				Data:       map[string]string{"other": "value"},
			}
			Expect(k8sClient.Create(ctx, cm)).To(Succeed())

			session := newAgentSession(ns, "denied-missing-key",
				withPromptConfigMapRef("agent-prompt", "instructions"),
			)
			key := client.ObjectKeyFromObject(session)

			Expect(k8sClient.Create(ctx, session)).To(Succeed())

			waitForPhase(ctx, key, []relayv1alpha1.AgentSessionPhase{
				relayv1alpha1.PhaseDenied,
			}, deniedPhaseTimeout, deniedPhasePoll)

			var got relayv1alpha1.AgentSession
			Expect(k8sClient.Get(ctx, key, &got)).To(Succeed())
			validated := getCondition(&got, controller.ConditionValidated)
			Expect(validated).NotTo(BeNil())
			Expect(validated.Status).To(Equal(metav1.ConditionFalse))
			Expect(validated.Reason).To(Equal("InvalidTask"))
			Expect(validated.Message).To(ContainSubstring("instructions"))

			expectNoJob(ctx, ns, "relay-session-denied-missing-key")
		})
	})

	Context("admission-time validation (CRD pattern)", func() {
		It("rejects an out-of-range temperature at apiserver Create", func(ctx SpecContext) {
			ns := newTestNamespace("relay-e2e-admit")
			session := newAgentSession(ns, "bad-temp", withTemperature("2.5"))

			By("attempting to create — should fail with an Invalid error before reaching the controller")
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
				withCommand("sh", "-c", "exit 0"),
			)
			key := client.ObjectKeyFromObject(session)
			Expect(k8sClient.Create(ctx, session)).To(Succeed())

			waitForPhase(ctx, key, []relayv1alpha1.AgentSessionPhase{
				relayv1alpha1.PhaseSucceeded,
			}, terminalPhaseTimeout, terminalPhasePoll)

			var got relayv1alpha1.AgentSession
			Expect(k8sClient.Get(ctx, key, &got)).To(Succeed())
			job := expectJobExists(ctx, ns, got.Status.JobName)
			var prompt string
			for _, e := range job.Spec.Template.Spec.Containers[0].Env {
				if e.Name == controller.EnvTaskPrompt {
					prompt = e.Value
					break
				}
			}
			Expect(prompt).To(Equal("prompt from configmap"))
		})

		It("accepts a valid string-encoded temperature", func(ctx SpecContext) {
			ns := newTestNamespace("relay-e2e-temp-ok")
			session := newAgentSession(ns, "good-temp",
				withTemperature("0.7"),
				withCommand("sh", "-c", "exit 0"),
			)
			key := client.ObjectKeyFromObject(session)

			Expect(k8sClient.Create(ctx, session)).To(Succeed())

			waitForPhase(ctx, key, []relayv1alpha1.AgentSessionPhase{
				relayv1alpha1.PhaseSucceeded,
			}, terminalPhaseTimeout, terminalPhasePoll)

			By("confirming temperature round-tripped through the apiserver")
			var got relayv1alpha1.AgentSession
			Expect(k8sClient.Get(ctx, key, &got)).To(Succeed())
			Expect(got.Spec.Model.Temperature).NotTo(BeNil())
			Expect(*got.Spec.Model.Temperature).To(Equal("0.7"))
		})
	})
})
