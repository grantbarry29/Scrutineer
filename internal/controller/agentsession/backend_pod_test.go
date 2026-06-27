/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package agentsession

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
	relayjob "github.com/secureai/relay/internal/controller/job"
	"github.com/secureai/relay/internal/policy"
)

func podTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatalf("add client-go scheme: %v", err)
	}
	if err := relayv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("add relay scheme: %v", err)
	}
	return s
}

func podTestSession() *relayv1alpha1.AgentSession {
	return &relayv1alpha1.AgentSession{
		ObjectMeta: metav1.ObjectMeta{Name: "s", Namespace: "ns", UID: "session-uid"},
		Spec: relayv1alpha1.AgentSessionSpec{
			Runtime: relayv1alpha1.RuntimeSpec{
				Orchestrator:       OrchestratorKubernetesPod,
				Image:              "busybox:latest",
				ServiceAccountName: "default",
			},
		},
	}
}

func policyWithDomains(domains ...string) *policy.Resolved {
	return &policy.Resolved{
		Mode:  relayv1alpha1.PolicyModeEnforced,
		Rules: relayv1alpha1.PolicyRules{AllowedDomains: domains},
	}
}

func agentEnvOf(pod *corev1.Pod) map[string]string {
	for _, c := range pod.Spec.Containers {
		if c.Name == relayjob.AgentContainerName {
			out := make(map[string]string, len(c.Env))
			for _, e := range c.Env {
				out[e.Name] = e.Value
			}
			return out
		}
	}
	return nil
}

func TestPodRuntimePhase(t *testing.T) {
	cases := []struct {
		name   string
		phase  corev1.PodPhase
		reason string
		want   runtimePhase
	}{
		{"succeeded", corev1.PodSucceeded, "", runtimeSucceeded},
		{"failed", corev1.PodFailed, "Error", runtimeFailed},
		{"timed-out", corev1.PodFailed, podDeadlineExceededReason, runtimeTimedOut},
		{"running", corev1.PodRunning, "", runtimeRunning},
		{"pending", corev1.PodPending, "", runtimeStarting},
		{"empty", "", "", runtimeStarting},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pod := &corev1.Pod{Status: corev1.PodStatus{Phase: tc.phase, Reason: tc.reason}}
			if got := podRuntimePhase(pod); got != tc.want {
				t.Fatalf("podRuntimePhase(%s/%s) = %q, want %q", tc.phase, tc.reason, got, tc.want)
			}
		})
	}
}

func TestPodReplaceableForSync(t *testing.T) {
	cases := []struct {
		phase corev1.PodPhase
		want  bool
	}{
		{"", true},
		{corev1.PodPending, true},
		{corev1.PodRunning, false},
		{corev1.PodSucceeded, false},
		{corev1.PodFailed, false},
	}
	for _, tc := range cases {
		pod := &corev1.Pod{Status: corev1.PodStatus{Phase: tc.phase}}
		if got := podReplaceableForSync(pod); got != tc.want {
			t.Fatalf("podReplaceableForSync(%q) = %v, want %v", tc.phase, got, tc.want)
		}
	}
	if podReplaceableForSync(nil) {
		t.Fatalf("podReplaceableForSync(nil) = true, want false")
	}
}

func TestPodBackendEnsureCreatesWhenAbsent(t *testing.T) {
	scheme := podTestScheme(t)
	session := podTestSession()
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()
	b := newKubernetesPodBackend(cl, nil, scheme)

	obs, err := b.ensure(context.Background(), session, nil, policyWithDomains("example.com"), nil)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if !obs.created {
		t.Fatalf("expected created=true on first ensure")
	}
	if !obs.policyInSync {
		t.Fatalf("expected policyInSync=true on create")
	}
	if obs.runtimeRef == nil || obs.runtimeRef.Kind != "Pod" {
		t.Fatalf("expected runtimeRef.kind=Pod, got %#v", obs.runtimeRef)
	}

	var pod corev1.Pod
	key := client.ObjectKey{Namespace: session.Namespace, Name: relayjob.NameFor(session)}
	if err := cl.Get(context.Background(), key, &pod); err != nil {
		t.Fatalf("get created pod: %v", err)
	}
	if pod.Spec.RestartPolicy != corev1.RestartPolicyNever {
		t.Fatalf("expected RestartPolicy=Never, got %q", pod.Spec.RestartPolicy)
	}
	if !metav1.IsControlledBy(&pod, session) {
		t.Fatalf("expected created pod to be controlled by the session")
	}
}

func TestPodBackendEnsureOwnershipConflict(t *testing.T) {
	scheme := podTestScheme(t)
	session := podTestSession()
	foreign := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: session.Namespace, Name: relayjob.NameFor(session)},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "x", Image: "busybox"}}},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(foreign).Build()
	b := newKubernetesPodBackend(cl, nil, scheme)

	_, err := b.ensure(context.Background(), session, nil, policyWithDomains("example.com"), nil)
	if !errors.Is(err, ErrJobNotOwned) {
		t.Fatalf("expected ErrJobNotOwned, got %v", err)
	}
}

func TestPodBackendEnsureReplacesPendingPodOnPolicyDrift(t *testing.T) {
	scheme := podTestScheme(t)
	session := podTestSession()
	b := newKubernetesPodBackend(nil, nil, scheme)

	existing := b.buildPod(session, nil, policyWithDomains("old.example.com"), nil)
	if err := controllerutil.SetControllerReference(session, existing, scheme); err != nil {
		t.Fatalf("set controller ref: %v", err)
	}
	existing.Status.Phase = corev1.PodPending

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing).Build()
	b.client = cl

	obs, err := b.ensure(context.Background(), session, nil, policyWithDomains("new.example.com"), nil)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if !obs.replaced {
		t.Fatalf("expected replaced=true on pending-pod policy drift")
	}
	if !obs.policyInSync {
		t.Fatalf("expected policyInSync=true after replace")
	}

	var got corev1.Pod
	key := client.ObjectKey{Namespace: session.Namespace, Name: relayjob.NameFor(session)}
	if err := cl.Get(context.Background(), key, &got); err != nil {
		t.Fatalf("get recreated pod: %v", err)
	}
	if dom := agentEnvOf(&got)[relayjob.EnvPolicyAllowedDomains]; dom != "new.example.com" {
		t.Fatalf("expected recreated pod to carry new policy env, got %q", dom)
	}
}

func TestPodBackendEnsureSurfacesDriftOnRunningPodWithoutReplace(t *testing.T) {
	scheme := podTestScheme(t)
	session := podTestSession()
	b := newKubernetesPodBackend(nil, nil, scheme)

	existing := b.buildPod(session, nil, policyWithDomains("old.example.com"), nil)
	if err := controllerutil.SetControllerReference(session, existing, scheme); err != nil {
		t.Fatalf("set controller ref: %v", err)
	}
	existing.Status.Phase = corev1.PodRunning

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing).Build()
	b.client = cl

	obs, err := b.ensure(context.Background(), session, nil, policyWithDomains("new.example.com"), nil)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if obs.replaced {
		t.Fatalf("expected replaced=false for a running pod")
	}
	if obs.policyInSync {
		t.Fatalf("expected policyInSync=false (drift surfaced) on a running pod")
	}
	if obs.policyMessage == "" {
		t.Fatalf("expected a drift message on a running pod")
	}
	if obs.phase != runtimeRunning {
		t.Fatalf("expected phase=running, got %q", obs.phase)
	}

	var got corev1.Pod
	key := client.ObjectKey{Namespace: session.Namespace, Name: relayjob.NameFor(session)}
	if err := cl.Get(context.Background(), key, &got); err != nil {
		t.Fatalf("get pod: %v", err)
	}
	if dom := agentEnvOf(&got)[relayjob.EnvPolicyAllowedDomains]; dom != "old.example.com" {
		t.Fatalf("expected running pod to keep its original env, got %q", dom)
	}
}

func TestPodBackendStopAndRuntimeGone(t *testing.T) {
	scheme := podTestScheme(t)
	session := podTestSession()
	b := newKubernetesPodBackend(nil, nil, scheme)

	existing := b.buildPod(session, nil, policyWithDomains("example.com"), nil)
	if err := controllerutil.SetControllerReference(session, existing, scheme); err != nil {
		t.Fatalf("set controller ref: %v", err)
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing).Build()
	b.client = cl
	ctx := context.Background()

	gone, err := b.runtimeGone(ctx, session)
	if err != nil {
		t.Fatalf("runtimeGone: %v", err)
	}
	if gone {
		t.Fatalf("expected runtimeGone=false while pod exists")
	}

	if err := b.stop(ctx, session); err != nil {
		t.Fatalf("stop: %v", err)
	}

	gone, err = b.runtimeGone(ctx, session)
	if err != nil {
		t.Fatalf("runtimeGone after stop: %v", err)
	}
	if !gone {
		t.Fatalf("expected runtimeGone=true after stop")
	}

	// stop is idempotent: a missing pod is treated as already stopped.
	if err := b.stop(ctx, session); err != nil {
		t.Fatalf("stop (absent) should be nil, got %v", err)
	}
}
