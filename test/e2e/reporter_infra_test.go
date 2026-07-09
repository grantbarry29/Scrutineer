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
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/grantbarry29/scrutineer/internal/enforcement/envoy"
	"github.com/grantbarry29/scrutineer/internal/enforcement/lockverify"
)

const (
	scrutineerSystemNamespace = "scrutineer-system"
	e2eReporterLabel          = "scrutineer.sh/e2e-component"
	e2eReporterLabelVal       = "runtime-reporter"
	e2eReporterDeploy         = "scrutineer-e2e-runtime-reporter"
	e2eReporterService        = "scrutineer-controller-reporter"
	e2eReporterSA             = "scrutineer-e2e-runtime-reporter"
	e2eReporterClusterRole    = "scrutineer-e2e-runtime-reporter"
)

// scrutineerE2EImage is the controller image loaded into kind for the in-cluster
// reporter. The fallback derives from this test binary's baked version: under the make
// targets (which pass VERSION_LDFLAGS) it is the dev image test-e2e-images built and
// loaded; under a bare `go test` it resolves to the nonexistent v0.0.0-dev image and
// the live specs skip with a pointer to the make target (#112).
func scrutineerE2EImage() string {
	if img := os.Getenv("SCRUTINEER_E2E_IMG"); img != "" {
		return img
	}
	return lockverify.DefaultProbeImage()
}

// requireLiveEgressEvidenceImages skips the spec unless the Envoy egress-proxy images
// (Envoy + egress-reporter) are present in kind — the sole observed-evidence path (#71).
func requireLiveEgressEvidenceImages(ctx SpecContext) {
	GinkgoHelper()
	requireScrutineerE2EImage(ctx)
	if !clusterImageRunnable(ctx, envoy.DefaultEnvoyImage) {
		Skip("envoy image not available in cluster — run: make kind-load-envoy")
	}
	if !clusterImageRunnable(ctx, envoy.DefaultEgressReporterImage()) {
		Skip("egress-reporter image not available in cluster — run: make kind-load-egress-reporter")
	}
}

func requireScrutineerE2EImage(ctx SpecContext) {
	GinkgoHelper()
	if !clusterImageRunnable(ctx, scrutineerE2EImage()) {
		Skip("scrutineer image not available in cluster — run: make kind-load")
	}
}

// clusterImageRunnable reports whether image is already present on a cluster node, so a
// pod scheduled with ImagePullPolicy=IfNotPresent can run it. It inspects
// node.status.images rather than launching a probe pod: the scrutineer/sidecar images are
// distroless (no shell), so a `sh -c` probe would fail even when the image is fine to run.
func clusterImageRunnable(ctx context.Context, image string) bool {
	GinkgoHelper()
	var nodes corev1.NodeList
	if err := k8sClient.List(ctx, &nodes); err != nil {
		return false
	}
	want := imageCandidates(image)
	for i := range nodes.Items {
		for _, img := range nodes.Items[i].Status.Images {
			for _, name := range img.Names {
				n := normalizeImageRef(name)
				for _, w := range want {
					if n == w {
						return true
					}
				}
			}
		}
	}
	return false
}

// normalizeImageRef strips default-registry prefixes so a user-supplied ref like
// "scrutineer-e2e-shell:latest" matches the fully-qualified "docker.io/library/..." form that
// the container runtime reports in node.status.images.
func normalizeImageRef(ref string) string {
	for _, prefix := range []string{"index.docker.io/library/", "index.docker.io/", "docker.io/library/", "docker.io/"} {
		ref = strings.TrimPrefix(ref, prefix)
	}
	return ref
}

// imageCandidates expands an image ref into the equivalent forms a node may report in
// status.images. A digest-pinned ref "repo:tag@sha256:D" is stored by the node as separate
// "repo:tag" and "repo@sha256:D" entries, so a plain equality check on the combined ref
// never matches — return all forms and match any.
func imageCandidates(ref string) []string {
	ref = normalizeImageRef(ref)
	out := []string{ref}
	base, digest, ok := strings.Cut(ref, "@")
	if !ok {
		return out
	}
	out = append(out, base) // repo:tag
	repo := base
	if i := strings.LastIndex(base, ":"); i >= 0 && !strings.Contains(base[i+1:], "/") {
		repo = base[:i] // strip the tag → bare repo
	}
	out = append(out, repo+"@"+digest) // repo@sha256:D
	return out
}

func deployInClusterReporter(ctx SpecContext) {
	GinkgoHelper()
	ensureScrutineerSystemNamespace(ctx)

	labels := map[string]string{
		e2eReporterLabel:              e2eReporterLabelVal,
		"app.kubernetes.io/name":      "scrutineer",
		"app.kubernetes.io/component": "runtime-reporter",
	}

	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      e2eReporterSA,
			Namespace: scrutineerSystemNamespace,
			Labels:    labels,
		},
	}
	Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, sa))).To(Succeed())

	cr := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: e2eReporterClusterRole, Labels: labels},
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
		ObjectMeta: metav1.ObjectMeta{Name: e2eReporterClusterRole, Labels: labels},
		Subjects: []rbacv1.Subject{{
			Kind:      "ServiceAccount",
			Name:      e2eReporterSA,
			Namespace: scrutineerSystemNamespace,
		}},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     e2eReporterClusterRole,
		},
	}
	Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, crb))).To(Succeed())

	replicas := int32(1)
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      e2eReporterDeploy,
			Namespace: scrutineerSystemNamespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					ServiceAccountName: e2eReporterSA,
					Containers: []corev1.Container{{
						Name:            "reporter",
						Image:           scrutineerE2EImage(),
						ImagePullPolicy: corev1.PullIfNotPresent,
						Args: []string{
							"--reporter-only",
							"--reporter-bind-address=:8088",
							"--metrics-bind-address=0",
							"--health-probe-bind-address=0",
						},
					}},
				},
			},
		},
	}
	Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, deploy))).To(Succeed())

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      e2eReporterService,
			Namespace: scrutineerSystemNamespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports: []corev1.ServicePort{{
				Name:       "reporter",
				Port:       8088,
				TargetPort: intstr.FromInt32(8088),
			}},
		},
	}
	Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, svc))).To(Succeed())

	Eventually(func(g Gomega) {
		var got appsv1.Deployment
		g.Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: scrutineerSystemNamespace, Name: e2eReporterDeploy}, &got)).To(Succeed())
		g.Expect(got.Status.ReadyReplicas).To(Equal(int32(1)))
	}, 90*time.Second, time.Second).Should(Succeed())
}

func ensureScrutineerSystemNamespace(ctx context.Context) {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: scrutineerSystemNamespace}}
	Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, ns))).To(Succeed())
}

func cleanupInClusterReporter(ctx context.Context) {
	_ = k8sClient.Delete(ctx, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: e2eReporterDeploy, Namespace: scrutineerSystemNamespace},
	})
	_ = k8sClient.Delete(ctx, &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: e2eReporterService, Namespace: scrutineerSystemNamespace},
	})
	_ = k8sClient.Delete(ctx, &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: e2eReporterSA, Namespace: scrutineerSystemNamespace},
	})
	_ = k8sClient.Delete(ctx, &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: e2eReporterClusterRole},
	})
	_ = k8sClient.Delete(ctx, &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: e2eReporterClusterRole},
	})
	Eventually(func() bool {
		err := k8sClient.Get(ctx, client.ObjectKey{Namespace: scrutineerSystemNamespace, Name: e2eReporterDeploy}, &appsv1.Deployment{})
		return apierrors.IsNotFound(err)
	}, 30*time.Second, time.Second).Should(BeTrue())
}
