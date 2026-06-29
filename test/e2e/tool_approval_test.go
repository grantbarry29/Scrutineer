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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"sigs.k8s.io/controller-runtime/pkg/client"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/enforcement/toolgateway"
)

var _ = Describe("Live runtime tool approval", func() {
	BeforeEach(func(ctx SpecContext) {
		requireLiveToolEvidenceImages(ctx)
		deployInClusterReporter(ctx)
	})

	It("holds a requireHumanApproval tool call until a grant, then records a redacted approval decision", func(ctx SpecContext) {
		const tool = "deploy"
		// SECRETTOKEN is the request arg value that must never appear in evidence.
		const secretValue = "prod-cluster-SECRETTOKEN"
		ns := newTestNamespace("scrutineer-e2e-tool-approval")
		const profileName = "tool-gateway-enforced"
		createRuntimeProfileWithToolGateway(ctx, ns, profileName)
		createEnforcedApprovalPolicy(ctx, ns, "approve-deploy", tool)

		session := newAgentSession(ns, "approval-hold",
			withRuntimeProfileRef(profileName),
			withPolicyRef("AgentPolicy", "approve-deploy"),
			withApprovalHoldToolInvokeProbe(tool, `{"target":"`+secretValue+`"}`),
		)
		key := createAgentSession(ctx, session)

		expectJobForSession(ctx, ns, session)
		waitForPhase(ctx, key, []scrutineerv1alpha1.AgentSessionPhase{scrutineerv1alpha1.PhaseRunning}, 60*time.Second, time.Second)

		By("waiting for the gateway to register a runtime ApprovalRequest, then granting it")
		reqName := waitForRuntimeApprovalRequest(ctx, ns, session.Name)
		grantRuntimeApproval(ctx, ns, reqName, "e2e-approver@scrutineer.test")

		By("confirming the controller granted the hold without gating the session phase")
		Eventually(func(g Gomega) {
			var req scrutineerv1alpha1.ApprovalRequest
			g.Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: ns, Name: reqName}, &req)).To(Succeed())
			g.Expect(req.Status.State).To(Equal(scrutineerv1alpha1.ApprovalStateGranted))
		}, 60*time.Second, 2*time.Second).Should(Succeed())
		Expect(getSession(ctx, key).Status.Phase).To(Equal(scrutineerv1alpha1.PhaseRunning),
			"runtime approval must not change the session phase")

		By("confirming the gateway records a redacted runtime approval decision after the grant")
		Eventually(func(g Gomega) {
			got := getSession(ctx, key)

			var granted *scrutineerv1alpha1.PolicyDecision
			for i := range got.Status.PolicyDecisions {
				d := &got.Status.PolicyDecisions[i]
				if d.Phase == scrutineerv1alpha1.PolicyDecisionPhaseRuntime && d.Type == "approval" &&
					d.Action == scrutineerv1alpha1.PolicyDecisionAllow && d.Reason == toolgateway.ReasonApprovalGranted {
					granted = d
					break
				}
			}
			g.Expect(granted).NotTo(BeNil(), "expected an allowed runtime approval decision; decisions=%+v", got.Status.PolicyDecisions)
			g.Expect(granted.Target).To(Equal(tool))
			g.Expect(granted.Rule).To(Equal("requireHumanApproval"))
			g.Expect(granted.Message).To(ContainSubstring("argDigest=sha256:"))

			// Redaction invariant: the request arg value never appears in any evidence.
			for _, d := range got.Status.PolicyDecisions {
				g.Expect(d.Message).NotTo(ContainSubstring("SECRETTOKEN"), "decision leaks request arg value: %q", d.Message)
			}
			for _, v := range got.Status.Violations {
				g.Expect(v.Message).NotTo(ContainSubstring("SECRETTOKEN"), "violation leaks request arg value: %q", v.Message)
			}
		}, 120*time.Second, 2*time.Second).Should(Succeed())

		requestCancellation(ctx, key)
		waitForTerminalPhase(ctx, key, scrutineerv1alpha1.PhaseCancelled)
	})
})
