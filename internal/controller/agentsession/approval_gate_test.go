/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package agentsession

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
)

func approvalPolicy(ns, name string, actions ...string) *relayv1alpha1.ApprovalPolicy {
	return &relayv1alpha1.ApprovalPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: relayv1alpha1.ApprovalPolicySpec{
			Actions:     actions,
			Requirement: relayv1alpha1.ApprovalRequirementDefault,
			OnTimeout:   relayv1alpha1.ApprovalTimeoutDeny,
		},
	}
}

func sessionRequiringApproval(ns, name, action string) *relayv1alpha1.AgentSession {
	s := minimalAgentSession(ns, name)
	s.Spec.Policy = relayv1alpha1.InlinePolicySpec{
		PolicyRules: relayv1alpha1.PolicyRules{RequireHumanApproval: []string{action}},
	}
	return s
}

var _ = Describe("AgentSession approval gate", func() {
	It("proceeds (warn-only) when approval is declared but no ApprovalPolicy gates it", func() {
		ns := newTestNamespace()
		session := sessionRequiringApproval(ns, "no-gate", "deploy")
		Expect(k8sClient.Create(testCtx, session)).To(Succeed())

		// Without a matching ApprovalPolicy the session is not blocked; a Job is created.
		waitForJob(ns, session)

		reqKey := types.NamespacedName{Namespace: ns, Name: session.Name}
		var req relayv1alpha1.ApprovalRequest
		Expect(k8sClient.Get(testCtx, reqKey, &req)).NotTo(Succeed())
	})

	It("holds the session in AwaitingApproval and creates an ApprovalRequest", func() {
		ns := newTestNamespace()
		Expect(k8sClient.Create(testCtx, approvalPolicy(ns, "gate", "deploy"))).To(Succeed())

		session := sessionRequiringApproval(ns, "awaits", "deploy")
		Expect(k8sClient.Create(testCtx, session)).To(Succeed())
		key := client.ObjectKeyFromObject(session)

		waitForPhase(key, relayv1alpha1.PhaseAwaitingApproval)
		expectJobAbsent(ns, session)

		var req relayv1alpha1.ApprovalRequest
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(testCtx, key, &req)).To(Succeed())
			g.Expect(req.Spec.Action).To(Equal("deploy"))
			g.Expect(req.Spec.SessionRef.Name).To(Equal(session.Name))
		}, controllerWaitTimeout, controllerPollInterval).Should(Succeed())

		var got relayv1alpha1.AgentSession
		Expect(k8sClient.Get(testCtx, key, &got)).To(Succeed())
		cond := getCondition(&got, ConditionApprovalRequired)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
	})

	It("resumes to runtime creation once the request is granted", func() {
		ns := newTestNamespace()
		Expect(k8sClient.Create(testCtx, approvalPolicy(ns, "gate", "deploy"))).To(Succeed())

		session := sessionRequiringApproval(ns, "granted", "deploy")
		Expect(k8sClient.Create(testCtx, session)).To(Succeed())
		key := client.ObjectKeyFromObject(session)
		waitForPhase(key, relayv1alpha1.PhaseAwaitingApproval)

		// Approver grants the request.
		Eventually(func(g Gomega) {
			var req relayv1alpha1.ApprovalRequest
			g.Expect(k8sClient.Get(testCtx, key, &req)).To(Succeed())
			req.Spec.Decision = relayv1alpha1.ApprovalDecisionGranted
			g.Expect(k8sClient.Update(testCtx, &req)).To(Succeed())
		}, controllerWaitTimeout, controllerPollInterval).Should(Succeed())

		waitForJob(ns, session)

		var got relayv1alpha1.AgentSession
		Expect(k8sClient.Get(testCtx, key, &got)).To(Succeed())
		Expect(got.Status.Phase).NotTo(Equal(relayv1alpha1.PhaseAwaitingApproval))
		Expect(hasApprovalDecision(&got, "deploy", relayv1alpha1.PolicyDecisionAllow)).To(BeTrue())
	})

	It("denies the session terminally when the request is denied", func() {
		ns := newTestNamespace()
		Expect(k8sClient.Create(testCtx, approvalPolicy(ns, "gate", "deploy"))).To(Succeed())

		session := sessionRequiringApproval(ns, "denied", "deploy")
		Expect(k8sClient.Create(testCtx, session)).To(Succeed())
		key := client.ObjectKeyFromObject(session)
		waitForPhase(key, relayv1alpha1.PhaseAwaitingApproval)

		Eventually(func(g Gomega) {
			var req relayv1alpha1.ApprovalRequest
			g.Expect(k8sClient.Get(testCtx, key, &req)).To(Succeed())
			req.Spec.Decision = relayv1alpha1.ApprovalDecisionDenied
			g.Expect(k8sClient.Update(testCtx, &req)).To(Succeed())
		}, controllerWaitTimeout, controllerPollInterval).Should(Succeed())

		waitForPhase(key, relayv1alpha1.PhaseDenied)
		expectJobAbsent(ns, session)

		var got relayv1alpha1.AgentSession
		Expect(k8sClient.Get(testCtx, key, &got)).To(Succeed())
		Expect(hasApprovalDecision(&got, "deploy", relayv1alpha1.PolicyDecisionDeny)).To(BeTrue())
	})
})
