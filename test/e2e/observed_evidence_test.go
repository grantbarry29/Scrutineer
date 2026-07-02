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

	"k8s.io/apimachinery/pkg/types"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/enforcement/envoy"
)

// Slice C (#62) live proof: egress through the per-session Envoy lands in
// status.policyDecisions stamped `observed` — evidence produced outside the agent's
// trust domain, authenticated by the egress-proxy pod's own identity. Complements the
// unit/handler tests that prove the negative (agent-adjacent callers cannot claim
// observed); here we prove the positive end to end: Envoy access log → egress-reporter
// → reporter identity path → status.
var _ = Describe("Live observed egress evidence", Label(labelNetworking), func() {
	BeforeEach(func(ctx SpecContext) {
		requireScrutineerE2EImage(ctx)
		if !clusterImageRunnable(ctx, envoy.DefaultEnvoyImage) {
			Skip("envoy image not available in cluster — run: make kind-load-envoy")
		}
		if !clusterImageRunnable(ctx, envoy.DefaultEgressReporterImage) {
			Skip("egress-reporter image not available in cluster — run: make kind-load-egress-reporter")
		}
		deployInClusterReporter(ctx)
	})

	It("stamps Envoy-observed egress decisions as observed in session status", func(ctx SpecContext) {
		ns := newTestNamespace("scrutineer-e2e-observed")
		const profileName = "envoy-egress"
		createRuntimeProfileWithEnvoy(ctx, ns, profileName)

		// Non-resolvable on purpose: the evidence records the attempt regardless of
		// upstream success, so the spec needs no internet access.
		const probeHost = "observed.scrutineer.invalid"
		session := newAgentSession(ns, "observed-live",
			withRuntimeProfileRef(profileName),
			withEnvoyEgressProbe(probeHost),
		)
		key := createAgentSession(ctx, session)

		expectJobForSession(ctx, ns, session)
		waitForPhase(ctx, key, []scrutineerv1alpha1.AgentSessionPhase{scrutineerv1alpha1.PhaseRunning}, 90*time.Second, 2*time.Second)
		waitForEnvoyPodReady(ctx, types.NamespacedName{Namespace: ns, Name: envoy.ResourceName(session.Name)})

		By("observed decisions for the probe host appearing in status")
		Eventually(func(g Gomega) {
			got := getSession(ctx, key)
			var observed []scrutineerv1alpha1.PolicyDecision
			for _, d := range got.Status.PolicyDecisions {
				if d.Phase != scrutineerv1alpha1.PolicyDecisionPhaseRuntime || d.Type != "network" {
					continue
				}
				if d.Actor == envoy.AccessLogActor {
					observed = append(observed, d)
				}
			}
			g.Expect(observed).NotTo(BeEmpty(),
				"no egress-proxy decisions in status; decisions=%+v", got.Status.PolicyDecisions)

			foundProbe := false
			for _, d := range observed {
				// Identity-derived assurance: every decision from the proxy identity
				// must be observed — anything else means the identity path regressed.
				g.Expect(d.AssuranceLevel).To(Equal(scrutineerv1alpha1.EvidenceObserved),
					"egress-proxy decision not stamped observed: %+v", d)
				if d.Target == probeHost || d.Target == probeHost+":443" {
					foundProbe = true
				}
			}
			g.Expect(foundProbe).To(BeTrue(),
				"no observed decision for %q; observed=%+v", probeHost, observed)

			// Usage is derived server-side from novel network decisions.
			g.Expect(got.Status.Usage).NotTo(BeNil(), "expected usage after observed egress")
			g.Expect(got.Status.Usage.NetworkRequests).To(BeNumerically(">=", 1))
		}, 150*time.Second, 3*time.Second).Should(Succeed())

		requestCancellation(ctx, key)
		waitForTerminalPhase(ctx, key, scrutineerv1alpha1.PhaseCancelled)
	})
})
