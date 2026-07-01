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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	scrutineerjob "github.com/grantbarry29/scrutineer/internal/controller/job"
	"github.com/grantbarry29/scrutineer/internal/enforcement/envoy"
)

// envoyProfile creates a RuntimeProfile in ns that enables the out-of-pod Envoy egress proxy.
func envoyProfile(ns, name string) *scrutineerv1alpha1.RuntimeProfile {
	enabled := true
	rp := &scrutineerv1alpha1.RuntimeProfile{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: scrutineerv1alpha1.RuntimeProfileSpec{
			Sidecars: []scrutineerv1alpha1.RuntimeProfileSidecar{
				{Name: "envoy", Type: scrutineerjob.SidecarTypeEnvoy, Enabled: &enabled},
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

		// The agent is pointed at its per-session Envoy via explicit-proxy env.
		var job batchv1.Job
		Expect(k8sClient.Get(testCtx, types.NamespacedName{Namespace: ns, Name: jobNameFor(session)}, &job)).To(Succeed())
		byName := map[string]corev1.Container{}
		for _, c := range job.Spec.Template.Spec.Containers {
			byName[c.Name] = c
		}
		// Envoy is out-of-pod: no in-pod envoy container.
		Expect(byName).NotTo(HaveKey("envoy"))
		agentEnv := envMap(byName[scrutineerjob.AgentContainerName].Env)
		Expect(agentEnv[scrutineerjob.EnvHTTPSProxy]).To(Equal(envoy.ProxyURL(session.Name, ns)))
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
