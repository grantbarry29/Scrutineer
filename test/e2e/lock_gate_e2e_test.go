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

	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
)

// Verified-or-refused gate (#70, docs/design/untamperable-enforcement.md §4). This spec runs
// on BOTH networking clusters with opposite expectations: a CNI that enforces
// NetworkPolicy must verify and run the enforced session; a CNI that does not must
// refuse it — loudly, via the EgressLockVerified condition — and never create its Job.
var _ = Describe("egress lock verification gate", Label(labelNetworking), func() {
	BeforeEach(func(ctx SpecContext) {
		if !lockVerifyEnabled() {
			Skip("lock verifier not enabled — run via make test-e2e-net (" + envLockVerify + "=1)")
		}
		// The verifier's canary pods run the controller image.
		requireScrutineerE2EImage(ctx)
	})

	It("holds enforced sessions unless NetworkPolicy enforcement is proven", func(ctx SpecContext) {
		// The suite-wide verdict (probed once, #156) decides which branch this cluster
		// must take; the spec then asserts the verifier reached the same conclusion.
		enforcing := egressEnforcingCNI(ctx)

		ns := newTestNamespace("scrutineer-e2e-lockgate")
		createRuntimeProfileWithEnvoy(ctx, ns, "envoy-lockgate")
		createEnforcedDeniedDomainPolicy(ctx, ns, "deny-gate", "gate.scrutineer.invalid")

		session := newAgentSession(ns, "lockgate",
			withRuntimeProfileRef("envoy-lockgate"),
			withPolicyRef("AgentPolicy", "deny-gate"),
		)
		key := createAgentSession(ctx, session)

		if enforcing {
			By("enforcing CNI: the verifier must verify and the session must run")
			Eventually(func(g Gomega) {
				got := getSession(ctx, key)
				c := meta.FindStatusCondition(got.Status.Conditions, "EgressLockVerified")
				g.Expect(c).NotTo(BeNil(), "EgressLockVerified condition must be set")
				g.Expect(c.Status).To(Equal(metav1.ConditionTrue), "condition: %+v", c)
			}, 180*time.Second, 3*time.Second).Should(Succeed())
			expectJobForSession(ctx, ns, session)

			requestCancellation(ctx, key)
			waitForTerminalPhase(ctx, key, scrutineerv1alpha1.PhaseCancelled)
			return
		}

		By("non-enforcing CNI: the session must be refused with an explanatory condition")
		Eventually(func(g Gomega) {
			got := getSession(ctx, key)
			c := meta.FindStatusCondition(got.Status.Conditions, "EgressLockVerified")
			g.Expect(c).NotTo(BeNil(), "EgressLockVerified condition must be set")
			g.Expect(c.Status).To(Equal(metav1.ConditionFalse), "condition: %+v", c)
			g.Expect(c.Reason).To(Equal("CNIDoesNotEnforceNetworkPolicy"), "condition: %+v", c)
		}, 180*time.Second, 3*time.Second).Should(Succeed())

		By("confirming the runtime Job is never created while held")
		jobName := jobNameForSession(session)
		Consistently(func() bool {
			var job batchv1.Job
			err := k8sClient.Get(ctx, client.ObjectKey{Namespace: ns, Name: jobName}, &job)
			return err != nil
		}, 15*time.Second, 2*time.Second).Should(BeTrue(), "Job %s/%s must not exist on an unverified cluster", ns, jobName)

		got := getSession(ctx, key)
		Expect(got.Status.Phase).To(Equal(scrutineerv1alpha1.PhasePending), "held session stays Pending")
	})
})
