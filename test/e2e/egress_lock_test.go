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

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/enforcement/envoy"
)

// This spec proves the Slice B routing lock actually blocks bypass — the #8 guarantee that
// an agent cannot egress except through Envoy. It is CNI-generic: it asserts enforcement
// *behavior* (direct egress/DNS dropped, Envoy reachable), so the same test validates any
// NetworkPolicy-enforcing CNI. Part of the networking suite (`make test-e2e-net`), run
// across kindnet and Calico; skipped on a CNI that does not enforce egress policy.
var _ = Describe("Live routing lock enforcement", Label(labelNetworking), func() {
	BeforeEach(func(ctx SpecContext) {
		requireEgressEnforcingCNI(ctx)
		if !clusterImageRunnable(ctx, envoy.DefaultEnvoyImage) {
			Skip("envoy image not available in cluster — run: make kind-load-envoy")
		}
	})

	It("routes agent egress through Envoy and drops direct egress + DNS", func(ctx SpecContext) {
		ns := newTestNamespace("scrutineer-e2e-lock")
		createRuntimeProfileWithEnvoy(ctx, ns, "envoy-egress")

		// A non-Envoy in-cluster POD the agent must NOT reach directly. A real pod (not the
		// apiserver, which is host-network and exempt from pod egress policy on some CNIs)
		// keeps this negative CNI-generic.
		targetIP := kubeDNSPodIP(ctx)
		Expect(targetIP).NotTo(BeEmpty(), "need a kube-dns pod IP as a non-Envoy egress target")

		const probeHost = "probe.scrutineer.invalid"
		session := newAgentSession(ns, "lock-live",
			withRuntimeProfileRef("envoy-egress"),
			withNetpolEgressProbe(probeHost),
		)
		session.Spec.Runtime.Env = append(session.Spec.Runtime.Env,
			corev1.EnvVar{Name: "PROBE_TARGET_IP", Value: targetIP})
		key := createAgentSession(ctx, session)

		expectJobForSession(ctx, ns, session)
		waitForPhase(ctx, key, []scrutineerv1alpha1.AgentSessionPhase{scrutineerv1alpha1.PhaseRunning}, 90*time.Second, 2*time.Second)

		egressKey := types.NamespacedName{Namespace: ns, Name: envoy.ResourceName(session.Name)}
		waitForEnvoyPodReady(ctx, egressKey)

		By("the mandatory routing-lock NetworkPolicy existing on the agent pod")
		var np networkingv1.NetworkPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: netpolNameForSession(session)}, &np)).To(Succeed())
		Expect(np.Spec.PolicyTypes).To(ContainElement(networkingv1.PolicyTypeEgress))

		By("agent egress traversing Envoy under the lock (Envoy access log)")
		Eventually(func(g Gomega) {
			g.Expect(envoyAccessLog(ctx, egressKey)).To(ContainSubstring(probeHost),
				"agent egress did not reach Envoy under the routing lock")
		}, 120*time.Second, 3*time.Second).Should(Succeed())

		By("direct egress and DNS being dropped, while Envoy stays reachable (agent probe markers)")
		Eventually(func(g Gomega) {
			logs := agentPodLog(ctx, key)
			// Positive control: the lock ALLOWS the agent to reach its Envoy (and nc works),
			// so a BLOCKED verdict below is a real deny, not a broken probe.
			g.Expect(logs).To(ContainSubstring("PROBE_ENVOY_TCP=OK"),
				"agent could not reach its Envoy — probe/connectivity broken, negatives are meaningless")
			// Negatives: direct DNS and a direct TCP connect to a non-Envoy IP must be dropped.
			g.Expect(logs).To(ContainSubstring("PROBE_DNS=BLOCKED"))
			g.Expect(logs).NotTo(ContainSubstring("PROBE_DNS=OK"))
			g.Expect(logs).To(ContainSubstring("PROBE_DIRECT=BLOCKED"))
			g.Expect(logs).NotTo(ContainSubstring("PROBE_DIRECT=OK"))
		}, 120*time.Second, 3*time.Second).Should(Succeed())

		requestCancellation(ctx, key)
		waitForTerminalPhase(ctx, key, scrutineerv1alpha1.PhaseCancelled)
	})
})

// agentPodLog returns the agent container's stdout for the session's current pod, or "" if
// the pod/logs are not available yet (so Eventually retries).
func agentPodLog(ctx context.Context, key client.ObjectKey) string {
	got := getSession(ctx, key)
	if got.Status.PodName == "" {
		return ""
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return ""
	}
	raw, err := cs.CoreV1().Pods(key.Namespace).
		GetLogs(got.Status.PodName, &corev1.PodLogOptions{Container: "agent"}).
		DoRaw(ctx)
	if err != nil {
		return ""
	}
	return string(raw)
}
