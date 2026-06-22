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
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
	"github.com/secureai/relay/internal/enforcement/toolgateway"
)

var _ = Describe("Live tool violation population", func() {
	BeforeEach(func(ctx SpecContext) {
		requireLiveToolEvidenceImages(ctx)
		deployInClusterReporter(ctx)
	})

	It("populates status.violations when tool-gateway denies an enforced tool in a running pod", func(ctx SpecContext) {
		const deniedTool = "kubectl"
		ns := newTestNamespace("relay-e2e-tool-violation")
		const profileName = "tool-gateway-enforced"
		createRuntimeProfileWithToolGateway(ctx, ns, profileName)
		createEnforcedDeniedToolPolicy(ctx, ns, "deny-kubectl", deniedTool)

		session := newAgentSession(ns, "tool-violation",
			withRuntimeProfileRef(profileName),
			withPolicyRef("ToolPolicy", "deny-kubectl"),
			withDeniedToolInvokeProbe(deniedTool),
		)
		key := createAgentSession(ctx, session)

		expectJobForSession(ctx, ns, session)
		waitForPhase(ctx, key, []relayv1alpha1.AgentSessionPhase{relayv1alpha1.PhaseRunning}, 60*time.Second, time.Second)

		Eventually(func(g Gomega) {
			got := getSession(ctx, key)
			var runtimeViolations []relayv1alpha1.PolicyViolation
			for _, v := range got.Status.Violations {
				if v.Target == deniedTool || strings.Contains(v.Message, deniedTool) {
					runtimeViolations = append(runtimeViolations, v)
				}
			}
			g.Expect(runtimeViolations).NotTo(BeEmpty(), "expected violation for denied tool %q; violations=%+v", deniedTool, got.Status.Violations)

			var runtimeDecisions []relayv1alpha1.PolicyDecision
			for _, d := range got.Status.PolicyDecisions {
				if d.Phase == relayv1alpha1.PolicyDecisionPhaseRuntime && d.Target == deniedTool {
					runtimeDecisions = append(runtimeDecisions, d)
				}
			}
			g.Expect(runtimeDecisions).NotTo(BeEmpty())
			g.Expect(runtimeDecisions[0].Action).To(Equal(relayv1alpha1.PolicyDecisionDeny))
			g.Expect(runtimeDecisions[0].Type).To(Equal("tool"))

			g.Expect(got.Status.Usage).NotTo(BeNil(), "expected status.usage after runtime tool decision")
			g.Expect(got.Status.Usage.ToolCalls).To(BeNumerically(">=", 1),
				"expected toolCalls from novel type:tool runtime decision; usage=%+v", got.Status.Usage)
		}, 90*time.Second, 2*time.Second).Should(Succeed())

		By("confirming the session pod has tool-gateway and agent containers")
		got := getSession(ctx, key)
		Expect(got.Status.PodName).NotTo(BeEmpty())
		var pod corev1.Pod
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: ns, Name: got.Status.PodName}, &pod)).To(Succeed())
		names := containerNames(pod.Spec.Containers)
		Expect(names).To(ContainElements("agent", "tools"))

		requestCancellation(ctx, key)
		waitForTerminalPhase(ctx, key, relayv1alpha1.PhaseCancelled)
	})

	It("populates a redacted argument violation when tool-gateway denies a tool by argument rule", func(ctx SpecContext) {
		const tool = "read_file"
		// SECRETTOKEN is the request value that must never appear in evidence; "/etc/" is
		// the policy operand that may.
		const secretValue = "/etc/shadow-SECRETTOKEN"
		ns := newTestNamespace("relay-e2e-arg-violation")
		const profileName = "tool-gateway-enforced"
		createRuntimeProfileWithToolGateway(ctx, ns, profileName)
		createEnforcedArgumentRuleToolPolicy(ctx, ns, "deny-etc-read", tool, "path", "/etc/")

		session := newAgentSession(ns, "arg-violation",
			withRuntimeProfileRef(profileName),
			withPolicyRef("ToolPolicy", "deny-etc-read"),
			withArgumentDeniedToolInvokeProbe(tool, fmt.Sprintf(`{"path":"%s"}`, secretValue)),
		)
		key := createAgentSession(ctx, session)

		expectJobForSession(ctx, ns, session)
		waitForPhase(ctx, key, []relayv1alpha1.AgentSessionPhase{relayv1alpha1.PhaseRunning}, 60*time.Second, time.Second)

		Eventually(func(g Gomega) {
			got := getSession(ctx, key)

			var argDecisions []relayv1alpha1.PolicyDecision
			for _, d := range got.Status.PolicyDecisions {
				if d.Phase == relayv1alpha1.PolicyDecisionPhaseRuntime && d.Reason == toolgateway.ReasonArgumentDenied {
					argDecisions = append(argDecisions, d)
				}
			}
			g.Expect(argDecisions).NotTo(BeEmpty(), "expected ArgumentDenied runtime decision; decisions=%+v", got.Status.PolicyDecisions)
			g.Expect(argDecisions[0].Action).To(Equal(relayv1alpha1.PolicyDecisionDeny))
			g.Expect(argDecisions[0].Type).To(Equal("tool"))
			g.Expect(argDecisions[0].Rule).To(Equal("argumentRules"))
			g.Expect(argDecisions[0].Target).To(Equal(tool))

			// Evidence must be redacted: the request value never appears in any decision.
			for _, d := range got.Status.PolicyDecisions {
				g.Expect(d.Message).NotTo(ContainSubstring("SECRETTOKEN"), "decision leaks request arg value: %q", d.Message)
			}

			var argViolations []relayv1alpha1.PolicyViolation
			for _, v := range got.Status.Violations {
				if v.Type == "tool" && strings.Contains(v.Message, "argument") {
					argViolations = append(argViolations, v)
				}
			}
			g.Expect(argViolations).NotTo(BeEmpty(), "expected an argument violation; violations=%+v", got.Status.Violations)
			for _, v := range got.Status.Violations {
				g.Expect(v.Message).NotTo(ContainSubstring("SECRETTOKEN"), "violation leaks request arg value: %q", v.Message)
			}
		}, 90*time.Second, 2*time.Second).Should(Succeed())

		requestCancellation(ctx, key)
		waitForTerminalPhase(ctx, key, relayv1alpha1.PhaseCancelled)
	})
})
