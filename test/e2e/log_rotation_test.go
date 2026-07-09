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
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/enforcement/envoy"
)

// Access-log rotation (#98) is only provable end to end against the running artifact:
// the distroless Envoy must honor POST /reopen_logs from the egress-reporter container
// (loopback admin, shared pod netns) and the reporter must be able to rename/delete in
// the shared emptyDir under the hardened pod security context. The suite wires a small
// rotation threshold (48KiB, suite_test.go) through the controller env plumbing; this
// spec drives enough proxied traffic to cross it and asserts a completed rotation cycle
// via the reporter's own metrics, then that evidence keeps flowing afterwards.
// Design: docs/design/access-log-rotation.md.
var _ = Describe("Access-log rotation", Label(labelNetworking), func() {
	BeforeEach(func(ctx SpecContext) {
		requireScrutineerE2EImage(ctx)
		if !clusterImageRunnable(ctx, envoy.DefaultEnvoyImage) {
			Skip("envoy image not available in cluster — run: make kind-load-envoy")
		}
		if !clusterImageRunnable(ctx, envoy.DefaultEgressReporterImage()) {
			Skip("egress-reporter image not available in cluster — run: make kind-load-egress-reporter")
		}
		deployInClusterReporter(ctx)
	})

	It("rotates the ingested access log via Envoy /reopen_logs and keeps evidence flowing", func(ctx SpecContext) {
		ns := newTestNamespace("scrutineer-e2e-rotation")
		const profileName = "envoy-egress"
		createRuntimeProfileWithEnvoy(ctx, ns, profileName)

		// Burst enough proxied requests to push the access log past the suite's 48KiB
		// rotation threshold (~250 lines), then keep a slow trickle so post-rotation
		// ingest is observable. Non-resolvable host on purpose: the log line (and the
		// evidence) records the attempt regardless of upstream success.
		const probeHost = "rotate.scrutineer.invalid"
		burst := fmt.Sprintf(`sleep 12
for i in $(seq 1 400); do wget -q -O /dev/null -T 2 "http://%[1]s/" 2>/dev/null || true; done
for i in $(seq 1 240); do wget -q -O /dev/null -T 2 "http://%[1]s/" 2>/dev/null || true; sleep 1; done
sleep 60`, probeHost)
		session := newAgentSession(ns, "rotation-live", withRuntimeProfileRef(profileName))
		session.Spec.Runtime.Command = []string{"sh", "-c", burst}
		key := createAgentSession(ctx, session)

		expectJobForSession(ctx, ns, session)
		waitForPhase(ctx, key, []scrutineerv1alpha1.AgentSessionPhase{scrutineerv1alpha1.PhaseRunning}, 90*time.Second, 2*time.Second)

		egressKey := types.NamespacedName{Namespace: ns, Name: envoy.ResourceName(session.Name)}
		waitForEnvoyPodReady(ctx, egressKey)
		var proxyPod corev1.Pod
		Expect(k8sClient.Get(ctx, egressKey, &proxyPod)).To(Succeed())
		Expect(proxyPod.Status.PodIP).NotTo(BeEmpty(), "proxy pod has no IP yet")

		By("watching the reporter's rotation and decision counters from a probe pod")
		scrapeCmd := fmt.Sprintf(`while true; do
wget -qO- -T 3 http://%s:%d/metrics 2>/dev/null | awk '
  /^scrutineer_egress_reporter_log_rotations_total/ {rot=$2}
  /^scrutineer_egress_reporter_decisions_total/ {dec+=$2}
  END {printf "ROT=%%d DEC=%%d\n", rot, dec}'
sleep 2
done`, proxyPod.Status.PodIP, envoy.ReporterMetricsPort)
		scraper := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "rotation-scraper", Namespace: ns},
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

		readCounters := func(g Gomega) (rot, dec int) {
			tail := podLogTail(ctx, ns, "rotation-scraper", "probe", 3)
			line := ""
			for _, l := range strings.Split(strings.TrimSpace(tail), "\n") {
				if strings.HasPrefix(l, "ROT=") {
					line = l
				}
			}
			g.Expect(line).NotTo(BeEmpty(), "no scrape output yet: %q", tail)
			n, err := fmt.Sscanf(line, "ROT=%d DEC=%d", &rot, &dec)
			g.Expect(err).NotTo(HaveOccurred(), "unparseable scrape output: %q", line)
			g.Expect(n).To(Equal(2))
			return rot, dec
		}

		By("a rotation cycle completing")
		var decisionsAtRotation int
		Eventually(func(g Gomega) {
			rot, dec := readCounters(g)
			g.Expect(rot).To(BeNumerically(">=", 1),
				"no completed rotation cycle yet (decisions so far: %d)", dec)
			decisionsAtRotation = dec
		}, 4*time.Minute, 3*time.Second).Should(Succeed())

		By("evidence still flowing after the rotation")
		Eventually(func(g Gomega) {
			_, dec := readCounters(g)
			g.Expect(dec).To(BeNumerically(">", decisionsAtRotation),
				"no new decisions ingested after rotation — pipeline dead")
		}, 2*time.Minute, 3*time.Second).Should(Succeed())
	})
})
