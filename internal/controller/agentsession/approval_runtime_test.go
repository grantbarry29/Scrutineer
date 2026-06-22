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
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
)

// newRuntimeApprovalRequest builds a mid-execution (trigger=runtime) ApprovalRequest
// for a session's tool call, scoped to a tool target with a post-grant window.
func newRuntimeApprovalRequest(ns, name, sessionName, action, target, policyRef string) *relayv1alpha1.ApprovalRequest {
	return &relayv1alpha1.ApprovalRequest{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: relayv1alpha1.ApprovalRequestSpec{
			SessionRef: relayv1alpha1.ApprovalSessionRef{Name: sessionName},
			Trigger:    relayv1alpha1.ApprovalTriggerRuntime,
			RequestID:  name + "-rid",
			PolicyRef:  policyRef,
			Action:     action,
			Scope: relayv1alpha1.ApprovalScope{
				Target:    target,
				ArgDigest: "sha256:deadbeef",
				Window:    &metav1.Duration{Duration: time.Hour},
			},
			Decision: relayv1alpha1.ApprovalDecisionPending,
		},
	}
}

// patchApprovalDecision sets the human decision on an ApprovalRequest spec, retrying
// on optimistic-concurrency conflicts with the controller's status writes.
func patchApprovalDecision(key types.NamespacedName, decision relayv1alpha1.ApprovalDecision, decidedBy string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		var req relayv1alpha1.ApprovalRequest
		g.Expect(k8sClient.Get(testCtx, key, &req)).To(Succeed())
		req.Spec.Decision = decision
		req.Spec.DecidedBy = decidedBy
		g.Expect(k8sClient.Update(testCtx, &req)).To(Succeed())
	}, controllerWaitTimeout, controllerPollInterval).Should(Succeed())
}

func waitForApprovalState(key types.NamespacedName, want relayv1alpha1.ApprovalState) *relayv1alpha1.ApprovalRequest {
	GinkgoHelper()
	var got relayv1alpha1.ApprovalRequest
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(testCtx, key, &got)).To(Succeed())
		g.Expect(got.Status.State).To(Equal(want))
	}, controllerWaitTimeout, controllerPollInterval).Should(Succeed())
	return &got
}

var _ = Describe("Runtime per-tool approval", func() {
	// A runtime ApprovalRequest gates a single held tool call, not the session: the
	// controller resolves its lifecycle (decision -> state) while the session keeps
	// running. These specs rely on the suite's live controller.

	It("grants a held tool call and sets an expiry without gating the session", func() {
		ns := newTestNamespace()
		session := minimalAgentSession(ns, "rt-grant")
		Expect(k8sClient.Create(testCtx, session)).To(Succeed())
		waitForJob(ns, session) // session passed the gate and is running

		reqKey := types.NamespacedName{Namespace: ns, Name: "rt-grant-deploy"}
		req := newRuntimeApprovalRequest(ns, reqKey.Name, session.Name, "deploy", "deploy-prod", "")
		Expect(k8sClient.Create(testCtx, req)).To(Succeed())
		waitForApprovalState(reqKey, relayv1alpha1.ApprovalStatePending)

		patchApprovalDecision(reqKey, relayv1alpha1.ApprovalDecisionGranted, "alice")

		granted := waitForApprovalState(reqKey, relayv1alpha1.ApprovalStateGranted)
		Expect(granted.Status.DecidedBy).To(Equal("alice"))
		Expect(granted.Status.DecidedAt).NotTo(BeNil())
		Expect(granted.Status.ExpiresAt).NotTo(BeNil(), "scope.window should yield a post-grant expiry")

		// The session itself must never enter the human-approval gate or be denied
		// because of a runtime tool approval.
		var got relayv1alpha1.AgentSession
		Expect(k8sClient.Get(testCtx, client.ObjectKeyFromObject(session), &got)).To(Succeed())
		Expect(got.Status.Phase).NotTo(Equal(relayv1alpha1.PhaseAwaitingApproval))
		Expect(got.Status.Phase).NotTo(Equal(relayv1alpha1.PhaseDenied))
	})

	It("denies a held tool call without failing the session", func() {
		ns := newTestNamespace()
		session := minimalAgentSession(ns, "rt-deny")
		Expect(k8sClient.Create(testCtx, session)).To(Succeed())
		waitForJob(ns, session)

		reqKey := types.NamespacedName{Namespace: ns, Name: "rt-deny-deploy"}
		req := newRuntimeApprovalRequest(ns, reqKey.Name, session.Name, "deploy", "deploy-prod", "")
		Expect(k8sClient.Create(testCtx, req)).To(Succeed())
		waitForApprovalState(reqKey, relayv1alpha1.ApprovalStatePending)

		patchApprovalDecision(reqKey, relayv1alpha1.ApprovalDecisionDenied, "alice")
		waitForApprovalState(reqKey, relayv1alpha1.ApprovalStateDenied)

		var got relayv1alpha1.AgentSession
		Expect(k8sClient.Get(testCtx, client.ObjectKeyFromObject(session), &got)).To(Succeed())
		Expect(got.Status.Phase).NotTo(Equal(relayv1alpha1.PhaseDenied))
	})

	It("does not honor a grant from an unlisted approver when a policy scopes it", func() {
		ns := newTestNamespace()
		session := minimalAgentSession(ns, "rt-approver")
		Expect(k8sClient.Create(testCtx, session)).To(Succeed())
		waitForJob(ns, session)

		policy := &relayv1alpha1.ApprovalPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "deploy-approvers", Namespace: ns},
			Spec: relayv1alpha1.ApprovalPolicySpec{
				Actions: []string{"deploy"},
				Approvers: []relayv1alpha1.ApprovalSubject{
					{Kind: relayv1alpha1.ApprovalSubjectUser, Name: "alice"},
				},
			},
		}
		Expect(k8sClient.Create(testCtx, policy)).To(Succeed())

		reqKey := types.NamespacedName{Namespace: ns, Name: "rt-approver-deploy"}
		req := newRuntimeApprovalRequest(ns, reqKey.Name, session.Name, "deploy", "deploy-prod", policy.Name)
		Expect(k8sClient.Create(testCtx, req)).To(Succeed())
		waitForApprovalState(reqKey, relayv1alpha1.ApprovalStatePending)

		// An unlisted approver's grant is rejected: the request stays Pending.
		patchApprovalDecision(reqKey, relayv1alpha1.ApprovalDecisionGranted, "mallory")
		Consistently(func(g Gomega) {
			var got relayv1alpha1.ApprovalRequest
			g.Expect(k8sClient.Get(testCtx, reqKey, &got)).To(Succeed())
			g.Expect(got.Status.State).To(Equal(relayv1alpha1.ApprovalStatePending))
		}, 2*time.Second, controllerPollInterval).Should(Succeed())

		// A listed approver's grant is honored.
		patchApprovalDecision(reqKey, relayv1alpha1.ApprovalDecisionGranted, "alice")
		waitForApprovalState(reqKey, relayv1alpha1.ApprovalStateGranted)
	})
})

var _ = Describe("Runtime approval helpers", func() {
	It("treats only trigger=runtime as a runtime hold", func() {
		Expect(relayv1alpha1.ApprovalRequestSpec{Trigger: relayv1alpha1.ApprovalTriggerRuntime}.IsRuntime()).To(BeTrue())
		Expect(relayv1alpha1.ApprovalRequestSpec{Trigger: relayv1alpha1.ApprovalTriggerSession}.IsRuntime()).To(BeFalse())
		Expect(relayv1alpha1.ApprovalRequestSpec{}.IsRuntime()).To(BeFalse(), "empty trigger means session")
	})

	It("prefers scope.target then action for the decision subject", func() {
		withTarget := &relayv1alpha1.ApprovalRequest{Spec: relayv1alpha1.ApprovalRequestSpec{
			Action: "deploy", Scope: relayv1alpha1.ApprovalScope{Target: "deploy-prod"},
		}}
		withoutTarget := &relayv1alpha1.ApprovalRequest{Spec: relayv1alpha1.ApprovalRequestSpec{Action: "deploy"}}
		Expect(runtimeApprovalTarget(withTarget)).To(Equal("deploy-prod"))
		Expect(runtimeApprovalTarget(withoutTarget)).To(Equal("deploy"))
	})

	It("reports decided states as final", func() {
		Expect(approvalStateDecided(relayv1alpha1.ApprovalStateGranted)).To(BeTrue())
		Expect(approvalStateDecided(relayv1alpha1.ApprovalStateDenied)).To(BeTrue())
		Expect(approvalStateDecided(relayv1alpha1.ApprovalStateExpired)).To(BeTrue())
		Expect(approvalStateDecided(relayv1alpha1.ApprovalStatePending)).To(BeFalse())
		Expect(approvalStateDecided(relayv1alpha1.ApprovalState(""))).To(BeFalse())
	})

	It("derives the validity window from policy first, then scope.window", func() {
		policy := &relayv1alpha1.ApprovalPolicy{Spec: relayv1alpha1.ApprovalPolicySpec{
			ExpiresAfter: &metav1.Duration{Duration: 30 * time.Minute},
		}}
		reqWindow := &relayv1alpha1.ApprovalRequest{Spec: relayv1alpha1.ApprovalRequestSpec{
			Scope: relayv1alpha1.ApprovalScope{Window: &metav1.Duration{Duration: 5 * time.Minute}},
		}}
		Expect(approvalValidityWindow(policy, reqWindow)).To(Equal(30 * time.Minute))
		Expect(approvalValidityWindow(nil, reqWindow)).To(Equal(5 * time.Minute))
		Expect(approvalValidityWindow(nil, &relayv1alpha1.ApprovalRequest{})).To(BeZero())
	})
})
