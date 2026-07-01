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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/enforcement/envoy"
)

// Part of the generic networking suite (Label "networking"): runs against any CNI cluster
// via `make test-e2e-net` (kindnet, Calico, …). Excluded from the standard `make test-e2e`.
var _ = Describe("Live per-session Envoy egress proxy", Label(labelNetworking), func() {
	BeforeEach(func(ctx SpecContext) {
		requireEgressEnforcingCNI(ctx)
		if !clusterImageRunnable(ctx, envoy.DefaultEnvoyImage) {
			Skip("envoy image not available in cluster — run: make kind-load-envoy")
		}
	})

	It("provisions a per-session Envoy pod, routes agent egress through it (incl. CONNECT), and tears it down", func(ctx SpecContext) {
		ns := newTestNamespace("scrutineer-e2e-egress")
		const profileName = "envoy-egress"
		createRuntimeProfileWithEnvoy(ctx, ns, profileName)

		// Non-resolvable on purpose: the proof is Envoy's access log, not upstream success.
		const probeHost = "probe.scrutineer.invalid"
		session := newAgentSession(ns, "egress-live",
			withRuntimeProfileRef(profileName),
			withEnvoyEgressProbe(probeHost),
		)
		key := createAgentSession(ctx, session)

		expectJobForSession(ctx, ns, session)
		waitForPhase(ctx, key, []scrutineerv1alpha1.AgentSessionPhase{scrutineerv1alpha1.PhaseRunning}, 90*time.Second, 2*time.Second)

		egressName := envoy.ResourceName(session.Name)
		egressKey := types.NamespacedName{Namespace: ns, Name: egressName}

		By("the per-session Envoy pod becoming Ready")
		waitForEnvoyPodReady(ctx, egressKey)

		By("the Service, ServiceAccount, and ConfigMap existing")
		Expect(k8sClient.Get(ctx, egressKey, &corev1.Service{})).To(Succeed())
		Expect(k8sClient.Get(ctx, egressKey, &corev1.ServiceAccount{})).To(Succeed())
		Expect(k8sClient.Get(ctx, egressKey, &corev1.ConfigMap{})).To(Succeed())

		By("the agent's egress traversing Envoy — proven by Envoy's access log")
		Eventually(func(g Gomega) {
			logs := envoyAccessLog(ctx, egressKey)
			// The agent's env-routed HTTP request reached Envoy (host appears as authority).
			g.Expect(logs).To(ContainSubstring(probeHost),
				"agent egress did not traverse Envoy; access log:\n%s", logs)
			// Envoy handled a real CONNECT for the HTTPS port.
			g.Expect(logs).To(ContainSubstring("CONNECT "+probeHost+":443"),
				"Envoy did not log a CONNECT tunnel; access log:\n%s", logs)
		}, 120*time.Second, 3*time.Second).Should(Succeed())

		By("tearing the egress proxy down when the session terminates")
		requestCancellation(ctx, key)
		waitForTerminalPhase(ctx, key, scrutineerv1alpha1.PhaseCancelled)
		Eventually(func(g Gomega) {
			g.Expect(egressObjectGone(ctx, egressKey, &corev1.Pod{})).To(BeTrue(), "envoy pod not torn down")
			g.Expect(egressObjectGone(ctx, egressKey, &corev1.Service{})).To(BeTrue(), "envoy service not torn down")
			g.Expect(egressObjectGone(ctx, egressKey, &corev1.ServiceAccount{})).To(BeTrue(), "envoy SA not torn down")
			g.Expect(egressObjectGone(ctx, egressKey, &corev1.ConfigMap{})).To(BeTrue(), "envoy configmap not torn down")
		}, 60*time.Second, 2*time.Second).Should(Succeed())
	})
})

// waitForEnvoyPodReady waits until the per-session Envoy pod is Running with a Ready container.
func waitForEnvoyPodReady(ctx context.Context, key types.NamespacedName) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		var pod corev1.Pod
		g.Expect(k8sClient.Get(ctx, key, &pod)).To(Succeed())
		g.Expect(pod.Status.Phase).To(Equal(corev1.PodRunning),
			"envoy pod phase=%s message=%s", pod.Status.Phase, pod.Status.Message)
		ready := false
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.Name == "envoy" && cs.Ready {
				ready = true
			}
		}
		g.Expect(ready).To(BeTrue(), "envoy container not ready: %+v", pod.Status.ContainerStatuses)
	}, 120*time.Second, 3*time.Second).Should(Succeed())
}

// envoyAccessLog returns the stdout (access log) of the per-session Envoy container.
func envoyAccessLog(ctx context.Context, key types.NamespacedName) string {
	GinkgoHelper()
	cs, err := kubernetes.NewForConfig(cfg)
	Expect(err).NotTo(HaveOccurred())
	raw, err := cs.CoreV1().Pods(key.Namespace).
		GetLogs(key.Name, &corev1.PodLogOptions{Container: "envoy"}).
		DoRaw(ctx)
	Expect(err).NotTo(HaveOccurred())
	return string(raw)
}

// egressObjectGone reports whether an egress object is deleted (NotFound) or already
// terminating (deletionTimestamp set) — the latter matters for Pods with a grace period.
func egressObjectGone(ctx context.Context, key types.NamespacedName, obj client.Object) bool {
	err := k8sClient.Get(ctx, key, obj)
	if apierrors.IsNotFound(err) {
		return true
	}
	return err == nil && obj.GetDeletionTimestamp() != nil
}
