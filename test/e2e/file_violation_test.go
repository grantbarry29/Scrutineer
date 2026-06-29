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
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
)

var _ = Describe("Live file violation population", func() {
	BeforeEach(func(ctx SpecContext) {
		requireLiveFileEvidenceImages(ctx)
		deployInClusterReporter(ctx)
	})

	It("populates status.violations when fs-gateway denies enforced path access in a running pod", func(ctx SpecContext) {
		const deniedPath = "/etc/passwd"
		const deniedGlob = "/etc/**"
		ns := newTestNamespace("scrutineer-e2e-file-violation")
		const profileName = "fs-gateway-enforced"
		createRuntimeProfileWithFSGateway(ctx, ns, profileName)
		createEnforcedDeniedPathPolicy(ctx, ns, "deny-etc", deniedGlob)

		session := newAgentSession(ns, "file-violation",
			withRuntimeProfileRef(profileName),
			withPolicyRef("AgentPolicy", "deny-etc"),
			withDeniedPathAccessProbe(deniedPath),
		)
		key := createAgentSession(ctx, session)

		expectJobForSession(ctx, ns, session)
		waitForPhase(ctx, key, []scrutineerv1alpha1.AgentSessionPhase{scrutineerv1alpha1.PhaseRunning}, 60*time.Second, time.Second)

		Eventually(func(g Gomega) {
			got := getSession(ctx, key)
			var runtimeViolations []scrutineerv1alpha1.PolicyViolation
			for _, v := range got.Status.Violations {
				if v.Target == deniedPath || strings.Contains(v.Message, deniedPath) {
					runtimeViolations = append(runtimeViolations, v)
				}
			}
			g.Expect(runtimeViolations).NotTo(BeEmpty(), "expected violation for denied path %q; violations=%+v", deniedPath, got.Status.Violations)

			var runtimeDecisions []scrutineerv1alpha1.PolicyDecision
			for _, d := range got.Status.PolicyDecisions {
				if d.Phase == scrutineerv1alpha1.PolicyDecisionPhaseRuntime && d.Target == deniedPath {
					runtimeDecisions = append(runtimeDecisions, d)
				}
			}
			g.Expect(runtimeDecisions).NotTo(BeEmpty())
			g.Expect(runtimeDecisions[0].Action).To(Equal(scrutineerv1alpha1.PolicyDecisionDeny))
			g.Expect(runtimeDecisions[0].Type).To(Equal("file"))

			g.Expect(got.Status.Usage).NotTo(BeNil(), "expected status.usage after runtime file decision")
			g.Expect(got.Status.Usage.FileOperations).To(BeNumerically(">=", 1),
				"expected fileOperations from novel type:file runtime decision; usage=%+v", got.Status.Usage)
		}, 90*time.Second, 2*time.Second).Should(Succeed())

		By("confirming the session pod has fs-gateway and agent containers")
		got := getSession(ctx, key)
		Expect(got.Status.PodName).NotTo(BeEmpty())
		var pod corev1.Pod
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: ns, Name: got.Status.PodName}, &pod)).To(Succeed())
		names := containerNames(pod.Spec.Containers)
		Expect(names).To(ContainElements("agent", "files"))

		requestCancellation(ctx, key)
		waitForTerminalPhase(ctx, key, scrutineerv1alpha1.PhaseCancelled)
	})
})
