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
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/enforcement/envoy"
)

// #32 live proof: FQDN policy enforced at the per-session Envoy, surfaced as observed
// evidence. Enforced mode blocks a denied host and records an observed deny; audit mode
// lets it through but records an observed dry-run. Runs on any enforcing CNI.
var _ = Describe("Live FQDN egress policy at Envoy", Label(labelNetworking), func() {
	BeforeEach(func(ctx SpecContext) {
		requireScrutineerE2EImage(ctx)
		if !clusterImageRunnable(ctx, envoy.DefaultEnvoyImage) {
			Skip("envoy image not available — run: make kind-load-envoy")
		}
		if !clusterImageRunnable(ctx, envoy.DefaultEgressReporterImage()) {
			Skip("egress-reporter image not available — run: make kind-load-egress-reporter")
		}
		deployInClusterReporter(ctx)
	})

	// probeHost is denied via a wildcard so the test also exercises subdomain matching.
	const probeHost = "c2.tracker.scrutineer.invalid"

	It("blocks a denied domain and records an observed deny (enforced)", func(ctx SpecContext) {
		// Enforced-mode egress sessions require a verified routing lock (#70); on a
		// non-enforcing CNI the gate correctly refuses them, so this spec is
		// meaningful only where the lock is real.
		requireEgressEnforcingCNI(ctx)
		ns := newTestNamespace("scrutineer-e2e-fqdn-enf")
		createRuntimeProfileWithEnvoy(ctx, ns, "envoy-egress")
		createFQDNDenyPolicy(ctx, ns, "fqdn-deny", scrutineerv1alpha1.PolicyModeEnforced, "*.tracker.scrutineer.invalid")

		session := newAgentSession(ns, "fqdn-enforced",
			withRuntimeProfileRef("envoy-egress"),
			withPolicyRef("AgentPolicy", "fqdn-deny"),
			withEnvoyEgressProbe(probeHost),
		)
		key := createAgentSession(ctx, session)
		expectJobForSession(ctx, ns, session)
		waitForPhase(ctx, key, []scrutineerv1alpha1.AgentSessionPhase{scrutineerv1alpha1.PhaseRunning}, 90*time.Second, 2*time.Second)

		By("an observed DENY decision for the denied host appearing in status")
		expectObservedDecision(ctx, key, probeHost, scrutineerv1alpha1.PolicyDecisionDeny)

		By("Envoy having blocked it (403 in the access log)")
		Eventually(func(g Gomega) {
			logs := envoyAccessLog(ctx, envoyKey(ns, session.Name))
			g.Expect(logs).To(ContainSubstring(probeHost))
			g.Expect(logs).To(ContainSubstring("403"), "denied egress should be 403-blocked by RBAC; log:\n%s", logs)
		}, 120*time.Second, 3*time.Second).Should(Succeed())

		requestCancellation(ctx, key)
		waitForTerminalPhase(ctx, key, scrutineerv1alpha1.PhaseCancelled)
	})

	It("records an observed dry-run for a would-be denial (audit)", func(ctx SpecContext) {
		ns := newTestNamespace("scrutineer-e2e-fqdn-audit")
		createRuntimeProfileWithEnvoy(ctx, ns, "envoy-egress")
		createFQDNDenyPolicy(ctx, ns, "fqdn-audit", scrutineerv1alpha1.PolicyModeAuditOnly, "*.tracker.scrutineer.invalid")

		session := newAgentSession(ns, "fqdn-audit",
			withRuntimeProfileRef("envoy-egress"),
			withPolicyRef("AgentPolicy", "fqdn-audit"),
			withEnvoyEgressProbe(probeHost),
		)
		key := createAgentSession(ctx, session)
		expectJobForSession(ctx, ns, session)
		waitForPhase(ctx, key, []scrutineerv1alpha1.AgentSessionPhase{scrutineerv1alpha1.PhaseRunning}, 90*time.Second, 2*time.Second)

		By("an observed DRY-RUN decision (traffic allowed, would-be-denied recorded)")
		expectObservedDecision(ctx, key, probeHost, scrutineerv1alpha1.PolicyDecisionDryRun)

		requestCancellation(ctx, key)
		waitForTerminalPhase(ctx, key, scrutineerv1alpha1.PhaseCancelled)
	})
})

func envoyKey(ns, sessionName string) types.NamespacedName {
	return types.NamespacedName{Namespace: ns, Name: envoy.ResourceName(sessionName)}
}

// createFQDNDenyPolicy creates an AgentPolicy that denies the given domain in the given mode.
func createFQDNDenyPolicy(ctx context.Context, ns, name string, mode scrutineerv1alpha1.PolicyMode, domain string) {
	GinkgoHelper()
	Expect(k8sClient.Create(ctx, &scrutineerv1alpha1.AgentPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: scrutineerv1alpha1.AgentPolicySpec{
			Mode:        mode,
			PolicyRules: scrutineerv1alpha1.PolicyRules{DeniedDomains: []string{domain}},
		},
	})).To(Succeed())
}

// expectObservedDecision waits for a runtime network decision for host with the given
// action, stamped observed (produced by the out-of-pod egress proxy).
func expectObservedDecision(ctx context.Context, key client.ObjectKey, host string, action scrutineerv1alpha1.PolicyDecisionAction) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		got := getSession(ctx, key)
		var match *scrutineerv1alpha1.PolicyDecision
		for i := range got.Status.PolicyDecisions {
			d := &got.Status.PolicyDecisions[i]
			if d.Type != "network" || d.Actor != envoy.AccessLogActor {
				continue
			}
			// Port-agnostic: the proxy logs the authority as the bare host or host:<port>.
			// Matching any :<port> (not only :443) is what lets non-443 CONNECT-tunnel
			// authorities (#123/#162) be asserted the same way as HTTPS on :443.
			if d.Target == host || strings.HasPrefix(d.Target, host+":") {
				match = d
			}
		}
		g.Expect(match).NotTo(BeNil(), "no egress-proxy decision for %q; decisions=%+v", host, got.Status.PolicyDecisions)
		g.Expect(match.Action).To(Equal(action), "decision=%+v", match)
		g.Expect(match.AssuranceLevel).To(Equal(scrutineerv1alpha1.EvidenceObserved),
			"FQDN decisions from the proxy must be observed: %+v", match)
	}, 150*time.Second, 3*time.Second).Should(Succeed())
}
