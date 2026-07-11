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
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/enforcement/envoy"
)

// #125 live proof: allowedCIDRs/deniedCIDRs enforced at the per-session Envoy as
// authority matching on IP-literal dials, surfaced as observed evidence. The probe
// CONNECTs to an in-cluster IPv4 literal — its own Envoy's ClusterIP, derived from the
// injected proxy env exactly like the other probes (no DNS under the routing lock).
// Runs on any enforcing CNI.
var _ = Describe("Live CIDR egress policy at Envoy", Label(labelNetworking), func() {
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

	// deniedTarget is a fixed IPv4 literal inside the denied ranges. RBAC matches the
	// CONNECT/Host authority and blocks it (403) before any DNS or connection, so the
	// literal need not be reachable — and it is deliberately NOT the proxy's own address
	// (a self-referential deny that Envoy does not cleanly log).
	rfc1918 := []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"}
	const deniedTarget = "10.0.0.1:9999"

	It("blocks an IP-literal dial into a denied CIDR and records an observed deny (enforced)", func(ctx SpecContext) {
		requireEgressEnforcingCNI(ctx)
		ns := newTestNamespace("scrutineer-e2e-cidr-enf")
		createRuntimeProfileWithEnvoy(ctx, ns, "envoy-egress")
		createCIDRPolicy(ctx, ns, "cidr-deny", scrutineerv1alpha1.PolicyModeEnforced,
			scrutineerv1alpha1.PolicyRules{DeniedCIDRs: rfc1918})

		session := newAgentSession(ns, "cidr-enforced",
			withRuntimeProfileRef("envoy-egress"),
			withPolicyRef("AgentPolicy", "cidr-deny"),
			withEnvoyIPConnectProbe(deniedTarget),
		)
		key := createAgentSession(ctx, session)
		expectJobForSession(ctx, ns, session)
		waitForPhase(ctx, key, []scrutineerv1alpha1.AgentSessionPhase{scrutineerv1alpha1.PhaseRunning}, 90*time.Second, 2*time.Second)

		By("an observed DENY decision with the CIDR reason for the IP-literal target")
		expectObservedCIDRDecision(ctx, key, deniedTarget, scrutineerv1alpha1.PolicyDecisionDeny, envoy.ReasonDeniedCIDRs)

		By("Envoy having blocked it (403 in the access log)")
		Eventually(func(g Gomega) {
			logs := envoyAccessLog(ctx, envoyKey(ns, session.Name))
			g.Expect(logs).To(ContainSubstring(deniedTarget))
			g.Expect(logs).To(ContainSubstring("403"), "denied IP-literal egress should be 403-blocked by RBAC; log:\n%s", logs)
		}, 120*time.Second, 3*time.Second).Should(Succeed())

		requestCancellation(ctx, key)
		waitForTerminalPhase(ctx, key, scrutineerv1alpha1.PhaseCancelled)
	})

	It("records an observed dry-run for a would-be CIDR denial (audit)", func(ctx SpecContext) {
		ns := newTestNamespace("scrutineer-e2e-cidr-audit")
		createRuntimeProfileWithEnvoy(ctx, ns, "envoy-egress")
		createCIDRPolicy(ctx, ns, "cidr-audit", scrutineerv1alpha1.PolicyModeAuditOnly,
			scrutineerv1alpha1.PolicyRules{DeniedCIDRs: rfc1918})

		session := newAgentSession(ns, "cidr-audit",
			withRuntimeProfileRef("envoy-egress"),
			withPolicyRef("AgentPolicy", "cidr-audit"),
			withEnvoyIPConnectProbe(deniedTarget),
		)
		key := createAgentSession(ctx, session)
		expectJobForSession(ctx, ns, session)
		waitForPhase(ctx, key, []scrutineerv1alpha1.AgentSessionPhase{scrutineerv1alpha1.PhaseRunning}, 90*time.Second, 2*time.Second)

		By("an observed DRY-RUN decision (traffic allowed, would-be-denied recorded)")
		expectObservedCIDRDecision(ctx, key, deniedTarget, scrutineerv1alpha1.PolicyDecisionDryRun, envoy.ReasonDeniedCIDRs)

		requestCancellation(ctx, key)
		waitForTerminalPhase(ctx, key, scrutineerv1alpha1.PhaseCancelled)
	})

	// #126: a non-canonical numeric spelling a resolver would expand into a denied range
	// (leading-zero octet) must be refused fail-closed, so it can't evade the deny-list.
	It("refuses a non-canonical numeric authority that would evade a denied CIDR (enforced)", func(ctx SpecContext) {
		requireEgressEnforcingCNI(ctx)
		const evasion = "010.0.0.1:9999" // → 10.0.0.1, inside 10.0.0.0/8, via a leading zero
		ns := newTestNamespace("scrutineer-e2e-cidr-noncanon")
		createRuntimeProfileWithEnvoy(ctx, ns, "envoy-egress")
		createCIDRPolicy(ctx, ns, "cidr-deny-nc", scrutineerv1alpha1.PolicyModeEnforced,
			scrutineerv1alpha1.PolicyRules{DeniedCIDRs: rfc1918})

		session := newAgentSession(ns, "cidr-noncanon",
			withRuntimeProfileRef("envoy-egress"),
			withPolicyRef("AgentPolicy", "cidr-deny-nc"),
			withEnvoyIPConnectProbe(evasion),
		)
		key := createAgentSession(ctx, session)
		expectJobForSession(ctx, ns, session)
		waitForPhase(ctx, key, []scrutineerv1alpha1.AgentSessionPhase{scrutineerv1alpha1.PhaseRunning}, 90*time.Second, 2*time.Second)

		By("an observed DENY decision with the non-canonical reason for the evasion authority")
		expectObservedCIDRDecision(ctx, key, evasion, scrutineerv1alpha1.PolicyDecisionDeny, envoy.ReasonNonCanonicalIP)

		By("Envoy having refused it (403 in the access log)")
		Eventually(func(g Gomega) {
			logs := envoyAccessLog(ctx, envoyKey(ns, session.Name))
			g.Expect(logs).To(ContainSubstring(evasion))
			g.Expect(logs).To(ContainSubstring("403"), "non-canonical evasion should be 403-refused by RBAC; log:\n%s", logs)
		}, 120*time.Second, 3*time.Second).Should(Succeed())

		requestCancellation(ctx, key)
		waitForTerminalPhase(ctx, key, scrutineerv1alpha1.PhaseCancelled)
	})

	// The union semantic worth locking: an allow-list made only of CIDRs can never match
	// a hostname authority, so hostname dials are default-denied at the chokepoint.
	It("default-denies hostname dials under a CIDR-only allow-list (enforced)", func(ctx SpecContext) {
		requireEgressEnforcingCNI(ctx)
		const probeHost = "cidr-union.probe.scrutineer.invalid"
		ns := newTestNamespace("scrutineer-e2e-cidr-union")
		createRuntimeProfileWithEnvoy(ctx, ns, "envoy-egress")
		// TEST-NET-1: routable nowhere in the cluster, so nothing the probe dials is allowed.
		createCIDRPolicy(ctx, ns, "cidr-allow-only", scrutineerv1alpha1.PolicyModeEnforced,
			scrutineerv1alpha1.PolicyRules{AllowedCIDRs: []string{"192.0.2.0/24"}})

		session := newAgentSession(ns, "cidr-union",
			withRuntimeProfileRef("envoy-egress"),
			withPolicyRef("AgentPolicy", "cidr-allow-only"),
			withEnvoyEgressProbe(probeHost),
		)
		key := createAgentSession(ctx, session)
		expectJobForSession(ctx, ns, session)
		waitForPhase(ctx, key, []scrutineerv1alpha1.AgentSessionPhase{scrutineerv1alpha1.PhaseRunning}, 90*time.Second, 2*time.Second)

		By("an observed DENY decision naming the CIDR allow-list for the hostname dial")
		expectObservedCIDRDecision(ctx, key, probeHost, scrutineerv1alpha1.PolicyDecisionDeny, envoy.ReasonNotInAllowedCIDRs)

		By("Envoy having blocked it (403 in the access log)")
		Eventually(func(g Gomega) {
			logs := envoyAccessLog(ctx, envoyKey(ns, session.Name))
			g.Expect(logs).To(ContainSubstring(probeHost))
			g.Expect(logs).To(ContainSubstring("403"), "hostname dial under a CIDR-only allow-list should be 403-blocked; log:\n%s", logs)
		}, 120*time.Second, 3*time.Second).Should(Succeed())

		requestCancellation(ctx, key)
		waitForTerminalPhase(ctx, key, scrutineerv1alpha1.PhaseCancelled)
	})
})

// createCIDRPolicy creates an AgentPolicy carrying rules in the given mode.
func createCIDRPolicy(ctx context.Context, ns, name string, mode scrutineerv1alpha1.PolicyMode, rules scrutineerv1alpha1.PolicyRules) {
	GinkgoHelper()
	Expect(k8sClient.Create(ctx, &scrutineerv1alpha1.AgentPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: scrutineerv1alpha1.AgentPolicySpec{
			Mode:        mode,
			PolicyRules: rules,
		},
	})).To(Succeed())
}

// withEnvoyIPConnectProbe makes the busybox agent dial a fixed IPv4-literal target
// (host:port) through its per-session Envoy. Both paths carry the literal as the
// :authority the CIDR RBAC matches: a proxied HTTP GET (via the injected proxy env) and
// a raw CONNECT (the transport reaches Envoy by its ClusterIP, derived from the proxy env
// — direct DNS is denied under the routing lock). RBAC blocks the authority before any
// connection, so the target need not be reachable; the assertion is on the access log and
// the observed decision. The target is a fixed literal, never the proxy's own address.
func withEnvoyIPConnectProbe(target string) agentSessionOption {
	return func(s *scrutineerv1alpha1.AgentSession) {
		script := fmt.Sprintf(`sleep 12
ENVOY_IP=$(printf '%%s' "${http_proxy:-$HTTP_PROXY}" | sed 's|^http://||; s|:.*$||')
for i in $(seq 1 40); do
  wget -q -O /dev/null "http://%[1]s/" 2>/dev/null || true
  printf 'CONNECT %[1]s HTTP/1.1\r\nHost: %[1]s\r\n\r\n' | nc -w 3 "$ENVOY_IP" 15001 2>/dev/null || true
  sleep 2
done
sleep 120`, target)
		s.Spec.Runtime.Command = []string{"sh", "-c", script}
	}
}

// expectObservedCIDRDecision waits for a runtime network decision for target with the
// given action AND reason, stamped observed (produced by the out-of-pod egress proxy).
func expectObservedCIDRDecision(ctx context.Context, key client.ObjectKey, target string, action scrutineerv1alpha1.PolicyDecisionAction, reason string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		got := getSession(ctx, key)
		var match *scrutineerv1alpha1.PolicyDecision
		for i := range got.Status.PolicyDecisions {
			d := &got.Status.PolicyDecisions[i]
			if d.Type != "network" || d.Actor != envoy.AccessLogActor {
				continue
			}
			if d.Target == target || d.Target == target+":443" {
				match = d
			}
		}
		g.Expect(match).NotTo(BeNil(), "no egress-proxy decision for %q; decisions=%+v", target, got.Status.PolicyDecisions)
		g.Expect(match.Action).To(Equal(action), "decision=%+v", match)
		g.Expect(match.Reason).To(Equal(reason), "decision=%+v", match)
		g.Expect(match.AssuranceLevel).To(Equal(scrutineerv1alpha1.EvidenceObserved),
			"CIDR decisions from the proxy must be observed: %+v", match)
	}, 150*time.Second, 3*time.Second).Should(Succeed())
}
