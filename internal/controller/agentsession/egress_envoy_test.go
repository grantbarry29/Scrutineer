/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package agentsession

import (
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
	scrutineerjob "github.com/grantbarry29/scrutineer/internal/controller/job"
	"github.com/grantbarry29/scrutineer/internal/enforcement/envoy"
	"github.com/grantbarry29/scrutineer/internal/enforcement/networkpolicy"
)

// envoyProfile creates a RuntimeProfile in ns that enables the out-of-pod Envoy egress proxy.
func envoyProfile(ns, name string) *scrutineerv1alpha1.RuntimeProfile {
	enabled := true
	rp := &scrutineerv1alpha1.RuntimeProfile{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: scrutineerv1alpha1.RuntimeProfileSpec{
			Enforcement: []scrutineerv1alpha1.RuntimeProfileEnforcement{
				{Name: "envoy", Type: scrutineerjob.EnforcementTypeEnvoy, Enabled: &enabled},
			},
		},
	}
	Expect(k8sClient.Create(testCtx, rp)).To(Succeed())
	return rp
}

// expectOwnedBy asserts obj carries a controller owner reference to session.
func expectOwnedBy(obj metav1.Object, session *scrutineerv1alpha1.AgentSession) {
	GinkgoHelper()
	owned := false
	for _, ref := range obj.GetOwnerReferences() {
		if ref.UID == session.UID && ref.Controller != nil && *ref.Controller {
			owned = true
		}
	}
	Expect(owned).To(BeTrue(), "expected %q to be controller-owned by the AgentSession", obj.GetName())
}

var _ = Describe("Per-session Envoy egress proxy", func() {
	It("provisions an owned ConfigMap, Service, Pod, and ServiceAccount when enabled", func() {
		ns := newTestNamespace()
		envoyProfile(ns, "egress")

		session := minimalAgentSession(ns, "egress-on")
		session.Spec.RuntimeProfileRef = &scrutineerv1alpha1.RuntimeProfileRef{Name: "egress"}
		Expect(k8sClient.Create(testCtx, session)).To(Succeed())
		Expect(k8sClient.Get(testCtx, client.ObjectKeyFromObject(session), session)).To(Succeed())

		waitForJob(ns, session)

		name := envoy.ResourceName(session.Name)
		key := types.NamespacedName{Namespace: ns, Name: name}

		var cm corev1.ConfigMap
		var svc corev1.Service
		var pod corev1.Pod
		var sa corev1.ServiceAccount
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(testCtx, key, &cm)).To(Succeed())
			g.Expect(k8sClient.Get(testCtx, key, &svc)).To(Succeed())
			g.Expect(k8sClient.Get(testCtx, key, &pod)).To(Succeed())
			g.Expect(k8sClient.Get(testCtx, key, &sa)).To(Succeed())
		}, controllerWaitTimeout, controllerPollInterval).Should(Succeed())

		expectOwnedBy(&cm, session)
		expectOwnedBy(&svc, session)
		expectOwnedBy(&pod, session)
		expectOwnedBy(&sa, session)

		// The proxy pod runs as its dedicated per-session identity, not the default SA.
		Expect(pod.Spec.ServiceAccountName).To(Equal(name))
		Expect(svc.Spec.Ports).To(HaveLen(1))
		Expect(svc.Spec.Ports[0].Port).To(Equal(int32(envoy.ProxyPort)))

		// Slice C: the egress-reporter container rides beside Envoy with the projected
		// per-session token — the identity behind observed evidence.
		podContainers := map[string]corev1.Container{}
		for _, c := range pod.Spec.Containers {
			podContainers[c.Name] = c
		}
		Expect(podContainers).To(HaveKey("egress-reporter"))
		Expect(podContainers["egress-reporter"].Image).To(Equal(envoy.DefaultEgressReporterImage))
		var hasToken bool
		for _, v := range pod.Spec.Volumes {
			if v.Projected != nil {
				for _, src := range v.Projected.Sources {
					if src.ServiceAccountToken != nil && src.ServiceAccountToken.Audience == scrutineerjob.ReporterTokenAudience {
						hasToken = true
					}
				}
			}
		}
		Expect(hasToken).To(BeTrue(), "expected a projected reporter-audience token volume on the Envoy pod")

		// The controller resolves the Envoy Service ClusterIP into status and points the
		// agent at that IP (not a DNS name) so it needs no DNS under the routing lock.
		Eventually(func(g Gomega) {
			var got scrutineerv1alpha1.AgentSession
			g.Expect(k8sClient.Get(testCtx, client.ObjectKeyFromObject(session), &got)).To(Succeed())
			g.Expect(got.Status.EgressProxyEndpoint).NotTo(BeEmpty(), "controller did not resolve the Envoy ClusterIP endpoint")
		}, controllerWaitTimeout, controllerPollInterval).Should(Succeed())

		Expect(k8sClient.Get(testCtx, key, &svc)).To(Succeed())
		wantEndpoint := "http://" + svc.Spec.ClusterIP + ":15001"
		var got scrutineerv1alpha1.AgentSession
		Expect(k8sClient.Get(testCtx, client.ObjectKeyFromObject(session), &got)).To(Succeed())
		Expect(got.Status.EgressProxyEndpoint).To(Equal(wantEndpoint))

		// The agent is pointed at that ClusterIP via explicit-proxy env; Envoy is out-of-pod.
		var job batchv1.Job
		Expect(k8sClient.Get(testCtx, types.NamespacedName{Namespace: ns, Name: jobNameFor(session)}, &job)).To(Succeed())
		byName := map[string]corev1.Container{}
		for _, c := range job.Spec.Template.Spec.Containers {
			byName[c.Name] = c
		}
		Expect(byName).NotTo(HaveKey("envoy"))
		agentEnv := envMap(byName[scrutineerjob.AgentContainerName].Env)
		Expect(agentEnv[scrutineerjob.EnvHTTPSProxy]).To(Equal(wantEndpoint))
	})

	It("tears the egress proxy down when the session reaches a terminal phase", func() {
		ns := newTestNamespace()
		envoyProfile(ns, "egress")

		session := minimalAgentSession(ns, "egress-teardown")
		session.Spec.RuntimeProfileRef = &scrutineerv1alpha1.RuntimeProfileRef{Name: "egress"}
		Expect(k8sClient.Create(testCtx, session)).To(Succeed())

		waitForJob(ns, session)
		key := types.NamespacedName{Namespace: ns, Name: envoy.ResourceName(session.Name)}
		Eventually(func() error {
			var cm corev1.ConfigMap
			return k8sClient.Get(testCtx, key, &cm)
		}, controllerWaitTimeout, controllerPollInterval).Should(Succeed())

		// Drive the session terminal by completing the Job.
		var job batchv1.Job
		Expect(k8sClient.Get(testCtx, types.NamespacedName{Namespace: ns, Name: jobNameFor(session)}, &job)).To(Succeed())
		setJobSucceeded(&job)
		waitForPhase(client.ObjectKeyFromObject(session), scrutineerv1alpha1.PhaseSucceeded)

		Eventually(func(g Gomega) {
			var cm corev1.ConfigMap
			var svc corev1.Service
			var pod corev1.Pod
			var sa corev1.ServiceAccount
			g.Expect(apierrors.IsNotFound(k8sClient.Get(testCtx, key, &cm))).To(BeTrue())
			g.Expect(apierrors.IsNotFound(k8sClient.Get(testCtx, key, &svc))).To(BeTrue())
			g.Expect(gone(k8sClient.Get(testCtx, key, &pod), &pod)).To(BeTrue())
			g.Expect(apierrors.IsNotFound(k8sClient.Get(testCtx, key, &sa))).To(BeTrue())
		}, controllerWaitTimeout, controllerPollInterval).Should(Succeed())
	})

	It("creates the agent routing lock and the Envoy egress backstop, and tears them down", func() {
		ns := newTestNamespace()
		envoyProfile(ns, "egress")

		session := minimalAgentSession(ns, "egress-np")
		session.Spec.RuntimeProfileRef = &scrutineerv1alpha1.RuntimeProfileRef{Name: "egress"}
		Expect(k8sClient.Create(testCtx, session)).To(Succeed())
		waitForJob(ns, session)

		lockKey := types.NamespacedName{Namespace: ns, Name: networkpolicy.NameFor(ns, session.Name)}
		backstopKey := types.NamespacedName{Namespace: ns, Name: networkpolicy.BackstopNameFor(ns, session.Name)}
		var lock, backstop networkingv1.NetworkPolicy
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(testCtx, lockKey, &lock)).To(Succeed())
			g.Expect(k8sClient.Get(testCtx, backstopKey, &backstop)).To(Succeed())
		}, controllerWaitTimeout, controllerPollInterval).Should(Succeed())

		expectOwnedBy(&lock, session)
		expectOwnedBy(&backstop, session)
		// The lock is on the agent pod; the backstop is on the Envoy pod.
		Expect(lock.Spec.PodSelector.MatchLabels[scrutineerjob.LabelSessionRef]).To(Equal(session.Name))
		Expect(backstop.Spec.PodSelector.MatchLabels).To(Equal(envoy.Labels(session.Name)))
		// The backstop denies the cloud-metadata range even for Envoy.
		var excepted []string
		for _, rule := range backstop.Spec.Egress {
			for _, to := range rule.To {
				if to.IPBlock != nil {
					excepted = append(excepted, to.IPBlock.Except...)
				}
			}
		}
		Expect(excepted).To(ContainElement("169.254.0.0/16"))

		// Both are torn down when the session goes terminal.
		var job batchv1.Job
		Expect(k8sClient.Get(testCtx, types.NamespacedName{Namespace: ns, Name: jobNameFor(session)}, &job)).To(Succeed())
		setJobSucceeded(&job)
		waitForPhase(client.ObjectKeyFromObject(session), scrutineerv1alpha1.PhaseSucceeded)
		Eventually(func(g Gomega) {
			g.Expect(apierrors.IsNotFound(k8sClient.Get(testCtx, lockKey, &lock))).To(BeTrue())
			g.Expect(apierrors.IsNotFound(k8sClient.Get(testCtx, backstopKey, &backstop))).To(BeTrue())
		}, controllerWaitTimeout, controllerPollInterval).Should(Succeed())
	})

	It("renders the effective FQDN policy into the Envoy config and propagates policy drift (#32)", func() {
		ns := newTestNamespace()
		envoyProfile(ns, "egress")
		Expect(k8sClient.Create(testCtx, &scrutineerv1alpha1.AgentPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "fqdn", Namespace: ns},
			Spec: scrutineerv1alpha1.AgentPolicySpec{
				Mode:        scrutineerv1alpha1.PolicyModeEnforced,
				PolicyRules: scrutineerv1alpha1.PolicyRules{DeniedDomains: []string{"evil.example"}},
			},
		})).To(Succeed())

		session := minimalAgentSession(ns, "egress-fqdn")
		session.Spec.RuntimeProfileRef = &scrutineerv1alpha1.RuntimeProfileRef{Name: "egress"}
		session.Spec.PolicyRefs = []scrutineerv1alpha1.PolicyRef{{Kind: "AgentPolicy", Name: "fqdn"}}
		Expect(k8sClient.Create(testCtx, session)).To(Succeed())
		waitForJob(ns, session)

		cmKey := types.NamespacedName{Namespace: ns, Name: envoy.ResourceName(session.Name)}

		By("the ConfigMap carrying an RBAC deny for the denied domain")
		var initialHash string
		Eventually(func(g Gomega) {
			var cm corev1.ConfigMap
			g.Expect(k8sClient.Get(testCtx, cmKey, &cm)).To(Succeed())
			g.Expect(cm.Data["envoy.yaml"]).To(ContainSubstring("filters.http.rbac.v3.RBAC"))
			g.Expect(cm.Data["envoy.yaml"]).To(ContainSubstring(`evil\.example`))
			initialHash = cm.Annotations[envoy.ConfigHashAnnotation]
			g.Expect(initialHash).NotTo(BeEmpty())
		}, controllerWaitTimeout, controllerPollInterval).Should(Succeed())

		By("a policy change re-rendering the config (drift propagation)")
		var ap scrutineerv1alpha1.AgentPolicy
		Expect(k8sClient.Get(testCtx, types.NamespacedName{Namespace: ns, Name: "fqdn"}, &ap)).To(Succeed())
		ap.Spec.PolicyRules.DeniedDomains = []string{"evil.example", "*.tracker.example"}
		Expect(k8sClient.Update(testCtx, &ap)).To(Succeed())

		Eventually(func(g Gomega) {
			var cm corev1.ConfigMap
			g.Expect(k8sClient.Get(testCtx, cmKey, &cm)).To(Succeed())
			g.Expect(cm.Data["envoy.yaml"]).To(ContainSubstring(`tracker\.example`))
			g.Expect(cm.Annotations[envoy.ConfigHashAnnotation]).NotTo(Equal(initialHash),
				"config hash must change when the policy changes")
		}, controllerWaitTimeout, controllerPollInterval).Should(Succeed())
	})

	It("does not provision an egress proxy when no profile enables it", func() {
		ns := newTestNamespace()
		session := minimalAgentSession(ns, "egress-off")
		Expect(k8sClient.Create(testCtx, session)).To(Succeed())

		waitForJob(ns, session)
		key := types.NamespacedName{Namespace: ns, Name: envoy.ResourceName(session.Name)}
		Consistently(func() bool {
			var cm corev1.ConfigMap
			return apierrors.IsNotFound(k8sClient.Get(testCtx, key, &cm))
		}, "2s", controllerPollInterval).Should(BeTrue())
	})
})

// gone reports NotFound, or (in envtest, where GC does not run) an object already marked
// for deletion.
func gone(err error, obj metav1.Object) bool {
	if apierrors.IsNotFound(err) {
		return true
	}
	return err == nil && obj.GetDeletionTimestamp() != nil
}
