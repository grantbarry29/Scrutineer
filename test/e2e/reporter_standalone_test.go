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
	"os"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

// Path (relative to test/e2e) of the shipped standalone-reporter manifest whose
// Deployment/ServiceAccount this spec exercises directly, rather than a Go replica.
const shippedReporterManifest = "../../config/reporter-standalone/reporter.yaml"

const (
	shippedReporterSA          = "scrutineer-reporter"
	shippedReporterDeploy      = "scrutineer-controller-reporter"
	standaloneReporterRoleName = "scrutineer-e2e-standalone-reporter"
)

// loadShippedReporterObjects parses config/reporter-standalone/reporter.yaml into its
// ServiceAccount and Deployment so the test asserts on and deploys the *shipped* artifact.
func loadShippedReporterObjects() (*corev1.ServiceAccount, *appsv1.Deployment) {
	GinkgoHelper()
	raw, err := os.ReadFile(shippedReporterManifest)
	Expect(err).NotTo(HaveOccurred(), "read %s", shippedReporterManifest)

	var sa *corev1.ServiceAccount
	var deploy *appsv1.Deployment
	for _, doc := range strings.Split(string(raw), "\n---") {
		if strings.TrimSpace(doc) == "" {
			continue
		}
		var tm metav1.TypeMeta
		Expect(yaml.Unmarshal([]byte(doc), &tm)).To(Succeed())
		switch tm.Kind {
		case "ServiceAccount":
			sa = &corev1.ServiceAccount{}
			Expect(yaml.Unmarshal([]byte(doc), sa)).To(Succeed())
		case "Deployment":
			deploy = &appsv1.Deployment{}
			Expect(yaml.Unmarshal([]byte(doc), deploy)).To(Succeed())
		}
	}
	Expect(sa).NotTo(BeNil(), "reporter.yaml must contain a ServiceAccount")
	Expect(deploy).NotTo(BeNil(), "reporter.yaml must contain a Deployment")
	return sa, deploy
}

var _ = Describe("standalone reporter overlay", func() {
	// This exercises the committed config/reporter-standalone/reporter.yaml artifact end
	// to end: it deploys the shipped ServiceAccount + --reporter-only Deployment (with the
	// kind-loaded image substituted for the overlay's controller:latest tag) plus the
	// reporter-role RBAC, and asserts the pod boots and passes its readiness probe. This
	// catches drift in the shipped Deployment spec (args, ports, securityContext, probes)
	// that the Go-constructed reporter in reporter_infra_test.go cannot.
	//
	// The full overlay layers on config/default (manager included), which would collide
	// with the suite's in-process manager; scope here is deliberately the reporter artifact.
	It("boots the shipped reporter Deployment in --reporter-only mode", func(ctx SpecContext) {
		requireScrutineerE2EImage(ctx)

		sa, deploy := loadShippedReporterObjects()

		By("asserting the shipped Deployment runs the standalone reporter")
		Expect(sa.Name).To(Equal(shippedReporterSA))
		Expect(deploy.Name).To(Equal(shippedReporterDeploy))
		Expect(deploy.Spec.Template.Spec.ServiceAccountName).To(Equal(shippedReporterSA))
		Expect(deploy.Spec.Template.Spec.Containers).To(HaveLen(1))
		Expect(deploy.Spec.Template.Spec.Containers[0].Args).To(ContainElement("--reporter-only"))

		By("substituting the kind-loaded image for the overlay's controller:latest tag")
		deploy.Spec.Template.Spec.Containers[0].Image = scrutineerE2EImage()
		deploy.Spec.Template.Spec.Containers[0].ImagePullPolicy = corev1.PullIfNotPresent

		ensureScrutineerSystemNamespace(ctx)
		deployStandaloneReporterRBAC(ctx)

		DeferCleanup(func(ctx context.Context) {
			cleanupStandaloneReporter(ctx)
		})

		By("applying the shipped ServiceAccount and Deployment")
		Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, sa))).To(Succeed())
		Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, deploy))).To(Succeed())

		By("waiting for the reporter pod to become ready (readiness probe passes)")
		Eventually(func(g Gomega) {
			var got appsv1.Deployment
			g.Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: scrutineerSystemNamespace, Name: shippedReporterDeploy}, &got)).To(Succeed())
			g.Expect(got.Status.ReadyReplicas).To(Equal(int32(1)))
		}, 90*time.Second, time.Second).Should(Succeed())
	})
})

// deployStandaloneReporterRBAC grants the shipped scrutineer-reporter ServiceAccount the
// reporter-role permissions it needs (the overlay retargets reporter-role to this SA; here
// we bind an equivalent ClusterRole so the standalone reporter can read sessions and merge
// status). Mirrors the rules in deployInClusterReporter.
func deployStandaloneReporterRBAC(ctx context.Context) {
	GinkgoHelper()
	cr := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: standaloneReporterRoleName},
		Rules: []rbacv1.PolicyRule{
			{APIGroups: []string{"authentication.k8s.io"}, Resources: []string{"tokenreviews"}, Verbs: []string{"create"}},
			{APIGroups: []string{"scrutineer.sh"}, Resources: []string{"agentsessions"}, Verbs: []string{"get"}},
			{APIGroups: []string{"scrutineer.sh"}, Resources: []string{"agentsessions/status"}, Verbs: []string{"get", "update", "patch"}},
			{APIGroups: []string{"scrutineer.sh"}, Resources: []string{"approvalrequests"}, Verbs: []string{"get", "list", "create"}},
			{APIGroups: []string{"batch"}, Resources: []string{"jobs"}, Verbs: []string{"get"}},
			{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get"}},
		},
	}
	Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, cr))).To(Succeed())

	crb := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: standaloneReporterRoleName},
		Subjects: []rbacv1.Subject{{
			Kind:      "ServiceAccount",
			Name:      shippedReporterSA,
			Namespace: scrutineerSystemNamespace,
		}},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     standaloneReporterRoleName,
		},
	}
	Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, crb))).To(Succeed())
}

func cleanupStandaloneReporter(ctx context.Context) {
	_ = k8sClient.Delete(ctx, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: shippedReporterDeploy, Namespace: scrutineerSystemNamespace},
	})
	_ = k8sClient.Delete(ctx, &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: shippedReporterSA, Namespace: scrutineerSystemNamespace},
	})
	_ = k8sClient.Delete(ctx, &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: standaloneReporterRoleName}})
	_ = k8sClient.Delete(ctx, &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: standaloneReporterRoleName}})
	Eventually(func() bool {
		err := k8sClient.Get(ctx, client.ObjectKey{Namespace: scrutineerSystemNamespace, Name: shippedReporterDeploy}, &appsv1.Deployment{})
		return apierrors.IsNotFound(err)
	}, 30*time.Second, time.Second).Should(BeTrue())
}
