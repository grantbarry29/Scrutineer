/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package v1alpha1

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
)

// This suite runs the identity-stamping webhook against an envtest control plane
// with the generated MutatingWebhookConfiguration installed, exercising the real
// admission path (authenticated userInfo → spec.decidedBy). It requires the
// envtest binaries; `make test` sets KUBEBUILDER_ASSETS. When that is unset (e.g.
// a bare `go test ./internal/webhook/...`), the suite skips so the pure-unit
// tests in this package still run standalone.

var (
	cfg       *rest.Config
	k8sClient client.Client
	testEnv   *envtest.Environment
	scheme    *runtime.Scheme
	testCtx   context.Context
	cancel    context.CancelFunc
)

func TestWebhookEnvtest(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS not set; skipping webhook envtest suite (run via `make test`)")
	}
	RegisterFailHandler(Fail)
	RunSpecs(t, "ApprovalRequest Webhook Envtest Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	By("bootstrapping test environment with the webhook installed")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
		WebhookInstallOptions: envtest.WebhookInstallOptions{
			Paths: []string{filepath.Join("..", "..", "..", "config", "webhook", "manifests.yaml")},
		},
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	scheme = runtime.NewScheme()
	Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())
	Expect(scrutineerv1alpha1.AddToScheme(scheme)).To(Succeed())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())

	testCtx, cancel = context.WithCancel(context.Background())

	webhookOpts := testEnv.WebhookInstallOptions
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
		LeaderElection:         false,
		WebhookServer: webhook.NewServer(webhook.Options{
			Host:    webhookOpts.LocalServingHost,
			Port:    webhookOpts.LocalServingPort,
			CertDir: webhookOpts.LocalServingCertDir,
		}),
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(SetupApprovalRequestWebhookWithManager(mgr)).To(Succeed())

	go func() {
		defer GinkgoRecover()
		Expect(mgr.Start(testCtx)).To(Succeed())
	}()

	By("waiting for the webhook server to accept TLS connections")
	addr := net.JoinHostPort(webhookOpts.LocalServingHost, fmt.Sprintf("%d", webhookOpts.LocalServingPort))
	Eventually(func() error {
		conn, derr := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", addr, &tls.Config{InsecureSkipVerify: true})
		if derr != nil {
			return derr
		}
		return conn.Close()
	}, 15*time.Second, 200*time.Millisecond).Should(Succeed())
})

var _ = AfterSuite(func() {
	if cancel != nil {
		cancel()
	}
	By("tearing down test environment")
	if testEnv != nil {
		Expect(testEnv.Stop()).To(Succeed())
	}
})

// clientAs returns a client authenticated as a freshly-provisioned user whose
// authenticated username equals name (the cert CN). This is how we simulate a
// real human approver versus the controller's own ServiceAccount. The user is
// granted cluster-admin so the test exercises the webhook (identity stamping),
// not RBAC on the ApprovalRequest (which is the deployment's separate concern).
func clientAs(name string, groups ...string) client.Client {
	GinkgoHelper()
	authUser, err := testEnv.AddUser(envtest.User{Name: name, Groups: groups}, nil)
	Expect(err).NotTo(HaveOccurred())

	binding := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "scrutineer-webhook-test-" + name},
		RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: "cluster-admin"},
		Subjects:   []rbacv1.Subject{{Kind: rbacv1.UserKind, Name: name, APIGroup: rbacv1.GroupName}},
	}
	err = k8sClient.Create(testCtx, binding)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		Expect(err).NotTo(HaveOccurred())
	}

	c, err := client.New(authUser.Config(), client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())
	return c
}
