/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package agentsession

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
)

var _ = Describe("status patch strategy", func() {
	It("mergeStatusConditionsInPlace preserves condition types missing from the desired set", func() {
		desired := []metav1.Condition{
			{Type: ConditionValidated, Status: metav1.ConditionTrue, Reason: "SpecValid", Message: "ok"},
			{Type: ConditionCompleted, Status: metav1.ConditionTrue, Reason: "JobSucceeded", Message: "done"},
		}
		preserve := []metav1.Condition{
			{Type: ConditionValidated, Status: metav1.ConditionTrue, Reason: "SpecValid", Message: "ok"},
			{Type: ConditionRuntimeCreated, Status: metav1.ConditionTrue, Reason: "JobCreated", Message: "exists"},
		}

		mergeStatusConditionsInPlace(&desired, preserve)

		Expect(conditionTypes(desired)).To(ConsistOf(
			ConditionValidated,
			ConditionRuntimeCreated,
			ConditionCompleted,
		))
		completed := findConditionByType(desired, ConditionCompleted)
		Expect(completed.Message).To(Equal("done"))
	})

	It("patchStatus retains conditions present on the apiserver but missing from a stale reconcile snapshot", func() {
		ns := newTestNamespace()
		session := minimalAgentSession(ns, "status-merge")
		Expect(k8sClient.Create(testCtx, session)).To(Succeed())
		key := client.ObjectKeyFromObject(session)

		var live relayv1alpha1.AgentSession
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(testCtx, key, &live)).To(Succeed())
			live.Status.Phase = relayv1alpha1.PhaseRunning
			setCondition(&live, ConditionValidated, metav1.ConditionTrue, "SpecValid", "accepted")
			setCondition(&live, ConditionPolicyResolved, metav1.ConditionTrue, "PoliciesMerged", "merged 0 referenced policies")
			setCondition(&live, ConditionRuntimeProfileResolved, metav1.ConditionTrue, "NoProfileRef", "no runtime profile referenced")
			setCondition(&live, ConditionPolicyPropagated, metav1.ConditionTrue, "EnvCurrent", "env current")
			setCondition(&live, ConditionRuntimeCreated, metav1.ConditionTrue, "JobCreated", "job exists")
			g.Expect(k8sClient.Status().Update(testCtx, &live)).To(Succeed())
		}, 10*time.Second, 200*time.Millisecond).Should(Succeed())
		Expect(k8sClient.Get(testCtx, key, &live)).To(Succeed())

		reconciler := &AgentSessionReconciler{Client: k8sClient, Scheme: mgr.GetScheme()}
		// The suite manager may reconcile the same AgentSession concurrently; retry on conflict.
		Eventually(func(g Gomega) {
			var current relayv1alpha1.AgentSession
			g.Expect(k8sClient.Get(testCtx, key, &current)).To(Succeed())

			staleSnap := current.DeepCopy()
			staleSnap.Status.Conditions = []metav1.Condition{
				*findConditionByType(current.Status.Conditions, ConditionValidated),
			}

			desired := current.DeepCopy()
			desired.Status.Phase = relayv1alpha1.PhaseSucceeded
			desired.Status.Conditions = []metav1.Condition{
				*findConditionByType(current.Status.Conditions, ConditionValidated),
			}
			setCondition(desired, ConditionCompleted, metav1.ConditionTrue, "JobSucceeded", "done")

			g.Expect(reconciler.patchStatus(testCtx, staleSnap, desired)).To(Succeed())
		}, 10*time.Second, 200*time.Millisecond).Should(Succeed())

		var after relayv1alpha1.AgentSession
		Expect(k8sClient.Get(testCtx, key, &after)).To(Succeed())
		Expect(conditionTypes(after.Status.Conditions)).To(ConsistOf(
			ConditionValidated,
			ConditionPolicyResolved,
			ConditionRuntimeProfileResolved,
			ConditionPolicyPropagated,
			ConditionRuntimeCreated,
			ConditionCompleted,
			ConditionReady,
		))
		Expect(after.Status.Phase).To(Equal(relayv1alpha1.PhaseSucceeded))
	})

	It("patchStatus preserves violations missing from a stale reconcile snapshot", func() {
		ns := newTestNamespace()
		session := minimalAgentSession(ns, "violations-status-merge")
		Expect(k8sClient.Create(testCtx, session)).To(Succeed())
		key := client.ObjectKeyFromObject(session)

		violationTS := metav1.Now()
		Eventually(func(g Gomega) {
			var live relayv1alpha1.AgentSession
			g.Expect(k8sClient.Get(testCtx, key, &live)).To(Succeed())
			live.Status.Phase = relayv1alpha1.PhaseRunning
			AppendRuntimeViolations(&live, []relayv1alpha1.PolicyViolation{{
				Time:    violationTS,
				Type:    "network",
				Message: "egress blocked",
				Target:  "10.0.0.0/8",
			}})
			g.Expect(k8sClient.Status().Update(testCtx, &live)).To(Succeed())
		}, 10*time.Second, 200*time.Millisecond).Should(Succeed())

		reconciler := &AgentSessionReconciler{Client: k8sClient, Scheme: mgr.GetScheme()}
		Eventually(func(g Gomega) {
			var current relayv1alpha1.AgentSession
			g.Expect(k8sClient.Get(testCtx, key, &current)).To(Succeed())
			g.Expect(current.Status.Violations).NotTo(BeEmpty())

			staleSnap := current.DeepCopy()
			staleSnap.Status.Violations = nil

			desired := current.DeepCopy()
			desired.Status.Phase = relayv1alpha1.PhaseSucceeded
			desired.Status.Violations = nil

			g.Expect(reconciler.patchStatus(testCtx, staleSnap, desired)).To(Succeed())

			var after relayv1alpha1.AgentSession
			g.Expect(k8sClient.Get(testCtx, key, &after)).To(Succeed())
			g.Expect(after.Status.Violations).To(HaveLen(1))
			g.Expect(after.Status.Violations[0].Target).To(Equal("10.0.0.0/8"))
		}, 10*time.Second, 200*time.Millisecond).Should(Succeed())
	})

	It("patchStatus preserves runtime policy decisions missing from a stale reconcile snapshot", func() {
		ns := newTestNamespace()
		session := minimalAgentSession(ns, "runtime-status-merge")
		Expect(k8sClient.Create(testCtx, session)).To(Succeed())
		key := client.ObjectKeyFromObject(session)

		runtimeTS := metav1.Now()
		Eventually(func(g Gomega) {
			var live relayv1alpha1.AgentSession
			g.Expect(k8sClient.Get(testCtx, key, &live)).To(Succeed())
			live.Status.Phase = relayv1alpha1.PhaseRunning
			setCondition(&live, ConditionValidated, metav1.ConditionTrue, "SpecValid", "accepted")
			AppendRuntimePolicyDecisions(&live, []relayv1alpha1.PolicyDecision{{
				Time:   runtimeTS,
				Type:   "network",
				Action: relayv1alpha1.PolicyDecisionDeny,
				Reason: "DeniedDomains",
				Target: "evil.example",
			}})
			g.Expect(k8sClient.Status().Update(testCtx, &live)).To(Succeed())
		}, 10*time.Second, 200*time.Millisecond).Should(Succeed())

		reconciler := &AgentSessionReconciler{Client: k8sClient, Scheme: mgr.GetScheme()}
		Eventually(func(g Gomega) {
			var current relayv1alpha1.AgentSession
			g.Expect(k8sClient.Get(testCtx, key, &current)).To(Succeed())

			staleSnap := current.DeepCopy()
			staleSnap.Status.PolicyDecisions = nil

			desired := current.DeepCopy()
			desired.Status.Phase = relayv1alpha1.PhaseSucceeded
			desired.Status.PolicyDecisions = []relayv1alpha1.PolicyDecision{{
				Time:   runtimeTS,
				Phase:  relayv1alpha1.PolicyDecisionPhaseMerge,
				Type:   "mode",
				Action: relayv1alpha1.PolicyDecisionAudit,
				Reason: "StrictestMode",
			}}

			g.Expect(reconciler.patchStatus(testCtx, staleSnap, desired)).To(Succeed())

			var after relayv1alpha1.AgentSession
			g.Expect(k8sClient.Get(testCtx, key, &after)).To(Succeed())
			var runtimeDecision *relayv1alpha1.PolicyDecision
			for i := range after.Status.PolicyDecisions {
				d := &after.Status.PolicyDecisions[i]
				if d.Phase == relayv1alpha1.PolicyDecisionPhaseRuntime && d.Target == "evil.example" {
					runtimeDecision = d
				}
			}
			g.Expect(runtimeDecision).NotTo(BeNil())
		}, 10*time.Second, 200*time.Millisecond).Should(Succeed())
	})

	It("documents that JSON merge patch replaces the entire conditions array on CRDs", func() {
		ns := newTestNamespace()
		session := minimalAgentSession(ns, "status-json-merge")
		Expect(k8sClient.Create(testCtx, session)).To(Succeed())
		key := client.ObjectKeyFromObject(session)

		var live relayv1alpha1.AgentSession
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(testCtx, key, &live)).To(Succeed())
			setCondition(&live, ConditionValidated, metav1.ConditionTrue, "SpecValid", "accepted")
			setCondition(&live, ConditionRuntimeCreated, metav1.ConditionTrue, "JobCreated", "job exists")
			g.Expect(k8sClient.Status().Update(testCtx, &live)).To(Succeed())
		}, 10*time.Second, 200*time.Millisecond).Should(Succeed())
		Expect(k8sClient.Get(testCtx, key, &live)).To(Succeed())

		stale := live.DeepCopy()
		stale.Status.Conditions = []metav1.Condition{
			*findConditionByType(live.Status.Conditions, ConditionValidated),
		}

		updated := live.DeepCopy()
		updated.Status.Conditions = []metav1.Condition{
			*findConditionByType(live.Status.Conditions, ConditionValidated),
		}
		setCondition(updated, ConditionCompleted, metav1.ConditionTrue, "JobSucceeded", "done")

		patch := client.MergeFrom(stale.DeepCopy())
		Expect(k8sClient.Status().Patch(testCtx, updated, patch)).To(Succeed())

		var after relayv1alpha1.AgentSession
		Expect(k8sClient.Get(testCtx, key, &after)).To(Succeed())
		Expect(findConditionByType(after.Status.Conditions, ConditionRuntimeCreated)).To(BeNil(),
			"JSON merge patch replaces the whole conditions array and drops RuntimeCreated")
	})
})

func conditionTypes(conds []metav1.Condition) []string {
	out := make([]string, 0, len(conds))
	for _, c := range conds {
		out = append(out, c.Type)
	}
	return out
}

func findConditionByType(conds []metav1.Condition, condType string) *metav1.Condition {
	for i := range conds {
		if conds[i].Type == condType {
			return &conds[i]
		}
	}
	return nil
}
