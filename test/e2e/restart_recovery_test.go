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

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/controller/agentsession"
	"github.com/grantbarry29/scrutineer/internal/enforcement/envoy"
)

// Controller restart/downtime recovery (#150): in production the controller WILL
// restart (upgrades, evictions, crashes) with sessions in flight, and everything that
// happened while it was down must be caught up on cold start — completed Jobs observed,
// sessions created during downtime admitted, tampered enforcement objects restored
// (the cold-start variant of #147's watch-driven repair), and evidence that kept
// flowing through the controller-independent reporter path merged without loss or
// duplication.
//
// Every spec here is Serial: it takes the shared in-process manager down mid-spec,
// which must never interleave with other specs (and future-proofs parallelization,
// #156). Each Describe re-arms the manager via DeferCleanup so even a failing
// assertion cannot strand the rest of the suite against a dead controller.

// countMergeDecisions returns how many merge-phase policy decisions are in status.
func countMergeDecisions(s *scrutineerv1alpha1.AgentSession) int {
	n := 0
	for _, d := range s.Status.PolicyDecisions {
		if d.Phase == scrutineerv1alpha1.PolicyDecisionPhaseMerge {
			n++
		}
	}
	return n
}

// expectNoDuplicateDecisions asserts every policy decision in status is unique across
// its full content — a controller that re-ingested existing evidence on restart would
// append content-identical entries and fail here.
func expectNoDuplicateDecisions(s *scrutineerv1alpha1.AgentSession) {
	GinkgoHelper()
	seen := map[string]bool{}
	for _, d := range s.Status.PolicyDecisions {
		k := fmt.Sprintf("%s|%s|%s|%s|%s|%s|%s",
			d.Phase, d.Type, d.Target, d.Action, d.Reason, d.Actor, d.Time.UTC().Format(time.RFC3339Nano))
		Expect(seen[k]).To(BeFalse(), "duplicate policy decision after restart: %s", k)
		seen[k] = true
	}
}

var _ = Describe("Controller restart recovery", Serial, func() {
	BeforeEach(func() {
		DeferCleanup(func() {
			if !controllerManagerRunning() {
				restartControllerManager()
			}
		})
	})

	It("observes on restart a Job that completed while the controller was down", func(ctx SpecContext) {
		ns := newTestNamespace("scrutineer-e2e-restart")
		session := newAgentSession(ns, "restart-complete",
			withCommand("sh", "-c", "sleep 6; echo done"))
		key := createAgentSession(ctx, session)

		expectJobForSession(ctx, ns, session)
		waitForPhase(ctx, key, []scrutineerv1alpha1.AgentSessionPhase{scrutineerv1alpha1.PhaseRunning},
			90*time.Second, 2*time.Second)
		pre := getSession(ctx, key)
		preMerge := countMergeDecisions(&pre)

		stopControllerManager()

		By("the Job completing while the controller is down")
		jobKey := client.ObjectKey{Namespace: ns, Name: jobNameForSession(session)}
		Eventually(func(g Gomega) {
			var job batchv1.Job
			g.Expect(k8sClient.Get(ctx, jobKey, &job)).To(Succeed())
			g.Expect(job.Status.Succeeded).To(BeNumerically(">=", 1),
				"job did not complete during the downtime window")
		}, 90*time.Second, 2*time.Second).Should(Succeed())
		// Nothing observed it: the session must still read Running.
		Expect(getSession(ctx, key).Status.Phase).To(Equal(scrutineerv1alpha1.PhaseRunning),
			"session phase moved while the controller was down — something else is reconciling")

		restartControllerManager()

		By("the restarted controller catching up to Succeeded with correct status")
		waitForTerminalPhase(ctx, key, scrutineerv1alpha1.PhaseSucceeded)
		got := getSession(ctx, key)
		expectCondition(&got, agentsession.ConditionCompleted, metav1.ConditionTrue, "JobSucceeded")
		Expect(got.Status.CompletionTime).NotTo(BeNil(), "completionTime missing after catch-up")
		Expect(got.Status.Result).NotTo(BeNil())
		Expect(got.Status.Result.Outcome).To(Equal("completed"))

		By("no merge decision duplicated or lost across the restart")
		Expect(countMergeDecisions(&got)).To(Equal(preMerge),
			"merge-decision count changed across restart: before=%d after=%d", preMerge, countMergeDecisions(&got))
		expectNoDuplicateDecisions(&got)
	})

	It("admits and runs to completion a session created while the controller was down", func(ctx SpecContext) {
		ns := newTestNamespace("scrutineer-e2e-restart")

		stopControllerManager()

		session := newAgentSession(ns, "restart-created",
			withCommand("sh", "-c", "echo recovered"))
		key := createAgentSession(ctx, session)

		By("nothing acting on the session while the controller is down")
		expectNoJobForSession(ctx, ns, session)

		restartControllerManager()

		By("the restarted controller admitting and running the session")
		expectJobForSession(ctx, ns, session)
		waitForTerminalPhase(ctx, key, scrutineerv1alpha1.PhaseSucceeded)
		got := getSession(ctx, key)
		expectCondition(&got, agentsession.ConditionValidated, metav1.ConditionTrue, "SpecValid")
		expectCondition(&got, agentsession.ConditionCompleted, metav1.ConditionTrue, "JobSucceeded")
		Expect(got.Status.Result).NotTo(BeNil())
		Expect(got.Status.Result.Outcome).To(Equal("completed"))
	})
})

var _ = Describe("Controller restart recovery under enforcement", Serial, Label(labelNetworking), func() {
	BeforeEach(func(ctx SpecContext) {
		requireEgressEnforcingCNI(ctx)
		if !clusterImageRunnable(ctx, envoy.DefaultEnvoyImage) {
			Skip("envoy image not available in cluster — run: make kind-load-envoy")
		}
		DeferCleanup(func() {
			if !controllerManagerRunning() {
				restartControllerManager()
			}
		})
	})

	It("restores on cold start a routing lock deleted during downtime, fail-closed with bounded exposure", func(ctx SpecContext) {
		ns := newTestNamespace("scrutineer-e2e-restart-lock")
		createRuntimeProfileWithEnvoy(ctx, ns, "envoy-egress")

		targetIP := kubeDNSPodIP(ctx)
		Expect(targetIP).NotTo(BeEmpty(), "need a kube-dns pod IP as a non-Envoy egress target")

		const probeHost = "restart-lock.scrutineer.invalid"
		session := newAgentSession(ns, "restart-lock",
			withRuntimeProfileRef("envoy-egress"),
			withTamperEgressProbe(probeHost),
		)
		session.Spec.Runtime.Env = append(session.Spec.Runtime.Env,
			corev1.EnvVar{Name: "PROBE_TARGET_IP", Value: targetIP})
		key := createAgentSession(ctx, session)

		expectJobForSession(ctx, ns, session)
		waitForPhase(ctx, key, []scrutineerv1alpha1.AgentSessionPhase{scrutineerv1alpha1.PhaseRunning},
			90*time.Second, 2*time.Second)
		waitForEnvoyPodReady(ctx, types.NamespacedName{Namespace: ns, Name: envoy.ResourceName(session.Name)})

		By("baseline: the lock enforcing and the probe healthy")
		expectLockBaselineMarkers(ctx, key)

		lockKey := client.ObjectKey{Namespace: ns, Name: netpolNameForSession(session)}
		var lock networkingv1.NetworkPolicy
		Expect(k8sClient.Get(ctx, lockKey, &lock)).To(Succeed())
		renderedEgress := lock.Spec.DeepCopy().Egress
		lockUID := lock.UID

		stopControllerManager()

		By("deleting the routing lock while the controller is down")
		Expect(k8sClient.Delete(ctx, &lock)).To(Succeed())
		deleted := time.Now()
		// No watch-driven repair can land now — a repair here would mean some other
		// controller is reconciling the cluster and the spec proves nothing.
		Consistently(func() bool {
			return apierrors.IsNotFound(k8sClient.Get(ctx, lockKey, &networkingv1.NetworkPolicy{}))
		}, 3*time.Second, 500*time.Millisecond).Should(BeTrue(),
			"lock reappeared while the controller was down — another reconciler is active")

		restartControllerManager()

		By("cold-start resync recreating the lock with the rendered spec")
		Eventually(func(g Gomega) {
			var np networkingv1.NetworkPolicy
			g.Expect(k8sClient.Get(ctx, lockKey, &np)).To(Succeed())
			g.Expect(np.UID).NotTo(Equal(lockUID), "old lock still present, not a recreation")
			g.Expect(np.Spec.Egress).To(Equal(renderedEgress))
		}, 60*time.Second, 500*time.Millisecond).Should(Succeed())
		GinkgoWriter.Printf("routing lock restored %v after deletion (downtime + cold start)\n",
			time.Since(deleted).Round(100*time.Millisecond))

		By("direct egress staying blocked once the restoration lands")
		expectProbesBlockedAfterRepair(ctx, key)

		By("whole-run exposure staying bounded (#147 accounting: window < two probe cycles)")
		finalLog := agentPodLog(ctx, key)
		okDNS := strings.Count(finalLog, "PROBE_DNS=OK")
		okDirect := strings.Count(finalLog, "PROBE_DIRECT=OK")
		GinkgoWriter.Printf("exposure markers across the downtime window: PROBE_DNS=OK ×%d, PROBE_DIRECT=OK ×%d\n", okDNS, okDirect)
		Expect(okDNS).To(BeNumerically("<=", 2),
			"downtime repair window exceeded two probe cycles — DNS egress was open too long:\n%s", finalLog)
		Expect(okDirect).To(BeNumerically("<=", 2),
			"downtime repair window exceeded two probe cycles — direct egress was open too long:\n%s", finalLog)

		requestCancellation(ctx, key)
		waitForTerminalPhase(ctx, key, scrutineerv1alpha1.PhaseCancelled)
	})

	It("merges evidence that kept flowing during downtime without loss or duplication", func(ctx SpecContext) {
		requireScrutineerE2EImage(ctx)
		if !clusterImageRunnable(ctx, envoy.DefaultEgressReporterImage()) {
			Skip("egress-reporter image not available in cluster — run: make kind-load-egress-reporter")
		}
		deployInClusterReporter(ctx)

		ns := newTestNamespace("scrutineer-e2e-restart-ev")
		createRuntimeProfileWithEnvoy(ctx, ns, "envoy-egress")

		targetIP := kubeDNSPodIP(ctx)
		Expect(targetIP).NotTo(BeEmpty(), "need a kube-dns pod IP as a non-Envoy egress target")

		// Slow-cadence probe (as in #147's evidence spec): total decision volume stays
		// well under MaxPolicyDecisions, so nothing here is truncation noise.
		const probeHost = "restart-evidence.scrutineer.invalid"
		session := newAgentSession(ns, "restart-evidence",
			withRuntimeProfileRef("envoy-egress"),
			withNetpolEgressProbe(probeHost),
		)
		session.Spec.Runtime.Env = append(session.Spec.Runtime.Env,
			corev1.EnvVar{Name: "PROBE_TARGET_IP", Value: targetIP})
		key := createAgentSession(ctx, session)

		expectJobForSession(ctx, ns, session)
		waitForPhase(ctx, key, []scrutineerv1alpha1.AgentSessionPhase{scrutineerv1alpha1.PhaseRunning},
			90*time.Second, 2*time.Second)
		waitForEnvoyPodReady(ctx, types.NamespacedName{Namespace: ns, Name: envoy.ResourceName(session.Name)})

		By("observed evidence flowing before the restart")
		preKeys := map[string]bool{}
		var preRequests int64
		Eventually(func(g Gomega) {
			got := getSession(ctx, key)
			decisions := observedEgressDecisions(got, probeHost)
			g.Expect(decisions).NotTo(BeEmpty(),
				"no observed egress decisions before the restart; decisions=%+v", got.Status.PolicyDecisions)
			for _, d := range decisions {
				preKeys[tamperDecisionKey(d)] = true
			}
			g.Expect(got.Status.Usage).NotTo(BeNil())
			preRequests = got.Status.Usage.NetworkRequests
			g.Expect(preRequests).To(BeNumerically(">=", 1))
		}, 180*time.Second, 3*time.Second).Should(Succeed())

		stopControllerManager()

		By("evidence continuing to land while the controller is down (the reporter path is controller-independent)")
		duringKeys := map[string]bool{}
		Eventually(func(g Gomega) {
			got := getSession(ctx, key)
			novel := 0
			for _, d := range observedEgressDecisions(got, probeHost) {
				k := tamperDecisionKey(d)
				duringKeys[k] = true
				if !preKeys[k] {
					novel++
				}
			}
			g.Expect(novel).To(BeNumerically(">=", 1),
				"no new observed evidence arrived during controller downtime")
		}, 120*time.Second, 3*time.Second).Should(Succeed())

		restartControllerManager()

		By("post-restart: nothing lost, nothing duplicated, usage monotonic")
		Eventually(func(g Gomega) {
			got := getSession(ctx, key)
			keys := map[string]bool{}
			for _, d := range observedEgressDecisions(got, probeHost) {
				g.Expect(d.AssuranceLevel).To(Equal(scrutineerv1alpha1.EvidenceObserved))
				keys[tamperDecisionKey(d)] = true
			}
			for k := range preKeys {
				g.Expect(keys).To(HaveKey(k), "pre-restart decision lost: %s", k)
			}
			for k := range duringKeys {
				g.Expect(keys).To(HaveKey(k), "downtime decision lost across restart: %s", k)
			}
			// Evidence keeps flowing through the restarted controller epoch.
			novel := 0
			for k := range keys {
				if !preKeys[k] && !duringKeys[k] {
					novel++
				}
			}
			g.Expect(novel).To(BeNumerically(">=", 1),
				"no new observed evidence after the controller restarted")
			// Usage never regresses (lost) — and a restart that re-ingested existing
			// decisions would show as duplicates below, not as a usage jump alone.
			g.Expect(got.Status.Usage).NotTo(BeNil())
			g.Expect(got.Status.Usage.NetworkRequests).To(BeNumerically(">=", preRequests),
				"usage counter regressed across restart")
		}, 180*time.Second, 3*time.Second).Should(Succeed())

		final := getSession(ctx, key)
		expectNoDuplicateDecisions(&final)

		requestCancellation(ctx, key)
		waitForTerminalPhase(ctx, key, scrutineerv1alpha1.PhaseCancelled)
	})
})
