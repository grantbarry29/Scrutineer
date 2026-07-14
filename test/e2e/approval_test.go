//go:build e2e

/*
Copyright 2026 The Scrutineer Authors.

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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/controller/agentsession"
)

// Approval-flow e2e (#146): the human-in-the-loop gate against a real runtime.
// Unit/envtest cover the state machine; these specs prove the e2e-shaped
// guarantees — no Job exists while a session is held, a grant is what creates
// the runtime, a denial/timeout leaves no runtime behind, and mid-run holds
// never gate the session phase. The identity-webhook e2e is #6, not here.
var _ = Describe("Human approval workflows", func() {

	const approver = "e2e-approver"

	// decideApproval sets the human decision on an ApprovalRequest, conflict-safe
	// against the in-process controller's concurrent status writes.
	decideApproval := func(ctx context.Context, key client.ObjectKey, decision scrutineerv1alpha1.ApprovalDecision, decidedBy string) {
		GinkgoHelper()
		Eventually(func(g Gomega) {
			var req scrutineerv1alpha1.ApprovalRequest
			g.Expect(k8sClient.Get(ctx, key, &req)).To(Succeed())
			if req.Spec.Decision == decision {
				return
			}
			req.Spec.Decision = decision
			req.Spec.DecidedBy = decidedBy
			g.Expect(k8sClient.Update(ctx, &req)).To(Succeed())
		}, 15*time.Second, 200*time.Millisecond).Should(Succeed())
	}

	// approvalDecision returns the session's approval policyDecision for (target, action).
	approvalDecision := func(s *scrutineerv1alpha1.AgentSession, target string, action scrutineerv1alpha1.PolicyDecisionAction) *scrutineerv1alpha1.PolicyDecision {
		for i := range s.Status.PolicyDecisions {
			d := s.Status.PolicyDecisions[i]
			if d.Type == "approval" && d.Target == target && d.Action == action {
				return &d
			}
		}
		return nil
	}

	// waitForHold waits until the session is held in AwaitingApproval with its
	// ApprovalRequest pending, and returns the request key.
	waitForHold := func(ctx context.Context, key client.ObjectKey) client.ObjectKey {
		GinkgoHelper()
		waitForPhase(ctx, key, []scrutineerv1alpha1.AgentSessionPhase{scrutineerv1alpha1.PhaseAwaitingApproval},
			60*time.Second, 500*time.Millisecond)
		reqKey := key // 1:1 MVP naming: ApprovalRequest name = session name
		Eventually(func(g Gomega) {
			var req scrutineerv1alpha1.ApprovalRequest
			g.Expect(k8sClient.Get(ctx, reqKey, &req)).To(Succeed())
			g.Expect(req.Status.State).To(Equal(scrutineerv1alpha1.ApprovalStatePending))
		}, 30*time.Second, 500*time.Millisecond).Should(Succeed())
		return reqKey
	}

	It("holds a gated session with no Job, then a grant creates the runtime and the session succeeds", func(ctx SpecContext) {
		ns := newTestNamespace("scrutineer-e2e-approve")
		createApprovalPolicy(ctx, ns, "gate-deploys", []string{"deploy"}, withApprovers(approver))
		session := newAgentSession(ns, "needs-approval",
			withRequireHumanApproval("deploy"),
			withCommand("sh", "-c", "echo approved-run; exit 0"),
		)
		key := createAgentSession(ctx, session)

		By("the gate holding the session before any runtime exists")
		reqKey := waitForHold(ctx, key)
		held := getSession(ctx, key)
		expectCondition(&held, agentsession.ConditionApprovalRequired, metav1.ConditionTrue, "AwaitingApproval")

		var req scrutineerv1alpha1.ApprovalRequest
		Expect(k8sClient.Get(ctx, reqKey, &req)).To(Succeed())
		Expect(req.OwnerReferences).NotTo(BeEmpty())
		Expect(req.OwnerReferences[0].Kind).To(Equal("AgentSession"))
		Expect(req.OwnerReferences[0].UID).To(Equal(held.UID))
		Expect(req.Spec.Action).To(Equal("deploy"))
		Expect(req.Spec.PolicyRef).To(Equal("gate-deploys"))

		By("no Job existing while the session is held")
		expectNoJobForSession(ctx, ns, session)

		By("a listed approver granting the request")
		decideApproval(ctx, reqKey, scrutineerv1alpha1.ApprovalDecisionGranted, approver)

		By("the grant resuming the session to completion")
		waitForTerminalPhase(ctx, key, scrutineerv1alpha1.PhaseSucceeded)
		got := getSession(ctx, key)
		expectCondition(&got, agentsession.ConditionApprovalRequired, metav1.ConditionFalse, "Approved")
		expectCondition(&got, agentsession.ConditionRuntimeCreated, metav1.ConditionTrue, "JobCreated")
		expectJobForSession(ctx, ns, session)

		By("the audit trail recording who granted")
		decision := approvalDecision(&got, "deploy", scrutineerv1alpha1.PolicyDecisionAllow)
		Expect(decision).NotTo(BeNil(), "expected an approval allow decision in status.policyDecisions")
		Expect(decision.Actor).To(Equal(approver))
		Expect(decision.AssuranceLevel).To(Equal(scrutineerv1alpha1.EvidenceControllerComputed))

		Expect(k8sClient.Get(ctx, reqKey, &req)).To(Succeed())
		Expect(req.Status.State).To(Equal(scrutineerv1alpha1.ApprovalStateGranted))
		Expect(req.Status.DecidedBy).To(Equal(approver))
		Expect(req.Status.DecidedAt).NotTo(BeNil())
	})

	It("denies the session terminally on a denied approval; no Job is ever created", func(ctx SpecContext) {
		ns := newTestNamespace("scrutineer-e2e-approve-deny")
		createApprovalPolicy(ctx, ns, "gate-deploys", []string{"deploy"}, withApprovers(approver))
		session := newAgentSession(ns, "denied-approval", withRequireHumanApproval("deploy"))
		key := createAgentSession(ctx, session)

		reqKey := waitForHold(ctx, key)
		decideApproval(ctx, reqKey, scrutineerv1alpha1.ApprovalDecisionDenied, approver)

		waitForDeniedPhase(ctx, key)
		got := getSession(ctx, key)
		expectCondition(&got, agentsession.ConditionApprovalRequired, metav1.ConditionFalse, "ApprovalDenied")
		Expect(got.Status.Result).NotTo(BeNil())
		Expect(got.Status.Result.Outcome).To(Equal("denied"))
		Expect(approvalDecision(&got, "deploy", scrutineerv1alpha1.PolicyDecisionDeny)).NotTo(BeNil(),
			"expected an approval deny decision in status.policyDecisions")

		var req scrutineerv1alpha1.ApprovalRequest
		Expect(k8sClient.Get(ctx, reqKey, &req)).To(Succeed())
		Expect(req.Status.State).To(Equal(scrutineerv1alpha1.ApprovalStateDenied))

		By("no Job ever having been created")
		expectNoJobForSession(ctx, ns, session)
		Expect(getCondition(&got, agentsession.ConditionRuntimeCreated)).To(BeNil())
	})

	It("denies a held session when the decision deadline lapses (onTimeout=deny)", func(ctx SpecContext) {
		ns := newTestNamespace("scrutineer-e2e-approve-timeout")
		createApprovalPolicy(ctx, ns, "gate-deploys", []string{"deploy"},
			withDecisionDeadline(5*time.Second, scrutineerv1alpha1.ApprovalTimeoutDeny))
		session := newAgentSession(ns, "timed-out-approval", withRequireHumanApproval("deploy"))
		key := createAgentSession(ctx, session)

		reqKey := waitForHold(ctx, key)

		// No decision: the gate's recheck backstop must expire the request and
		// deny the session on its own.
		waitForDeniedPhase(ctx, key)
		got := getSession(ctx, key)
		expectCondition(&got, agentsession.ConditionApprovalRequired, metav1.ConditionFalse, "ApprovalExpired")
		Expect(got.Status.Result).NotTo(BeNil())
		Expect(got.Status.Result.Outcome).To(Equal("denied"))
		Expect(approvalDecision(&got, "deploy", scrutineerv1alpha1.PolicyDecisionDeny)).NotTo(BeNil())

		var req scrutineerv1alpha1.ApprovalRequest
		Expect(k8sClient.Get(ctx, reqKey, &req)).To(Succeed())
		Expect(req.Status.State).To(Equal(scrutineerv1alpha1.ApprovalStateExpired))

		expectNoJobForSession(ctx, ns, session)
	})

	It("resolves a mid-run runtime approval without gating the session phase", func(ctx SpecContext) {
		ns := newTestNamespace("scrutineer-e2e-approve-runtime")
		session := newAgentSession(ns, "midrun", withLongRunningCommand())
		key := createAgentSession(ctx, session)

		waitForPhase(ctx, key, []scrutineerv1alpha1.AgentSessionPhase{scrutineerv1alpha1.PhaseRunning},
			120*time.Second, 500*time.Millisecond)

		By("raising a runtime hold for a tool call")
		hold := &scrutineerv1alpha1.ApprovalRequest{
			ObjectMeta: metav1.ObjectMeta{Name: "midrun-tool-1", Namespace: ns},
			Spec: scrutineerv1alpha1.ApprovalRequestSpec{
				SessionRef: scrutineerv1alpha1.ApprovalSessionRef{Name: session.Name},
				Trigger:    scrutineerv1alpha1.ApprovalTriggerRuntime,
				RequestID:  "req-1",
				Action:     "tool",
				Scope:      scrutineerv1alpha1.ApprovalScope{Target: "kubectl-apply"},
			},
		}
		Expect(k8sClient.Create(ctx, hold)).To(Succeed())
		holdKey := client.ObjectKeyFromObject(hold)

		By("the hold surfacing in status.pendingApprovals while the session keeps running")
		Eventually(func(g Gomega) {
			got := getSession(ctx, key)
			g.Expect(got.Status.Phase).To(Equal(scrutineerv1alpha1.PhaseRunning))
			g.Expect(got.Status.PendingApprovals).To(HaveLen(1))
			g.Expect(got.Status.PendingApprovals[0].Name).To(Equal("midrun-tool-1"))
			g.Expect(got.Status.PendingApprovals[0].RequestID).To(Equal("req-1"))
			g.Expect(got.Status.PendingApprovals[0].Target).To(Equal("kubectl-apply"))
			g.Expect(got.Status.PendingApprovals[0].State).To(Equal(scrutineerv1alpha1.ApprovalStatePending))
		}, 60*time.Second, 500*time.Millisecond).Should(Succeed())

		By("granting the hold")
		decideApproval(ctx, holdKey, scrutineerv1alpha1.ApprovalDecisionGranted, approver)

		By("the decision resolving and clearing the pending summary, phase untouched")
		Eventually(func(g Gomega) {
			var req scrutineerv1alpha1.ApprovalRequest
			g.Expect(k8sClient.Get(ctx, holdKey, &req)).To(Succeed())
			g.Expect(req.Status.State).To(Equal(scrutineerv1alpha1.ApprovalStateGranted))
			g.Expect(req.Status.DecidedBy).To(Equal(approver))
			g.Expect(req.Status.DecidedAt).NotTo(BeNil())
			got := getSession(ctx, key)
			g.Expect(got.Status.PendingApprovals).To(BeEmpty())
			g.Expect(got.Status.Phase).To(Equal(scrutineerv1alpha1.PhaseRunning))
		}, 60*time.Second, 500*time.Millisecond).Should(Succeed())

		Consistently(func() scrutineerv1alpha1.AgentSessionPhase {
			return getSession(ctx, key).Status.Phase
		}, 5*time.Second, 500*time.Millisecond).Should(Equal(scrutineerv1alpha1.PhaseRunning),
			"a runtime approval must never gate the session phase")

		requestCancellation(ctx, key)
		waitForTerminalPhase(ctx, key, scrutineerv1alpha1.PhaseCancelled)
	})
})
