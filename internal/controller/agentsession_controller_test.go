/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package controller

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
)

func testReconciler() *AgentSessionReconciler {
	return &AgentSessionReconciler{
		Client:   k8sClient,
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("relay-test"),
	}
}

var _ = Describe("AgentSession reconciler", func() {

	Context("validation and denial", func() {
		It("denies a session with an empty task and does not create a Job", func() {
			ns := newTestNamespace()
			session := minimalAgentSession(ns, "denied-empty-task")
			session.Spec.Task = relayv1alpha1.SessionTaskSpec{}

			Expect(k8sClient.Create(testCtx, session)).To(Succeed())
			key := client.ObjectKeyFromObject(session)

			waitForPhase(key, relayv1alpha1.PhaseDenied)

			var got relayv1alpha1.AgentSession
			Expect(k8sClient.Get(testCtx, key, &got)).To(Succeed())
			validated := getCondition(&got, ConditionValidated)
			Expect(validated).NotTo(BeNil())
			Expect(validated.Status).To(Equal(metav1.ConditionFalse))

			expectJobAbsent(ns, session)
		})

		It("denies when promptConfigMapRef points to a missing ConfigMap", func() {
			ns := newTestNamespace()
			session := minimalAgentSession(ns, "denied-missing-cm")
			session.Spec.Task = relayv1alpha1.SessionTaskSpec{
				PromptConfigMapRef: &relayv1alpha1.PromptConfigMapRef{
					Name: "does-not-exist",
					Key:  "prompt",
				},
			}

			Expect(k8sClient.Create(testCtx, session)).To(Succeed())
			key := client.ObjectKeyFromObject(session)

			waitForPhase(key, relayv1alpha1.PhaseDenied)

			var got relayv1alpha1.AgentSession
			Expect(k8sClient.Get(testCtx, key, &got)).To(Succeed())
			validated := getCondition(&got, ConditionValidated)
			Expect(validated).NotTo(BeNil())
			Expect(validated.Reason).To(Equal("InvalidTask"))
			Expect(validated.Message).To(ContainSubstring("ConfigMap"))
		})
	})

	Context("Job reconciliation", func() {
		It("creates an owned Job with relay labels and env vars", func() {
			ns := newTestNamespace()
			session := minimalAgentSession(ns, "creates-job")
			Expect(k8sClient.Create(testCtx, session)).To(Succeed())
			key := client.ObjectKeyFromObject(session)

			waitForJob(ns, session)

			var job batchv1.Job
			Expect(k8sClient.Get(testCtx, types.NamespacedName{Namespace: ns, Name: jobNameFor(session)}, &job)).To(Succeed())
			Expect(job.Labels[LabelSessionRef]).To(Equal(session.Name))
			Expect(job.OwnerReferences[0].Kind).To(Equal("AgentSession"))

			env := envMap(job.Spec.Template.Spec.Containers[0].Env)
			Expect(env[EnvTaskPrompt]).To(Equal("run the task"))
			Expect(env[EnvRelaySessionName]).To(Equal(session.Name))

			var got relayv1alpha1.AgentSession
			Expect(k8sClient.Get(testCtx, key, &got)).To(Succeed())
			runtimeCreated := getCondition(&got, ConditionRuntimeCreated)
			Expect(runtimeCreated.Status).To(Equal(metav1.ConditionTrue))
		})

		It("marks Succeeded when the Job completes and retains RuntimeCreated", func() {
			ns := newTestNamespace()
			session := minimalAgentSession(ns, "job-succeeds")
			Expect(k8sClient.Create(testCtx, session)).To(Succeed())
			key := client.ObjectKeyFromObject(session)

			waitForJob(ns, session)

			var job batchv1.Job
			Expect(k8sClient.Get(testCtx, types.NamespacedName{Namespace: ns, Name: jobNameFor(session)}, &job)).To(Succeed())
			setJobSucceeded(&job)

			waitForPhase(key, relayv1alpha1.PhaseSucceeded)

			var got relayv1alpha1.AgentSession
			Expect(k8sClient.Get(testCtx, key, &got)).To(Succeed())
			Expect(got.Status.Result.Outcome).To(Equal("completed"))
			runtimeCond := getCondition(&got, ConditionRuntimeCreated)
			Expect(runtimeCond.Status).To(Equal(metav1.ConditionTrue))
			completed := getCondition(&got, ConditionCompleted)
			Expect(completed.Reason).To(Equal("JobSucceeded"))
		})

		It("sets status.podName to the newest Pod owned by the Job", func() {
			ns := newTestNamespace()
			session := minimalAgentSession(ns, "pod-name")
			Expect(k8sClient.Create(testCtx, session)).To(Succeed())
			key := client.ObjectKeyFromObject(session)

			waitForJob(ns, session)

			var job batchv1.Job
			Expect(k8sClient.Get(testCtx, types.NamespacedName{Namespace: ns, Name: jobNameFor(session)}, &job)).To(Succeed())

			ownerRef := metav1.OwnerReference{
				APIVersion: batchv1.SchemeGroupVersion.String(),
				Kind:       "Job",
				Name:       job.Name,
				UID:        job.UID,
			}
			podLabels := map[string]string{LabelSessionRef: session.Name}

			podOlder := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "relay-session-pod-name-older", Namespace: ns,
					Labels: podLabels, OwnerReferences: []metav1.OwnerReference{ownerRef},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: AgentContainerName, Image: "busybox:latest"}},
				},
			}
			Expect(k8sClient.Create(testCtx, podOlder)).To(Succeed())
			time.Sleep(20 * time.Millisecond)

			podNewer := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "relay-session-pod-name-newer", Namespace: ns,
					Labels: podLabels, OwnerReferences: []metav1.OwnerReference{ownerRef},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: AgentContainerName, Image: "busybox:latest"}},
				},
			}
			Expect(k8sClient.Create(testCtx, podNewer)).To(Succeed())

			_, err := testReconciler().Reconcile(testCtx, reconcile.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())

			var got relayv1alpha1.AgentSession
			Expect(k8sClient.Get(testCtx, key, &got)).To(Succeed())
			Expect(got.Status.PodName).To(Equal(podNewer.Name))
		})
	})

	Context("cancellation", func() {
		It("deletes the owned Job when cancelRequested is set", func() {
			ns := newTestNamespace()
			session := minimalAgentSession(ns, "cancel-deletes-job")
			Expect(k8sClient.Create(testCtx, session)).To(Succeed())
			key := client.ObjectKeyFromObject(session)
			jobKey := types.NamespacedName{Namespace: ns, Name: jobNameFor(session)}

			waitForJob(ns, session)

			Eventually(func(g Gomega) {
				var got relayv1alpha1.AgentSession
				g.Expect(k8sClient.Get(testCtx, key, &got)).To(Succeed())
				got.Spec.CancelRequested = true
				g.Expect(k8sClient.Update(testCtx, &got)).To(Succeed())
			}, controllerWaitTimeout, controllerPollInterval).Should(Succeed())

			Eventually(func(g Gomega) {
				_, err := testReconciler().Reconcile(testCtx, reconcile.Request{NamespacedName: key})
				g.Expect(err).NotTo(HaveOccurred())

				var job batchv1.Job
				err = k8sClient.Get(testCtx, jobKey, &job)
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue())

				var got relayv1alpha1.AgentSession
				g.Expect(k8sClient.Get(testCtx, key, &got)).To(Succeed())
				g.Expect(got.Status.Phase).To(Equal(relayv1alpha1.PhaseCancelled))
				g.Expect(got.Status.Result.Outcome).To(Equal("cancelled"))
				completed := getCondition(&got, ConditionCompleted)
				g.Expect(completed.Reason).To(Equal("SessionCancelled"))

				var events corev1.EventList
				g.Expect(k8sClient.List(testCtx, &events, client.InNamespace(ns))).To(Succeed())
				found := false
				for _, ev := range events.Items {
					if ev.Reason == EventReasonSessionCancelled && ev.InvolvedObject.Name == session.Name {
						found = true
						break
					}
				}
				g.Expect(found).To(BeTrue())
			}, controllerWaitTimeout, controllerPollInterval).Should(Succeed())
		})

		It("is idempotent when cancelRequested is set and the Job is already gone", func() {
			ns := newTestNamespace()
			session := minimalAgentSession(ns, "cancel-no-job")
			session.Spec.CancelRequested = true
			Expect(k8sClient.Create(testCtx, session)).To(Succeed())
			key := client.ObjectKeyFromObject(session)

			for i := 0; i < 2; i++ {
				_, err := testReconciler().Reconcile(testCtx, reconcile.Request{NamespacedName: key})
				Expect(err).NotTo(HaveOccurred())
			}

			expectJobAbsent(ns, session)

			var got relayv1alpha1.AgentSession
			Expect(k8sClient.Get(testCtx, key, &got)).To(Succeed())
			expectCancelled(&got)
		})
	})

	Context("promptConfigMapRef", func() {
		It("injects the prompt from the referenced ConfigMap into the Job env", func() {
			ns := newTestNamespace()

			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "agent-prompt", Namespace: ns},
				Data:       map[string]string{"instructions": "prompt loaded from configmap"},
			}
			Expect(k8sClient.Create(testCtx, cm)).To(Succeed())

			session := minimalAgentSession(ns, "prompt-from-cm")
			session.Spec.Task = relayv1alpha1.SessionTaskSpec{
				Description: "uses external prompt",
				PromptConfigMapRef: &relayv1alpha1.PromptConfigMapRef{
					Name: "agent-prompt",
					Key:  "instructions",
				},
			}
			Expect(k8sClient.Create(testCtx, session)).To(Succeed())

			waitForJob(ns, session)

			var job batchv1.Job
			Expect(k8sClient.Get(testCtx, types.NamespacedName{Namespace: ns, Name: jobNameFor(session)}, &job)).To(Succeed())
			env := envMap(job.Spec.Template.Spec.Containers[0].Env)
			Expect(env[EnvTaskPrompt]).To(Equal("prompt loaded from configmap"))
		})

		It("denies when the ConfigMap key is missing", func() {
			ns := newTestNamespace()

			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "agent-prompt", Namespace: ns},
				Data:       map[string]string{"other": "value"},
			}
			Expect(k8sClient.Create(testCtx, cm)).To(Succeed())

			session := minimalAgentSession(ns, "prompt-missing-key")
			session.Spec.Task = relayv1alpha1.SessionTaskSpec{
				PromptConfigMapRef: &relayv1alpha1.PromptConfigMapRef{
					Name: "agent-prompt",
					Key:  "instructions",
				},
			}
			Expect(k8sClient.Create(testCtx, session)).To(Succeed())

			waitForPhase(client.ObjectKeyFromObject(session), relayv1alpha1.PhaseDenied)
		})
	})
})
