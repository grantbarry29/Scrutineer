//go:build e2e

/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package e2e runs the Scrutineer controller against a real kind cluster (whatever
// ~/.kube/config currently points at) and exercises end-to-end behavior of the
// AgentSession CRD, its admission validation, and the controller's reconciliation.
//
// Test layout (same package, build tag `e2e`):
//   - suite_test.go     — cluster preflight, in-process manager
//   - fixtures_test.go  — namespaces, session builders, options
//   - assertions_test.go — phase/job/condition wait helpers
//   - agentsession_test.go — Ginkgo specs only
//   - network_violation_test.go — live dns-proxy → reporter → status.violations
//   - tool_violation_test.go — live tool-gateway → reporter → status.violations
//   - egress_proxy_test.go — live per-session Envoy egress proxy: agent egress
//     traverses Envoy (access log, incl. CONNECT) + teardown
//
// Run with:   make test-e2e
// Skipped by: `go test ./...` (build tag `e2e`).
//
// Preconditions enforced by the suite:
//   - A reachable Kubernetes cluster (via current kubeconfig).
//   - The AgentSession CRD installed (run `make install` or `make dev-up` first).
//   - No other scrutineer-controller-manager running against the same cluster
//     (the suite starts its own in-process manager).
package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/controller/agentsession"
)

// Suite-wide handles set up in BeforeSuite and used by every It block.
var (
	cfg       *rest.Config
	k8sClient client.Client
	scheme    = runtime.NewScheme()

	// managerCancel stops the in-process controller manager goroutine.
	managerCancel context.CancelFunc
	managerDone   chan struct{}
)

// Default deadlines used by helpers. Generous enough for kind on a laptop;
// short enough that a regression is obvious.
const (
	terminalPhaseTimeout = 90 * time.Second
	terminalPhasePoll    = 2 * time.Second
	deniedPhaseTimeout   = 20 * time.Second
	deniedPhasePoll      = 500 * time.Millisecond
)

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Scrutineer e2e Suite")
}

var _ = BeforeSuite(func() {
	ctrl.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	By("registering core + scrutineer types on the scheme")
	Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())
	Expect(appsv1.AddToScheme(scheme)).To(Succeed())
	Expect(rbacv1.AddToScheme(scheme)).To(Succeed())
	Expect(scrutineerv1alpha1.AddToScheme(scheme)).To(Succeed())

	By("loading kubeconfig from the current environment")
	var err error
	cfg, err = config.GetConfig()
	Expect(err).NotTo(HaveOccurred(),
		"could not load a kubeconfig — is the dev container attached to kind? "+
			"(run 'make dev-up' first)")

	By("constructing a direct client")
	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())

	By("verifying the AgentSession CRD is installed")
	verifyAgentSessionCRDInstalled()

	By("starting the controller manager in-process")
	startControllerManager()
})

var _ = AfterSuite(func() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cleanupInClusterReporter(ctx)

	if managerCancel != nil {
		By("stopping the controller manager")
		managerCancel()
		select {
		case <-managerDone:
		case <-time.After(10 * time.Second):
			Fail("controller manager did not stop within 10s after cancel")
		}
	}
})

// verifyAgentSessionCRDInstalled fails the suite fast with a clear message if
// the CRD is missing, since otherwise every It block would fail with a confusing
// "no kind 'AgentSession' is registered" error.
func verifyAgentSessionCRDInstalled() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var list scrutineerv1alpha1.AgentSessionList
	err := k8sClient.List(ctx, &list, client.InNamespace("default"), client.Limit(1))
	if err == nil {
		return
	}
	if apierrors.IsNotFound(err) {
		Fail("AgentSession CRD is not installed. Run 'make install' or 'make dev-up' first.")
	}
	// Other errors (network, RBAC, etc.) — surface with full context.
	Fail(fmt.Sprintf("failed to list AgentSessions during suite preflight: %v", err))
}

// startControllerManager spins up an in-process controller-runtime manager that
// reconciles AgentSessions against the live cluster. We disable leader election,
// disable the metrics server, and disable health probes so this can coexist with
// nothing-special running on standard ports.
func startControllerManager() {
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
		LeaderElection:         false,
	})
	Expect(err).NotTo(HaveOccurred())

	Expect((&agentsession.AgentSessionReconciler{
		Client:    mgr.GetClient(),
		APIReader: mgr.GetAPIReader(),
		Scheme:    mgr.GetScheme(),
		Recorder:  mgr.GetEventRecorderFor("scrutineer-e2e"),
	}).SetupWithManager(mgr)).To(Succeed())

	ctx, cancel := context.WithCancel(context.Background())
	managerCancel = cancel
	managerDone = make(chan struct{})

	go func() {
		defer GinkgoRecover()
		defer close(managerDone)
		if err := mgr.Start(ctx); err != nil && ctx.Err() == nil {
			Fail(fmt.Sprintf("controller manager terminated unexpectedly: %v", err))
		}
	}()

	By("waiting for manager caches to sync")
	Eventually(func() bool {
		// A direct LIST against the cluster confirms the apiserver is reachable
		// even if the manager hasn't fully synced caches yet — the manager's
		// cached client below would block on cache sync.
		var ns metav1.PartialObjectMetadataList
		ns.SetGroupVersionKind(metav1.SchemeGroupVersion.WithKind("NamespaceList"))
		return mgr.GetCache().WaitForCacheSync(ctx)
	}, 30*time.Second, 500*time.Millisecond).Should(BeTrue(),
		"manager caches did not sync within 30s")
}
