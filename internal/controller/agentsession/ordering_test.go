/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package agentsession

import (
	"context"
	"errors"
	"fmt"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	scrutineerjob "github.com/grantbarry29/scrutineer/internal/controller/job"
	"github.com/grantbarry29/scrutineer/internal/enforcement/envoy"
	"github.com/grantbarry29/scrutineer/internal/enforcement/networkpolicy"
)

// Ordering invariant (#143/#145): every enforcement object — the routing-lock and
// backstop NetworkPolicies and the egress-proxy resources — must be created BEFORE the
// session's runtime object. Correct ordering is also the crash-window guarantee: a
// reconcile that dies between the two steps leaves the safe state (lock without
// runtime), never a runtime without its lock.
//
// These tests run whole Reconcile passes against a fake client whose interceptor
// records every Create in sequence. A fake client (not envtest) is deliberate: the
// in-package envtest suite runs a live manager whose background reconciles would race
// direct invocations, and the property under test is the reconciler's own operation
// order, which the interceptor captures exactly.

// opKey is how recorded operations and expectations name an object.
func opKey(obj client.Object) string {
	return fmt.Sprintf("%T:%s", obj, obj.GetName())
}

// orderingHarness is a reconciler wired to an order-recording fake client.
type orderingHarness struct {
	reconciler *AgentSessionReconciler
	client     client.WithWatch
	creates    *[]string
}

// newOrderingHarness builds the harness. failJobCreate injects a Create error for the
// runtime Job (the partial-failure probe); the failure is still recorded.
func newOrderingHarness(t *testing.T, failJobCreate bool, objs ...client.Object) *orderingHarness {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("add client-go scheme: %v", err)
	}
	if err := scrutineerv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add scrutineer scheme: %v", err)
	}

	creates := &[]string{}
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&scrutineerv1alpha1.AgentSession{}).
		WithObjects(objs...).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				*creates = append(*creates, opKey(obj))
				if _, isJob := obj.(*batchv1.Job); isJob && failJobCreate {
					return apierrors.NewInternalError(errors.New("injected Job create failure"))
				}
				return c.Create(ctx, obj, opts...)
			},
		}).
		Build()

	return &orderingHarness{
		reconciler: &AgentSessionReconciler{Client: cl, APIReader: cl, Scheme: scheme},
		client:     cl,
		creates:    creates,
	}
}

// reconcileUntil runs Reconcile passes (finalizer attach takes the first) until done
// reports true, the pass returns an error, or the attempt budget runs out.
func (h *orderingHarness) reconcileUntil(t *testing.T, key client.ObjectKey, done func() bool) error {
	t.Helper()
	req := reconcile.Request{NamespacedName: key}
	for i := 0; i < 6; i++ {
		if _, err := h.reconciler.Reconcile(context.Background(), req); err != nil {
			return err
		}
		if done() {
			return nil
		}
	}
	t.Fatalf("condition not reached after 6 reconcile passes; creates so far: %v", *h.creates)
	return nil
}

// firstIndex returns the position of key's first Create, or -1.
func (h *orderingHarness) firstIndex(key string) int {
	for i, k := range *h.creates {
		if k == key {
			return i
		}
	}
	return -1
}

// expectBefore fails unless both keys were created and before comes first.
func (h *orderingHarness) expectBefore(t *testing.T, before, after string) {
	t.Helper()
	bi, ai := h.firstIndex(before), h.firstIndex(after)
	if bi < 0 || ai < 0 {
		t.Fatalf("expected both %q and %q to be created; creates: %v", before, after, *h.creates)
	}
	if bi > ai {
		t.Fatalf("%q (index %d) must be created before %q (index %d); creates: %v",
			before, bi, after, ai, *h.creates)
	}
}

func orderingSession(name string, opts ...func(*scrutineerv1alpha1.AgentSession)) *scrutineerv1alpha1.AgentSession {
	s := &scrutineerv1alpha1.AgentSession{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns", UID: "ordering-uid"},
		Spec: scrutineerv1alpha1.AgentSessionSpec{
			Task:  scrutineerv1alpha1.SessionTaskSpec{Description: "ordering test", Prompt: "noop"},
			Model: scrutineerv1alpha1.ModelSpec{Provider: "openai", Name: "gpt-4.1"},
			Runtime: scrutineerv1alpha1.RuntimeSpec{
				Orchestrator: OrchestratorKubernetesJob,
				Image:        "busybox:latest",
				Command:      []string{"sh", "-c", "echo ok"},
			},
		},
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

func enforcedCIDRPolicy(name string) *scrutineerv1alpha1.AgentPolicy {
	return &scrutineerv1alpha1.AgentPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns"},
		Spec: scrutineerv1alpha1.AgentPolicySpec{
			Mode:        scrutineerv1alpha1.PolicyModeEnforced,
			PolicyRules: scrutineerv1alpha1.PolicyRules{AllowedCIDRs: []string{"203.0.113.0/24"}},
		},
	}
}

func envoyRuntimeProfile(name string) *scrutineerv1alpha1.RuntimeProfile {
	enabled := true
	return &scrutineerv1alpha1.RuntimeProfile{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns"},
		Spec: scrutineerv1alpha1.RuntimeProfileSpec{
			Enforcement: []scrutineerv1alpha1.RuntimeProfileEnforcement{{
				Name:    "envoy",
				Type:    scrutineerjob.EnforcementTypeEnvoy,
				Enabled: &enabled,
			}},
		},
	}
}

// jobExists is the reconcileUntil terminator for the happy-path variants.
func (h *orderingHarness) jobExists(key client.ObjectKey) func() bool {
	return func() bool {
		var job batchv1.Job
		return h.client.Get(context.Background(), key, &job) == nil
	}
}

func TestReconcileCreatesLockBeforeRuntimeJob(t *testing.T) {
	session := orderingSession("lock-order", func(s *scrutineerv1alpha1.AgentSession) {
		s.Spec.PolicyRefs = []scrutineerv1alpha1.PolicyRef{{Kind: "AgentPolicy", Name: "cidr"}}
	})
	h := newOrderingHarness(t, false, session, enforcedCIDRPolicy("cidr"))

	jobKey := client.ObjectKey{Namespace: "ns", Name: scrutineerjob.NameFor(session)}
	if err := h.reconcileUntil(t, client.ObjectKeyFromObject(session), h.jobExists(jobKey)); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	lock := opKey(&networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{
		Name: networkpolicy.NameFor("ns", session.Name)}})
	job := opKey(&batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: scrutineerjob.NameFor(session)}})
	h.expectBefore(t, lock, job)
}

func TestReconcileCreatesEnforcementBeforeRuntimeJobWithEnvoy(t *testing.T) {
	session := orderingSession("envoy-order", func(s *scrutineerv1alpha1.AgentSession) {
		s.Spec.PolicyRefs = []scrutineerv1alpha1.PolicyRef{{Kind: "AgentPolicy", Name: "cidr"}}
		s.Spec.RuntimeProfileRef = &scrutineerv1alpha1.RuntimeProfileRef{Name: "envoy-prof"}
	})
	h := newOrderingHarness(t, false, session, enforcedCIDRPolicy("cidr"), envoyRuntimeProfile("envoy-prof"))

	jobKey := client.ObjectKey{Namespace: "ns", Name: scrutineerjob.NameFor(session)}
	if err := h.reconcileUntil(t, client.ObjectKeyFromObject(session), h.jobExists(jobKey)); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	job := opKey(&batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: scrutineerjob.NameFor(session)}})
	lock := opKey(&networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{
		Name: networkpolicy.NameFor("ns", session.Name)}})
	backstop := opKey(&networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{
		Name: networkpolicy.BackstopNameFor("ns", session.Name)}})
	proxyPod := opKey(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: envoy.ResourceName(session.Name)}})

	h.expectBefore(t, lock, job)
	h.expectBefore(t, backstop, job)
	h.expectBefore(t, proxyPod, job)
}

// The crash-window guarantee: if the reconcile dies at Job creation, the state left
// behind must be the safe one — locks present, no runtime — never the reverse.
func TestReconcileJobCreateFailureLeavesLockNotRuntime(t *testing.T) {
	session := orderingSession("crash-window", func(s *scrutineerv1alpha1.AgentSession) {
		s.Spec.PolicyRefs = []scrutineerv1alpha1.PolicyRef{{Kind: "AgentPolicy", Name: "cidr"}}
	})
	h := newOrderingHarness(t, true, session, enforcedCIDRPolicy("cidr"))

	jobKey := client.ObjectKey{Namespace: "ns", Name: scrutineerjob.NameFor(session)}
	err := h.reconcileUntil(t, client.ObjectKeyFromObject(session), h.jobExists(jobKey))
	if err == nil {
		t.Fatalf("expected the injected Job create failure to surface; creates: %v", *h.creates)
	}

	ctx := context.Background()
	var np networkingv1.NetworkPolicy
	lockKey := client.ObjectKey{Namespace: "ns", Name: networkpolicy.NameFor("ns", session.Name)}
	if getErr := h.client.Get(ctx, lockKey, &np); getErr != nil {
		t.Fatalf("routing lock must already exist when Job creation fails (safe state): %v", getErr)
	}
	var job batchv1.Job
	if getErr := h.client.Get(ctx, jobKey, &job); !apierrors.IsNotFound(getErr) {
		t.Fatalf("runtime Job must not exist after the injected failure, got err=%v", getErr)
	}
}
