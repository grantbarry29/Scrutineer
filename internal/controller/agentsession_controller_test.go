/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package controller

import (
	"context"
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
		Client:    k8sClient,
		APIReader: mgr.GetAPIReader(),
		Scheme:    mgr.GetScheme(),
		Recorder:  mgr.GetEventRecorderFor("relay-test"),
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

		It("denies when spec.model.provider is empty", func() {
			ns := newTestNamespace()
			session := minimalAgentSession(ns, "denied-empty-provider")
			session.Spec.Model.Provider = "   "

			Expect(k8sClient.Create(testCtx, session)).To(Succeed())
			waitForPhase(client.ObjectKeyFromObject(session), relayv1alpha1.PhaseDenied)
			expectJobAbsent(ns, session)
		})

		It("denies when policyRefs points to a missing AgentPolicy", func() {
			ns := newTestNamespace()
			session := minimalAgentSession(ns, "denied-missing-policy")
			session.Spec.PolicyRefs = []relayv1alpha1.PolicyRef{{
				Kind: "AgentPolicy",
				Name: "does-not-exist",
			}}

			Expect(k8sClient.Create(testCtx, session)).To(Succeed())
			key := client.ObjectKeyFromObject(session)
			waitForPhase(key, relayv1alpha1.PhaseDenied)

			var got relayv1alpha1.AgentSession
			Expect(k8sClient.Get(testCtx, key, &got)).To(Succeed())
			validated := getCondition(&got, ConditionValidated)
			Expect(validated).NotTo(BeNil())
			Expect(validated.Reason).To(Equal("InvalidPolicy"))
			expectJobAbsent(ns, session)
		})

		It("denies when spec.workspace.size is invalid", func() {
			ns := newTestNamespace()
			session := minimalAgentSession(ns, "denied-bad-workspace-size")
			session.Spec.Workspace = relayv1alpha1.WorkspaceSpec{
				Ephemeral: true,
				Size:      "not-a-quantity",
			}

			Expect(k8sClient.Create(testCtx, session)).To(Succeed())
			waitForPhase(client.ObjectKeyFromObject(session), relayv1alpha1.PhaseDenied)
			expectJobAbsent(ns, session)
		})
	})

	Context("policy resolution", func() {
		It("merges AgentPolicy refs with inline overrides into effective policy and Job env", func() {
			ns := newTestNamespace()
			ap := &relayv1alpha1.AgentPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "baseline", Namespace: ns},
				Spec: relayv1alpha1.AgentPolicySpec{
					Mode: relayv1alpha1.PolicyModeDryRun,
					PolicyRules: relayv1alpha1.PolicyRules{
						DeniedDomains: []string{"dropbox.com"},
						DeniedTools:   []string{"kubectl-prod"},
						AllowedTools:  []string{"shell"},
					},
				},
			}
			Expect(k8sClient.Create(testCtx, ap)).To(Succeed())

			session := minimalAgentSession(ns, "policy-ref-merge")
			session.Spec.PolicyRefs = []relayv1alpha1.PolicyRef{{
				Kind: "AgentPolicy",
				Name: "baseline",
			}}
			session.Spec.Policy = relayv1alpha1.InlinePolicySpec{
				PolicyRules: relayv1alpha1.PolicyRules{
					AllowedDomains: []string{"github.com"},
					DeniedTools:    []string{"deploy"},
				},
			}
			Expect(k8sClient.Create(testCtx, session)).To(Succeed())
			key := client.ObjectKeyFromObject(session)

			waitForJob(ns, session)

			var got relayv1alpha1.AgentSession
			Expect(k8sClient.Get(testCtx, key, &got)).To(Succeed())
			Expect(got.Status.EffectivePolicy).NotTo(BeNil())
			Expect(got.Status.EffectivePolicy.Mode).To(Equal(relayv1alpha1.PolicyModeDryRun))
			Expect(got.Status.EffectivePolicy.AllowedDomains).To(ContainElements("github.com"))
			Expect(got.Status.EffectivePolicy.DeniedDomains).To(ContainElements("dropbox.com"))
			Expect(got.Status.EffectivePolicy.DeniedTools).To(ContainElements("kubectl-prod", "deploy"))
			Expect(got.Status.MatchedPolicies).To(HaveLen(1))
			Expect(got.Status.MatchedPolicies[0].Name).To(Equal("baseline"))

			policyResolved := getCondition(&got, ConditionPolicyResolved)
			Expect(policyResolved).NotTo(BeNil())
			Expect(policyResolved.Status).To(Equal(metav1.ConditionTrue))

			var job batchv1.Job
			Expect(k8sClient.Get(testCtx, types.NamespacedName{Namespace: ns, Name: jobNameFor(session)}, &job)).To(Succeed())
			env := envMap(job.Spec.Template.Spec.Containers[0].Env)
			Expect(env[EnvPolicyMode]).To(Equal("dry-run"))
			Expect(env[EnvPolicyDeniedDomains]).To(Equal("dropbox.com"))
			Expect(env[EnvPolicyAllowedDomains]).To(Equal("github.com"))
			Expect(env[EnvPolicyDeniedTools]).To(ContainSubstring("kubectl-prod"))
			Expect(env[EnvPolicyDeniedTools]).To(ContainSubstring("deploy"))
		})

		It("reconciles when a referenced AgentPolicy is updated", func() {
			ns := newTestNamespace()
			ap := &relayv1alpha1.AgentPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "rolling-baseline", Namespace: ns},
				Spec: relayv1alpha1.AgentPolicySpec{
					Mode: relayv1alpha1.PolicyModeAuditOnly,
					PolicyRules: relayv1alpha1.PolicyRules{
						DeniedDomains: []string{"dropbox.com"},
					},
				},
			}
			Expect(k8sClient.Create(testCtx, ap)).To(Succeed())

			session := minimalAgentSession(ns, "policy-watch")
			session.Spec.PolicyRefs = []relayv1alpha1.PolicyRef{{
				Kind: "AgentPolicy",
				Name: "rolling-baseline",
			}}
			Expect(k8sClient.Create(testCtx, session)).To(Succeed())
			key := client.ObjectKeyFromObject(session)

			Eventually(func(g Gomega) {
				var got relayv1alpha1.AgentSession
				g.Expect(k8sClient.Get(testCtx, key, &got)).To(Succeed())
				g.Expect(got.Status.EffectivePolicy).NotTo(BeNil())
				g.Expect(got.Status.EffectivePolicy.Mode).To(Equal(relayv1alpha1.PolicyModeAuditOnly))
			}, controllerWaitTimeout, controllerPollInterval).Should(Succeed())

			Expect(k8sClient.Get(testCtx, client.ObjectKeyFromObject(ap), ap)).To(Succeed())
			ap.Spec.Mode = relayv1alpha1.PolicyModeEnforced
			ap.Spec.DeniedDomains = []string{"dropbox.com", "evil.example"}
			Expect(k8sClient.Update(testCtx, ap)).To(Succeed())

			Eventually(func(g Gomega) {
				var got relayv1alpha1.AgentSession
				g.Expect(k8sClient.Get(testCtx, key, &got)).To(Succeed())
				g.Expect(got.Status.EffectivePolicy).NotTo(BeNil())
				g.Expect(got.Status.EffectivePolicy.Mode).To(Equal(relayv1alpha1.PolicyModeEnforced))
				g.Expect(got.Status.EffectivePolicy.DeniedDomains).To(ContainElement("evil.example"))
			}, controllerWaitTimeout, controllerPollInterval).Should(Succeed())
		})
	})

	Context("Job ownership conflict", func() {
		It("denies when a foreign Job already occupies the deterministic name", func() {
			ns := newTestNamespace()
			session := minimalAgentSession(ns, "foreign-job-conflict")
			foreign := &batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: ns,
					Name:      jobNameFor(session),
				},
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							RestartPolicy: corev1.RestartPolicyNever,
							Containers: []corev1.Container{{
								Name:    "other",
								Image:   "busybox:latest",
								Command: []string{"true"},
							}},
						},
					},
				},
			}
			Expect(k8sClient.Create(testCtx, foreign)).To(Succeed())

			Expect(k8sClient.Create(testCtx, session)).To(Succeed())
			key := client.ObjectKeyFromObject(session)
			waitForPhase(key, relayv1alpha1.PhaseDenied)

			var got relayv1alpha1.AgentSession
			Expect(k8sClient.Get(testCtx, key, &got)).To(Succeed())
			validated := getCondition(&got, ConditionValidated)
			Expect(validated).NotTo(BeNil())
			Expect(validated.Reason).To(Equal("JobConflict"))
		})
	})

	Context("syncStatusFromJob", func() {
		It("maps FailureTarget DeadlineExceeded to TimedOut before Failed count increments", func() {
			session := minimalAgentSession("default", "sync-timedout")
			session.Status.Phase = relayv1alpha1.PhaseStarting
			job := &batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{Name: jobNameFor(session)},
				Status: batchv1.JobStatus{
					Conditions: []batchv1.JobCondition{{
						Type:   batchv1.JobFailureTarget,
						Status: corev1.ConditionTrue,
						Reason: "DeadlineExceeded",
					}},
				},
			}
			testReconciler().syncStatusFromJob(context.Background(), session, job)
			Expect(session.Status.Phase).To(Equal(relayv1alpha1.PhaseTimedOut))
			completed := getCondition(session, ConditionCompleted)
			Expect(completed).NotTo(BeNil())
			Expect(completed.Reason).To(Equal("JobTimedOut"))
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

			podFirst := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "relay-session-pod-name-aaa", Namespace: ns,
					Labels: podLabels, OwnerReferences: []metav1.OwnerReference{ownerRef},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: AgentContainerName, Image: "busybox:latest"}},
				},
			}
			Expect(k8sClient.Create(testCtx, podFirst)).To(Succeed())
			time.Sleep(20 * time.Millisecond)

			podChosen := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					// Name sorts after podFirst when apiserver CreationTimestamps tie.
					Name: "relay-session-pod-name-zzz", Namespace: ns,
					Labels: podLabels, OwnerReferences: []metav1.OwnerReference{ownerRef},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: AgentContainerName, Image: "busybox:latest"}},
				},
			}
			Expect(k8sClient.Create(testCtx, podChosen)).To(Succeed())

			Eventually(func(g Gomega) {
				_, err := testReconciler().Reconcile(testCtx, reconcile.Request{NamespacedName: key})
				g.Expect(err).NotTo(HaveOccurred())

				var got relayv1alpha1.AgentSession
				g.Expect(k8sClient.Get(testCtx, key, &got)).To(Succeed())
				// Prefer podChosen: later CreationTimestamp when distinct, else lexicographic max.
				g.Expect(got.Status.PodName).To(Equal(podChosen.Name))
			}, controllerWaitTimeout, controllerPollInterval).Should(Succeed())
		})
	})

	Context("terminal phase stability", func() {
		It("does not recreate a Job when phase is terminal and the Job is missing", func() {
			ns := newTestNamespace()
			session := minimalAgentSession(ns, "terminal-no-job")
			Expect(k8sClient.Create(testCtx, session)).To(Succeed())
			key := client.ObjectKeyFromObject(session)
			jobKey := types.NamespacedName{Namespace: ns, Name: jobNameFor(session)}

			waitForJob(ns, session)

			var got relayv1alpha1.AgentSession
			Expect(k8sClient.Get(testCtx, key, &got)).To(Succeed())
			got.Status.Phase = relayv1alpha1.PhaseSucceeded
			now := metav1.Now()
			got.Status.CompletionTime = &now
			got.Status.Result = &relayv1alpha1.SessionResult{Outcome: "completed", Summary: "test terminal"}
			Expect(k8sClient.Status().Update(testCtx, &got)).To(Succeed())

			var job batchv1.Job
			Expect(k8sClient.Get(testCtx, jobKey, &job)).To(Succeed())
			Expect(k8sClient.Delete(testCtx, &job, client.PropagationPolicy(metav1.DeletePropagationBackground))).To(Succeed())

			Eventually(func(g Gomega) {
				_, err := testReconciler().Reconcile(testCtx, reconcile.Request{NamespacedName: key})
				g.Expect(err).NotTo(HaveOccurred())
				var check batchv1.Job
				err = k8sClient.Get(testCtx, jobKey, &check)
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue())

				g.Expect(k8sClient.Get(testCtx, key, &got)).To(Succeed())
				g.Expect(got.Status.Phase).To(Equal(relayv1alpha1.PhaseSucceeded))
			}, controllerWaitTimeout, controllerPollInterval).Should(Succeed())
		})

		It("syncStatusFromJob does not overwrite a terminal phase", func() {
			session := minimalAgentSession("default", "sync-terminal")
			session.Status.Phase = relayv1alpha1.PhaseSucceeded
			now := metav1.Now()
			session.Status.CompletionTime = &now

			job := &batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{Name: jobNameFor(session), Namespace: session.Namespace},
				Status: batchv1.JobStatus{
					Active: 1,
				},
			}

			testReconciler().syncStatusFromJob(testCtx, session, job)
			Expect(session.Status.Phase).To(Equal(relayv1alpha1.PhaseSucceeded))
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

			Eventually(func(g Gomega) {
				var got relayv1alpha1.AgentSession
				g.Expect(k8sClient.Get(testCtx, key, &got)).To(Succeed())
				expectCancelledG(g, &got)
			}, controllerWaitTimeout, controllerPollInterval).Should(Succeed())

			expectJobAbsent(ns, session)
		})
	})

	Context("finalizer and deletion", func() {
		It("adds the Relay finalizer on reconcile", func() {
			ns := newTestNamespace()
			session := minimalAgentSession(ns, "finalizer-attached")
			Expect(k8sClient.Create(testCtx, session)).To(Succeed())
			key := client.ObjectKeyFromObject(session)

			waitForFinalizer(key)

			var got relayv1alpha1.AgentSession
			Expect(k8sClient.Get(testCtx, key, &got)).To(Succeed())
			Expect(got.Finalizers).To(ContainElement(AgentSessionFinalizer))
		})

		It("deletes the owned Job and removes the AgentSession when deleted", func() {
			ns := newTestNamespace()
			session := minimalAgentSession(ns, "delete-with-job")
			Expect(k8sClient.Create(testCtx, session)).To(Succeed())
			key := client.ObjectKeyFromObject(session)

			waitForJob(ns, session)
			waitForFinalizer(key)

			Expect(k8sClient.Delete(testCtx, session)).To(Succeed())

			var terminating relayv1alpha1.AgentSession
			Expect(k8sClient.Get(testCtx, key, &terminating)).To(Succeed())
			Expect(terminating.DeletionTimestamp).NotTo(BeZero())
			Expect(terminating.Finalizers).To(ContainElement(AgentSessionFinalizer))

			// Drive finalizer cleanup explicitly so the spec is not sensitive to manager queue timing.
			Eventually(func(g Gomega) {
				_, err := testReconciler().Reconcile(testCtx, reconcile.Request{NamespacedName: key})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(jobAbsent(ns, session)).To(BeTrue())
				var got relayv1alpha1.AgentSession
				err = k8sClient.Get(testCtx, key, &got)
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
			}, controllerWaitTimeout, controllerPollInterval).Should(Succeed())
		})

		It("removes a denied session without a Job when deleted", func() {
			ns := newTestNamespace()
			session := minimalAgentSession(ns, "delete-denied")
			session.Spec.Task = relayv1alpha1.SessionTaskSpec{}
			Expect(k8sClient.Create(testCtx, session)).To(Succeed())
			key := client.ObjectKeyFromObject(session)

			waitForPhase(key, relayv1alpha1.PhaseDenied)
			waitForFinalizer(key)
			expectJobAbsent(ns, session)

			Expect(k8sClient.Delete(testCtx, session)).To(Succeed())
			waitForAgentSessionDeleted(key)
		})

		It("removes the finalizer when the Job is already absent", func() {
			ns := newTestNamespace()
			session := minimalAgentSession(ns, "delete-no-job")
			session.Spec.Task = relayv1alpha1.SessionTaskSpec{}
			Expect(k8sClient.Create(testCtx, session)).To(Succeed())
			key := client.ObjectKeyFromObject(session)

			waitForPhase(key, relayv1alpha1.PhaseDenied)
			waitForFinalizer(key)
			expectJobAbsent(ns, session)

			Expect(k8sClient.Delete(testCtx, session)).To(Succeed())
			waitForAgentSessionDeleted(key)
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
