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
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/enforcement/envoy"
)

// Slice D (#63): an agent that legitimately needs the Kubernetes API opts in via the
// RuntimeProfile automount toggle. This proves the design assumption that apiserver access
// still flows *through* the Envoy chokepoint under the routing lock — the token is mounted
// AND apiserver egress transits Envoy (nothing bypasses it). Runs on any enforcing CNI.
var _ = Describe("Live SA-token automount opt-in under the egress lock", Label(labelNetworking), func() {
	BeforeEach(func(ctx SpecContext) {
		requireEgressEnforcingCNI(ctx)
		if !clusterImageRunnable(ctx, envoy.DefaultEnvoyImage) {
			Skip("envoy image not available in cluster — run: make kind-load-envoy")
		}
	})

	It("mounts the SA token and routes apiserver access through Envoy when opted in", func(ctx SpecContext) {
		ns := newTestNamespace("scrutineer-e2e-automount")
		createRuntimeProfileWithEnvoyAutomount(ctx, ns, "envoy-automount")

		session := newAgentSession(ns, "automount-on",
			withRuntimeProfileRef("envoy-automount"),
			withApiserverViaEnvoyProbe(),
		)
		key := createAgentSession(ctx, session)
		expectJobForSession(ctx, ns, session)
		waitForPhase(ctx, key, []scrutineerv1alpha1.AgentSessionPhase{scrutineerv1alpha1.PhaseRunning}, 90*time.Second, 2*time.Second)

		egressKey := types.NamespacedName{Namespace: ns, Name: envoy.ResourceName(session.Name)}
		waitForEnvoyPodReady(ctx, egressKey)

		By("the agent pod having the SA token automounted")
		got := getSession(ctx, key)
		Expect(got.Status.PodName).NotTo(BeEmpty())
		var pod corev1.Pod
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: got.Status.PodName}, &pod)).To(Succeed())
		Expect(pod.Spec.AutomountServiceAccountToken).NotTo(BeNil())
		Expect(*pod.Spec.AutomountServiceAccountToken).To(BeTrue())

		By("the token actually being readable inside the agent container")
		Eventually(func(g Gomega) {
			g.Expect(agentContainerLog(ctx, ns, got.Status.PodName)).To(ContainSubstring("TOKEN=present"))
		}, 60*time.Second, 3*time.Second).Should(Succeed())

		By("apiserver access transiting Envoy (authority in the access log)")
		Eventually(func(g Gomega) {
			logs := envoyAccessLog(ctx, egressKey)
			g.Expect(logs).To(ContainSubstring("kubernetes.default.svc:443"),
				"agent apiserver egress did not traverse Envoy; access log:\n%s", logs)
		}, 120*time.Second, 3*time.Second).Should(Succeed())

		requestCancellation(ctx, key)
		waitForTerminalPhase(ctx, key, scrutineerv1alpha1.PhaseCancelled)
	})

	It("does not mount the SA token without the opt-in", func(ctx SpecContext) {
		ns := newTestNamespace("scrutineer-e2e-automount-off")
		createRuntimeProfileWithEnvoy(ctx, ns, "envoy-plain")

		session := newAgentSession(ns, "automount-off",
			withRuntimeProfileRef("envoy-plain"),
			withApiserverViaEnvoyProbe(),
		)
		key := createAgentSession(ctx, session)
		expectJobForSession(ctx, ns, session)
		waitForPhase(ctx, key, []scrutineerv1alpha1.AgentSessionPhase{scrutineerv1alpha1.PhaseRunning}, 90*time.Second, 2*time.Second)

		got := getSession(ctx, key)
		Expect(got.Status.PodName).NotTo(BeEmpty())
		var pod corev1.Pod
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: got.Status.PodName}, &pod)).To(Succeed())
		Expect(pod.Spec.AutomountServiceAccountToken).NotTo(BeNil())
		Expect(*pod.Spec.AutomountServiceAccountToken).To(BeFalse(), "default must keep the token off")

		Eventually(func(g Gomega) {
			g.Expect(agentContainerLog(ctx, ns, got.Status.PodName)).To(ContainSubstring("TOKEN=absent"))
		}, 60*time.Second, 3*time.Second).Should(Succeed())

		requestCancellation(ctx, key)
		waitForTerminalPhase(ctx, key, scrutineerv1alpha1.PhaseCancelled)
	})
})

// agentContainerLog returns the agent container's stdout for a session pod.
func agentContainerLog(ctx context.Context, namespace, podName string) string {
	GinkgoHelper()
	cs, err := kubernetes.NewForConfig(cfg)
	Expect(err).NotTo(HaveOccurred())
	raw, err := cs.CoreV1().Pods(namespace).
		GetLogs(podName, &corev1.PodLogOptions{Container: "agent"}).
		DoRaw(ctx)
	if err != nil {
		return ""
	}
	return string(raw)
}
