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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	scrutineerjob "github.com/grantbarry29/scrutineer/internal/controller/job"
	"github.com/grantbarry29/scrutineer/internal/enforcement/envoy"
)

// Session deletion drives the finalizer path (#149): stop the runtime, wait until it
// is gone, release scrutineer.sh/finalizer, and let owner-reference GC sweep every
// enforcement object. A finalizer bug is the classic operator failure mode — sessions
// stuck Terminating forever, or orphaned Jobs/proxy pods/NetworkPolicies after the
// session is gone — so these specs assert the whole teardown with bounded Eventually
// windows: a wedged finalizer manifests as expectSessionGone timing out.

// deleteSession issues the delete for the AgentSession object.
func deleteSession(ctx context.Context, key client.ObjectKey) {
	GinkgoHelper()
	s := &scrutineerv1alpha1.AgentSession{
		ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace},
	}
	Expect(k8sClient.Delete(ctx, s)).To(Succeed())
}

// expectSessionGone asserts the AgentSession object itself disappears — i.e. the
// finalizer was released. This is the wedge detector: a finalizer that never clears
// leaves the object Terminating and this times out.
func expectSessionGone(ctx context.Context, key client.ObjectKey) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		var got scrutineerv1alpha1.AgentSession
		err := k8sClient.Get(ctx, key, &got)
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue(),
			"AgentSession %s still present (finalizers=%v, deletionTimestamp=%v, err=%v)",
			key, got.Finalizers, got.DeletionTimestamp, err)
	}, 60*time.Second, time.Second).Should(Succeed())
}

// expectSessionPodsGone asserts every pod labeled for the session (the agent pod under
// the Job) is removed or already terminating. Terminating counts: the busybox agent
// ignores SIGTERM, so full pod removal waits out the grace period — what matters here
// is that deletion was issued, not the grace-period clock.
func expectSessionPodsGone(ctx context.Context, ns, sessionName string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		var pods corev1.PodList
		g.Expect(k8sClient.List(ctx, &pods,
			client.InNamespace(ns),
			client.MatchingLabels{scrutineerjob.LabelSessionRef: sessionName},
		)).To(Succeed())
		for i := range pods.Items {
			g.Expect(pods.Items[i].DeletionTimestamp.IsZero()).To(BeFalse(),
				"agent pod %s orphaned after session deletion", pods.Items[i].Name)
		}
	}, 60*time.Second, time.Second).Should(Succeed())
}

// Standard-suite coverage: the simple and held-pre-runtime deletion paths. No CNI or
// image dependency — the runtime here is the plain busybox Job (or no runtime at all).
var _ = Describe("Session deletion (finalizer teardown)", func() {

	It("releases a deleted running session and removes its Job and agent pod", func(ctx SpecContext) {
		ns := newTestNamespace("scrutineer-e2e-delete")
		session := newAgentSession(ns, "delete-running", withLongRunningCommand())
		key := createAgentSession(ctx, session)

		expectJobForSession(ctx, ns, session)
		waitForPhase(ctx, key, []scrutineerv1alpha1.AgentSessionPhase{scrutineerv1alpha1.PhaseRunning},
			90*time.Second, 2*time.Second)

		By("deleting the running session")
		deleteSession(ctx, key)

		By("the finalizer releasing the object within a bounded window")
		expectSessionGone(ctx, key)

		By("the Job and agent pod being removed, not orphaned")
		expectJobGoneForSession(ctx, ns, session)
		expectSessionPodsGone(ctx, ns, session.Name)
	})

	It("releases a deleted session held awaiting approval and garbage-collects its ApprovalRequest", func(ctx SpecContext) {
		ns := newTestNamespace("scrutineer-e2e-delete-held")
		createApprovalPolicy(ctx, ns, "gate-deploys", []string{"deploy"}, withApprovers("e2e-approver"))
		session := newAgentSession(ns, "delete-held", withRequireHumanApproval("deploy"))
		key := createAgentSession(ctx, session)

		By("the approval gate holding the session before any runtime exists")
		waitForPhase(ctx, key, []scrutineerv1alpha1.AgentSessionPhase{scrutineerv1alpha1.PhaseAwaitingApproval},
			60*time.Second, 500*time.Millisecond)
		reqKey := key // 1:1 MVP naming: ApprovalRequest name = session name
		Eventually(func(g Gomega) {
			var req scrutineerv1alpha1.ApprovalRequest
			g.Expect(k8sClient.Get(ctx, reqKey, &req)).To(Succeed())
			g.Expect(req.Status.State).To(Equal(scrutineerv1alpha1.ApprovalStatePending))
		}, 30*time.Second, 500*time.Millisecond).Should(Succeed())
		expectNoJobForSession(ctx, ns, session)

		By("deleting the held session")
		deleteSession(ctx, key)

		By("the finalizer releasing the held object promptly")
		expectSessionGone(ctx, key)

		By("the ApprovalRequest being garbage-collected with no Job ever created")
		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, reqKey, &scrutineerv1alpha1.ApprovalRequest{})
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "ApprovalRequest orphaned after session deletion")
		}, 60*time.Second, time.Second).Should(Succeed())
		expectNoJobForSession(ctx, ns, session)
	})
})

// Networking-suite coverage (issue #149): deletion under the Envoy egress profile must
// sweep the full enforcement set — proxy pod/Service/SA/ConfigMap plus BOTH
// NetworkPolicies (routing lock and Envoy-pod backstop) — and must not wedge when the
// agent is still generating egress at delete time (late evidence delivery lands on the
// reporter's 404-for-missing-session path).
var _ = Describe("Session deletion under the Envoy egress profile", Label(labelNetworking), func() {
	BeforeEach(func(ctx SpecContext) {
		if !clusterImageRunnable(ctx, envoy.DefaultEnvoyImage) {
			Skip("envoy image not available in cluster — run: make kind-load-envoy")
		}
	})

	It("tears down every enforcement object when a running Envoy-profile session is deleted", func(ctx SpecContext) {
		ns := newTestNamespace("scrutineer-e2e-delete-envoy")
		const profileName = "envoy-egress"
		createRuntimeProfileWithEnvoy(ctx, ns, profileName)
		session := newAgentSession(ns, "delete-envoy",
			withRuntimeProfileRef(profileName),
			withLongRunningCommand(),
		)
		key := createAgentSession(ctx, session)

		expectJobForSession(ctx, ns, session)
		waitForPhase(ctx, key, []scrutineerv1alpha1.AgentSessionPhase{scrutineerv1alpha1.PhaseRunning},
			90*time.Second, 2*time.Second)
		egressKey := types.NamespacedName{Namespace: ns, Name: envoy.ResourceName(session.Name)}
		waitForEnvoyPodReady(ctx, egressKey)

		By("the full enforcement set existing before deletion")
		lockKey := client.ObjectKey{Namespace: ns, Name: netpolNameForSession(session)}
		backstopKey := client.ObjectKey{Namespace: ns, Name: backstopNetpolNameForSession(session)}
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, egressKey, &corev1.Service{})).To(Succeed())
			g.Expect(k8sClient.Get(ctx, egressKey, &corev1.ServiceAccount{})).To(Succeed())
			g.Expect(k8sClient.Get(ctx, egressKey, &corev1.ConfigMap{})).To(Succeed())
			g.Expect(k8sClient.Get(ctx, lockKey, &networkingv1.NetworkPolicy{})).To(Succeed())
			g.Expect(k8sClient.Get(ctx, backstopKey, &networkingv1.NetworkPolicy{})).To(Succeed())
		}, 30*time.Second, time.Second).Should(Succeed())

		By("deleting the session mid-run")
		deleteSession(ctx, key)

		By("the finalizer releasing the object within a bounded window")
		expectSessionGone(ctx, key)

		By("the Job and agent pod being removed")
		expectJobGoneForSession(ctx, ns, session)
		expectSessionPodsGone(ctx, ns, session.Name)

		By("the egress proxy set being removed")
		Eventually(func(g Gomega) {
			g.Expect(egressObjectGone(ctx, egressKey, &corev1.Pod{})).To(BeTrue(), "envoy pod orphaned")
			g.Expect(egressObjectGone(ctx, egressKey, &corev1.Service{})).To(BeTrue(), "envoy service orphaned")
			g.Expect(egressObjectGone(ctx, egressKey, &corev1.ServiceAccount{})).To(BeTrue(), "envoy SA orphaned")
			g.Expect(egressObjectGone(ctx, egressKey, &corev1.ConfigMap{})).To(BeTrue(), "envoy configmap orphaned")
		}, 60*time.Second, 2*time.Second).Should(Succeed())

		By("both NetworkPolicies being removed")
		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, lockKey, &networkingv1.NetworkPolicy{})
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "routing-lock NetworkPolicy orphaned")
			err = k8sClient.Get(ctx, backstopKey, &networkingv1.NetworkPolicy{})
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "egress backstop NetworkPolicy orphaned")
		}, 60*time.Second, 2*time.Second).Should(Succeed())
	})

	It("does not wedge teardown when the session is deleted during active egress", func(ctx SpecContext) {
		requireLiveEgressEvidenceImages(ctx)
		deployInClusterReporter(ctx)

		ns := newTestNamespace("scrutineer-e2e-delete-egress")
		const profileName = "envoy-egress"
		createRuntimeProfileWithEnvoy(ctx, ns, profileName)

		// Non-resolvable on purpose (suite convention): the spec needs traffic through
		// Envoy and the evidence pipeline, not upstream success.
		const probeHost = "delete.scrutineer.invalid"
		session := newAgentSession(ns, "delete-live-egress",
			withRuntimeProfileRef(profileName),
			withEnvoyEgressProbe(probeHost),
		)
		key := createAgentSession(ctx, session)

		expectJobForSession(ctx, ns, session)
		waitForPhase(ctx, key, []scrutineerv1alpha1.AgentSessionPhase{scrutineerv1alpha1.PhaseRunning},
			90*time.Second, 2*time.Second)
		egressKey := types.NamespacedName{Namespace: ns, Name: envoy.ResourceName(session.Name)}
		waitForEnvoyPodReady(ctx, egressKey)

		By("observed evidence actively flowing before deletion")
		Eventually(func(g Gomega) {
			got := getSession(ctx, key)
			found := false
			for _, d := range got.Status.PolicyDecisions {
				if d.Phase == scrutineerv1alpha1.PolicyDecisionPhaseRuntime &&
					d.Type == "network" && d.Actor == envoy.AccessLogActor {
					found = true
					break
				}
			}
			g.Expect(found).To(BeTrue(),
				"no observed egress decisions yet; decisions=%+v", got.Status.PolicyDecisions)
		}, 150*time.Second, 3*time.Second).Should(Succeed())

		By("deleting the session while the agent is still generating egress")
		deleteSession(ctx, key)

		By("teardown completing without wedging on late evidence delivery")
		expectSessionGone(ctx, key)
		expectJobGoneForSession(ctx, ns, session)
		expectSessionPodsGone(ctx, ns, session.Name)
		Eventually(func(g Gomega) {
			g.Expect(egressObjectGone(ctx, egressKey, &corev1.Pod{})).To(BeTrue(), "envoy pod orphaned")
			g.Expect(egressObjectGone(ctx, egressKey, &corev1.Service{})).To(BeTrue(), "envoy service orphaned")
		}, 60*time.Second, 2*time.Second).Should(Succeed())

		By("the in-cluster reporter staying healthy after absorbing the late-evidence race")
		Consistently(func(g Gomega) {
			var dep appsv1.Deployment
			g.Expect(k8sClient.Get(ctx,
				client.ObjectKey{Namespace: scrutineerSystemNamespace, Name: e2eReporterDeploy}, &dep)).To(Succeed())
			g.Expect(dep.Status.ReadyReplicas).To(Equal(int32(1)),
				"reporter deployment unhealthy after session deletion")
		}, 10*time.Second, 2*time.Second).Should(Succeed())
	})
})
