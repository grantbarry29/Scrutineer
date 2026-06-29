/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package agentsession

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
)

// newRuntimeApprovalRequest builds a mid-execution (trigger=runtime) ApprovalRequest
// for a session's tool call, scoped to a tool target with a post-grant window.
func newRuntimeApprovalRequest(ns, name, sessionName, action, target, policyRef string) *scrutineerv1alpha1.ApprovalRequest {
	return &scrutineerv1alpha1.ApprovalRequest{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: scrutineerv1alpha1.ApprovalRequestSpec{
			SessionRef: scrutineerv1alpha1.ApprovalSessionRef{Name: sessionName},
			Trigger:    scrutineerv1alpha1.ApprovalTriggerRuntime,
			RequestID:  name + "-rid",
			PolicyRef:  policyRef,
			Action:     action,
			Scope: scrutineerv1alpha1.ApprovalScope{
				Target:    target,
				ArgDigest: "sha256:deadbeef",
				Window:    &metav1.Duration{Duration: time.Hour},
			},
			Decision: scrutineerv1alpha1.ApprovalDecisionPending,
		},
	}
}

// patchApprovalDecision sets the human decision on an ApprovalRequest spec, retrying
// on optimistic-concurrency conflicts with the controller's status writes.
func patchApprovalDecision(key types.NamespacedName, decision scrutineerv1alpha1.ApprovalDecision, decidedBy string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		var req scrutineerv1alpha1.ApprovalRequest
		g.Expect(k8sClient.Get(testCtx, key, &req)).To(Succeed())
		req.Spec.Decision = decision
		req.Spec.DecidedBy = decidedBy
		g.Expect(k8sClient.Update(testCtx, &req)).To(Succeed())
	}, controllerWaitTimeout, controllerPollInterval).Should(Succeed())
}

func waitForApprovalState(key types.NamespacedName, want scrutineerv1alpha1.ApprovalState) *scrutineerv1alpha1.ApprovalRequest {
	GinkgoHelper()
	var got scrutineerv1alpha1.ApprovalRequest
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
		waitForApprovalState(reqKey, scrutineerv1alpha1.ApprovalStatePending)

		// While the hold is undecided the session surfaces it (redaction-safe) so
		// UI/operators can see what needs approval — argDigest only, no raw args.
		Eventually(func(g Gomega) {
			var got scrutineerv1alpha1.AgentSession
			g.Expect(k8sClient.Get(testCtx, client.ObjectKeyFromObject(session), &got)).To(Succeed())
			g.Expect(got.Status.PendingApprovals).To(HaveLen(1))
			p := got.Status.PendingApprovals[0]
			g.Expect(p.Name).To(Equal(reqKey.Name))
			g.Expect(p.Target).To(Equal("deploy-prod"))
			g.Expect(p.ArgDigest).To(Equal("sha256:deadbeef"))
			g.Expect(p.State).To(Equal(scrutineerv1alpha1.ApprovalStatePending))
		}, controllerWaitTimeout, controllerPollInterval).Should(Succeed())

		patchApprovalDecision(reqKey, scrutineerv1alpha1.ApprovalDecisionGranted, "alice")

		granted := waitForApprovalState(reqKey, scrutineerv1alpha1.ApprovalStateGranted)
		Expect(granted.Status.DecidedBy).To(Equal("alice"))
		Expect(granted.Status.DecidedAt).NotTo(BeNil())
		Expect(granted.Status.ExpiresAt).NotTo(BeNil(), "scope.window should yield a post-grant expiry")

		// Once decided, the hold drops off the pending surface.
		Eventually(func(g Gomega) {
			var got scrutineerv1alpha1.AgentSession
			g.Expect(k8sClient.Get(testCtx, client.ObjectKeyFromObject(session), &got)).To(Succeed())
			g.Expect(got.Status.PendingApprovals).To(BeEmpty())
		}, controllerWaitTimeout, controllerPollInterval).Should(Succeed())

		// The session itself must never enter the human-approval gate or be denied
		// because of a runtime tool approval.
		var got scrutineerv1alpha1.AgentSession
		Expect(k8sClient.Get(testCtx, client.ObjectKeyFromObject(session), &got)).To(Succeed())
		Expect(got.Status.Phase).NotTo(Equal(scrutineerv1alpha1.PhaseAwaitingApproval))
		Expect(got.Status.Phase).NotTo(Equal(scrutineerv1alpha1.PhaseDenied))
	})

	It("denies a held tool call without failing the session", func() {
		ns := newTestNamespace()
		session := minimalAgentSession(ns, "rt-deny")
		Expect(k8sClient.Create(testCtx, session)).To(Succeed())
		waitForJob(ns, session)

		reqKey := types.NamespacedName{Namespace: ns, Name: "rt-deny-deploy"}
		req := newRuntimeApprovalRequest(ns, reqKey.Name, session.Name, "deploy", "deploy-prod", "")
		Expect(k8sClient.Create(testCtx, req)).To(Succeed())
		waitForApprovalState(reqKey, scrutineerv1alpha1.ApprovalStatePending)

		patchApprovalDecision(reqKey, scrutineerv1alpha1.ApprovalDecisionDenied, "alice")
		waitForApprovalState(reqKey, scrutineerv1alpha1.ApprovalStateDenied)

		var got scrutineerv1alpha1.AgentSession
		Expect(k8sClient.Get(testCtx, client.ObjectKeyFromObject(session), &got)).To(Succeed())
		Expect(got.Status.Phase).NotTo(Equal(scrutineerv1alpha1.PhaseDenied))
	})

	It("does not honor a grant from an unlisted approver when a policy scopes it", func() {
		ns := newTestNamespace()
		session := minimalAgentSession(ns, "rt-approver")
		Expect(k8sClient.Create(testCtx, session)).To(Succeed())
		waitForJob(ns, session)

		policy := &scrutineerv1alpha1.ApprovalPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "deploy-approvers", Namespace: ns},
			Spec: scrutineerv1alpha1.ApprovalPolicySpec{
				Actions: []string{"deploy"},
				Approvers: []scrutineerv1alpha1.ApprovalSubject{
					{Kind: scrutineerv1alpha1.ApprovalSubjectUser, Name: "alice"},
				},
			},
		}
		Expect(k8sClient.Create(testCtx, policy)).To(Succeed())

		reqKey := types.NamespacedName{Namespace: ns, Name: "rt-approver-deploy"}
		req := newRuntimeApprovalRequest(ns, reqKey.Name, session.Name, "deploy", "deploy-prod", policy.Name)
		Expect(k8sClient.Create(testCtx, req)).To(Succeed())
		waitForApprovalState(reqKey, scrutineerv1alpha1.ApprovalStatePending)

		// An unlisted approver's grant is rejected: the request stays Pending.
		patchApprovalDecision(reqKey, scrutineerv1alpha1.ApprovalDecisionGranted, "mallory")
		Consistently(func(g Gomega) {
			var got scrutineerv1alpha1.ApprovalRequest
			g.Expect(k8sClient.Get(testCtx, reqKey, &got)).To(Succeed())
			g.Expect(got.Status.State).To(Equal(scrutineerv1alpha1.ApprovalStatePending))
		}, 2*time.Second, controllerPollInterval).Should(Succeed())

		// A listed approver's grant is honored.
		patchApprovalDecision(reqKey, scrutineerv1alpha1.ApprovalDecisionGranted, "alice")
		waitForApprovalState(reqKey, scrutineerv1alpha1.ApprovalStateGranted)
	})
})

var _ = Describe("Runtime approval helpers", func() {
	It("treats only trigger=runtime as a runtime hold", func() {
		Expect(scrutineerv1alpha1.ApprovalRequestSpec{Trigger: scrutineerv1alpha1.ApprovalTriggerRuntime}.IsRuntime()).To(BeTrue())
		Expect(scrutineerv1alpha1.ApprovalRequestSpec{Trigger: scrutineerv1alpha1.ApprovalTriggerSession}.IsRuntime()).To(BeFalse())
		Expect(scrutineerv1alpha1.ApprovalRequestSpec{}.IsRuntime()).To(BeFalse(), "empty trigger means session")
	})

	It("prefers scope.target then action for the decision subject", func() {
		withTarget := &scrutineerv1alpha1.ApprovalRequest{Spec: scrutineerv1alpha1.ApprovalRequestSpec{
			Action: "deploy", Scope: scrutineerv1alpha1.ApprovalScope{Target: "deploy-prod"},
		}}
		withoutTarget := &scrutineerv1alpha1.ApprovalRequest{Spec: scrutineerv1alpha1.ApprovalRequestSpec{Action: "deploy"}}
		Expect(runtimeApprovalTarget(withTarget)).To(Equal("deploy-prod"))
		Expect(runtimeApprovalTarget(withoutTarget)).To(Equal("deploy"))
	})

	It("reports decided states as final", func() {
		Expect(approvalStateDecided(scrutineerv1alpha1.ApprovalStateGranted)).To(BeTrue())
		Expect(approvalStateDecided(scrutineerv1alpha1.ApprovalStateDenied)).To(BeTrue())
		Expect(approvalStateDecided(scrutineerv1alpha1.ApprovalStateExpired)).To(BeTrue())
		Expect(approvalStateDecided(scrutineerv1alpha1.ApprovalStatePending)).To(BeFalse())
		Expect(approvalStateDecided(scrutineerv1alpha1.ApprovalState(""))).To(BeFalse())
	})

	It("projects a redaction-safe summary (argDigest, never raw args) and defaults empty state to Pending", func() {
		created := metav1.NewTime(time.Now().Add(-time.Minute))
		req := newRuntimeApprovalRequest("team-a", "rt-sum-deploy", "sess", "deploy", "deploy-prod", "deploy-approvers")
		req.CreationTimestamp = created
		req.Status.Reason = "awaiting decision"

		s := runtimeApprovalSummary(req)
		Expect(s.Name).To(Equal("rt-sum-deploy"))
		Expect(s.RequestID).To(Equal("rt-sum-deploy-rid"))
		Expect(s.Action).To(Equal("deploy"))
		Expect(s.Target).To(Equal("deploy-prod"))
		Expect(s.ArgDigest).To(Equal("sha256:deadbeef"))
		Expect(s.PolicyRef).To(Equal("deploy-approvers"))
		Expect(s.Reason).To(Equal("awaiting decision"))
		Expect(s.RequestedAt).NotTo(BeNil())
		Expect(s.State).To(Equal(scrutineerv1alpha1.ApprovalStatePending), "empty observed state surfaces as Pending")
	})

	It("sorts, caps, and clears the pending-approval summary", func() {
		session := &scrutineerv1alpha1.AgentSession{}
		setPendingApprovals(session, []scrutineerv1alpha1.RuntimeApprovalSummary{
			{Name: "b"}, {Name: "a"}, {Name: "c"},
		})
		Expect(session.Status.PendingApprovals).To(HaveLen(3))
		Expect(session.Status.PendingApprovals[0].Name).To(Equal("a"))
		Expect(session.Status.PendingApprovals[2].Name).To(Equal("c"))

		over := make([]scrutineerv1alpha1.RuntimeApprovalSummary, maxPendingApprovals+5)
		for i := range over {
			over[i].Name = fmt.Sprintf("h-%03d", i)
		}
		setPendingApprovals(session, over)
		Expect(session.Status.PendingApprovals).To(HaveLen(maxPendingApprovals))

		setPendingApprovals(session, nil)
		Expect(session.Status.PendingApprovals).To(BeNil())
	})

	It("derives the validity window from policy first, then scope.window", func() {
		policy := &scrutineerv1alpha1.ApprovalPolicy{Spec: scrutineerv1alpha1.ApprovalPolicySpec{
			ExpiresAfter: &metav1.Duration{Duration: 30 * time.Minute},
		}}
		reqWindow := &scrutineerv1alpha1.ApprovalRequest{Spec: scrutineerv1alpha1.ApprovalRequestSpec{
			Scope: scrutineerv1alpha1.ApprovalScope{Window: &metav1.Duration{Duration: 5 * time.Minute}},
		}}
		Expect(approvalValidityWindow(policy, reqWindow)).To(Equal(30 * time.Minute))
		Expect(approvalValidityWindow(nil, reqWindow)).To(Equal(5 * time.Minute))
		Expect(approvalValidityWindow(nil, &scrutineerv1alpha1.ApprovalRequest{})).To(BeZero())
	})
})
