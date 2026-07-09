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
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/enforcement/envoy"
)

// Metrics exposure (#55) is only provable against the running artifact: the Envoy stats
// listener is bootstrap YAML and the reporter's /metrics is a live HTTP server. This spec
// runs a real session (whose egress probe generates traffic → non-trivial counters) and
// scrapes both endpoints on the egress-proxy pod IP from a separate probe pod. Part of
// the networking suite; the routing lock does not apply to the scraper (it selects only
// the agent pod) and the proxy pod has no ingress policy — scraping stays possible.
var _ = Describe("Egress-path metrics", Label(labelNetworking), func() {
	BeforeEach(func(ctx SpecContext) {
		requireEgressEnforcingCNI(ctx)
		if !clusterImageRunnable(ctx, envoy.DefaultEnvoyImage) {
			Skip("envoy image not available in cluster — run: make kind-load-envoy")
		}
		if !clusterImageRunnable(ctx, envoy.DefaultEgressReporterImage()) {
			Skip("egress-reporter image not available in cluster — run: make kind-load-egress-reporter")
		}
	})

	It("serves Envoy /stats/prometheus and egress-reporter /metrics from the proxy pod", func(ctx SpecContext) {
		ns := newTestNamespace("scrutineer-e2e-metrics")
		const profileName = "envoy-egress"
		createRuntimeProfileWithEnvoy(ctx, ns, profileName)

		const probeHost = "probe.scrutineer.invalid"
		session := newAgentSession(ns, "metrics-live",
			withRuntimeProfileRef(profileName),
			withEnvoyEgressProbe(probeHost),
		)
		key := createAgentSession(ctx, session)

		expectJobForSession(ctx, ns, session)
		waitForPhase(ctx, key, []scrutineerv1alpha1.AgentSessionPhase{scrutineerv1alpha1.PhaseRunning}, 90*time.Second, 2*time.Second)

		egressKey := types.NamespacedName{Namespace: ns, Name: envoy.ResourceName(session.Name)}
		waitForEnvoyPodReady(ctx, egressKey)

		var proxyPod corev1.Pod
		Expect(k8sClient.Get(ctx, egressKey, &proxyPod)).To(Succeed())
		Expect(proxyPod.Status.PodIP).NotTo(BeEmpty(), "proxy pod has no IP yet")

		By("scraping both endpoints from a separate probe pod")
		scrapeCmd := fmt.Sprintf(`while true; do
s=FAIL; wget -qO- -T 3 http://%[1]s:%[2]d/stats/prometheus 2>/dev/null | grep -q "envoy_" && s=OK
r=FAIL; wget -qO- -T 3 http://%[1]s:%[3]d/metrics 2>/dev/null | grep -q "scrutineer_egress_reporter_" && r=OK
echo "STATS=$s RMETRICS=$r"
sleep 2
done`, proxyPod.Status.PodIP, envoy.StatsPort, envoy.ReporterMetricsPort)
		scraper := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "metrics-scraper", Namespace: ns},
			Spec: corev1.PodSpec{
				RestartPolicy: corev1.RestartPolicyNever,
				Containers: []corev1.Container{{
					Name:    "probe",
					Image:   "busybox:latest",
					Command: []string{"sh", "-c", scrapeCmd},
				}},
			},
		}
		Expect(k8sClient.Create(ctx, scraper)).To(Succeed())

		Eventually(func(g Gomega) {
			g.Expect(podLogTail(ctx, ns, "metrics-scraper", "probe", 2)).To(
				ContainSubstring("STATS=OK RMETRICS=OK"),
				"both the Envoy stats listener and the reporter /metrics must serve")
		}, 90*time.Second, 2*time.Second).Should(Succeed())
	})
})
