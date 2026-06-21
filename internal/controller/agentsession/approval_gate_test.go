/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package agentsession

import (
	"context"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
	"github.com/secureai/relay/internal/approval"
)

// recordingNotifier captures approval notifications for assertions. Installed on
// the suite reconciler; namespaces/names are unique per spec so calls don't collide.
type recordingNotifier struct {
	mu    sync.Mutex
	calls []approval.Notification
}

func (r *recordingNotifier) Notify(_ context.Context, n approval.Notification) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, n)
	return nil
}

func (r *recordingNotifier) countFor(name string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, n := range r.calls {
		if n.Name == name {
			count++
		}
	}
	return count
}

var testNotifier = &recordingNotifier{}

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

	It("notifies approvers exactly once when the gate opens", func() {
		ns := newTestNamespace()
		Expect(k8sClient.Create(testCtx, approvalPolicy(ns, "gate", "deploy"))).To(Succeed())

		session := sessionRequiringApproval(ns, "notify-me", "deploy")
		Expect(k8sClient.Create(testCtx, session)).To(Succeed())
		key := client.ObjectKeyFromObject(session)
		waitForPhase(key, relayv1alpha1.PhaseAwaitingApproval)

		Eventually(func() int {
			return testNotifier.countFor(session.Name)
		}, controllerWaitTimeout, controllerPollInterval).Should(Equal(1))

		// Idempotent across subsequent reconciles (pending requeues every ~15s).
		Consistently(func() int {
			return testNotifier.countFor(session.Name)
		}, 2*time.Second, controllerPollInterval).Should(Equal(1))

		var req relayv1alpha1.ApprovalRequest
		Expect(k8sClient.Get(testCtx, key, &req)).To(Succeed())
		Expect(req.Annotations).To(HaveKey(approvalNotifiedAnnotation))
	})

	It("honors a grant only from a listed approver", func() {
		ns := newTestNamespace()
		policy := approvalPolicy(ns, "gate", "deploy")
		policy.Spec.Approvers = []relayv1alpha1.ApprovalSubject{
			{Kind: relayv1alpha1.ApprovalSubjectUser, Name: "alice"},
		}
		Expect(k8sClient.Create(testCtx, policy)).To(Succeed())

		session := sessionRequiringApproval(ns, "approver-gate", "deploy")
		Expect(k8sClient.Create(testCtx, session)).To(Succeed())
		key := client.ObjectKeyFromObject(session)
		waitForPhase(key, relayv1alpha1.PhaseAwaitingApproval)

		// Grant from an unlisted approver is not honored: session stays gated.
		Eventually(func(g Gomega) {
			var req relayv1alpha1.ApprovalRequest
			g.Expect(k8sClient.Get(testCtx, key, &req)).To(Succeed())
			req.Spec.Decision = relayv1alpha1.ApprovalDecisionGranted
			req.Spec.DecidedBy = "mallory"
			g.Expect(k8sClient.Update(testCtx, &req)).To(Succeed())
		}, controllerWaitTimeout, controllerPollInterval).Should(Succeed())

		Consistently(func(g Gomega) {
			var got relayv1alpha1.AgentSession
			g.Expect(k8sClient.Get(testCtx, key, &got)).To(Succeed())
			g.Expect(got.Status.Phase).To(Equal(relayv1alpha1.PhaseAwaitingApproval))
		}, 2*time.Second, controllerPollInterval).Should(Succeed())
		expectJobAbsent(ns, session)

		// A listed approver's grant resumes the session.
		Eventually(func(g Gomega) {
			var req relayv1alpha1.ApprovalRequest
			g.Expect(k8sClient.Get(testCtx, key, &req)).To(Succeed())
			req.Spec.DecidedBy = "alice"
			g.Expect(k8sClient.Update(testCtx, &req)).To(Succeed())
		}, controllerWaitTimeout, controllerPollInterval).Should(Succeed())

		waitForJob(ns, session)

		var req relayv1alpha1.ApprovalRequest
		Expect(k8sClient.Get(testCtx, key, &req)).To(Succeed())
		Expect(req.Status.DecidedBy).To(Equal("alice"))
	})

	It("requires every listed approver before resuming an allOf gate", func() {
		ns := newTestNamespace()
		policy := approvalPolicy(ns, "gate", "deploy")
		policy.Spec.Requirement = relayv1alpha1.ApprovalRequirementAllOf
		policy.Spec.Approvers = []relayv1alpha1.ApprovalSubject{
			{Kind: relayv1alpha1.ApprovalSubjectUser, Name: "alice"},
			{Kind: relayv1alpha1.ApprovalSubjectUser, Name: "bob"},
		}
		Expect(k8sClient.Create(testCtx, policy)).To(Succeed())

		session := sessionRequiringApproval(ns, "allof-gate", "deploy")
		Expect(k8sClient.Create(testCtx, session)).To(Succeed())
		key := client.ObjectKeyFromObject(session)
		waitForPhase(key, relayv1alpha1.PhaseAwaitingApproval)

		// First approver grants: session stays gated and the grant is recorded.
		Eventually(func(g Gomega) {
			var req relayv1alpha1.ApprovalRequest
			g.Expect(k8sClient.Get(testCtx, key, &req)).To(Succeed())
			req.Spec.Decision = relayv1alpha1.ApprovalDecisionGranted
			req.Spec.DecidedBy = "alice"
			g.Expect(k8sClient.Update(testCtx, &req)).To(Succeed())
		}, controllerWaitTimeout, controllerPollInterval).Should(Succeed())

		Eventually(func(g Gomega) {
			var req relayv1alpha1.ApprovalRequest
			g.Expect(k8sClient.Get(testCtx, key, &req)).To(Succeed())
			g.Expect(req.Status.ApprovedBy).To(ContainElement("alice"))
		}, controllerWaitTimeout, controllerPollInterval).Should(Succeed())

		Consistently(func(g Gomega) {
			var got relayv1alpha1.AgentSession
			g.Expect(k8sClient.Get(testCtx, key, &got)).To(Succeed())
			g.Expect(got.Status.Phase).To(Equal(relayv1alpha1.PhaseAwaitingApproval))
		}, 2*time.Second, controllerPollInterval).Should(Succeed())
		expectJobAbsent(ns, session)

		// Second approver grants: coverage is complete, the session resumes.
		Eventually(func(g Gomega) {
			var req relayv1alpha1.ApprovalRequest
			g.Expect(k8sClient.Get(testCtx, key, &req)).To(Succeed())
			req.Spec.DecidedBy = "bob"
			g.Expect(k8sClient.Update(testCtx, &req)).To(Succeed())
		}, controllerWaitTimeout, controllerPollInterval).Should(Succeed())

		waitForJob(ns, session)

		var got relayv1alpha1.AgentSession
		Expect(k8sClient.Get(testCtx, key, &got)).To(Succeed())
		Expect(hasApprovalDecision(&got, "deploy", relayv1alpha1.PolicyDecisionAllow)).To(BeTrue())
		var req relayv1alpha1.ApprovalRequest
		Expect(k8sClient.Get(testCtx, key, &req)).To(Succeed())
		Expect(req.Status.ApprovedBy).To(ConsistOf("alice", "bob"))
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
