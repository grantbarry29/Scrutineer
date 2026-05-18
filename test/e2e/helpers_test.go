//go:build e2e

/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package e2e

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	"sigs.k8s.io/controller-runtime/pkg/client"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
)

// strPtr is a tiny helper since the API has *string fields.
func strPtr(s string) *string { return &s }

// newTestNamespace creates a uniquely-named namespace for one It block and
// registers a cleanup that deletes it. The DeferCleanup runs in reverse-order
// per Ginkgo semantics, after the body of the It completes.
func newTestNamespace(prefix string) string {
	name := fmt.Sprintf("%s-%s", prefix, rand.String(5))
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	Expect(k8sClient.Create(ctx, ns)).To(Succeed())

	DeferCleanup(func(ctx SpecContext) {
		// Best-effort delete; if it fails the user can clean up by hand.
		_ = k8sClient.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}})
	}, NodeTimeout(60*time.Second))

	return name
}

// agentSessionOption mutates an AgentSession during construction. Each test
// composes the precise spec it wants from these.
type agentSessionOption func(*relayv1alpha1.AgentSession)

func withTemperature(t string) agentSessionOption {
	return func(s *relayv1alpha1.AgentSession) { s.Spec.Model.Temperature = strPtr(t) }
}

func withCommand(cmd ...string) agentSessionOption {
	return func(s *relayv1alpha1.AgentSession) { s.Spec.Runtime.Command = cmd }
}

func withTaskPrompt(p string) agentSessionOption {
	return func(s *relayv1alpha1.AgentSession) { s.Spec.Task.Prompt = p }
}

func withImage(img string) agentSessionOption {
	return func(s *relayv1alpha1.AgentSession) { s.Spec.Runtime.Image = img }
}

func withoutTask() agentSessionOption {
	return func(s *relayv1alpha1.AgentSession) {
		s.Spec.Task = relayv1alpha1.SessionTaskSpec{}
	}
}

// newAgentSession builds a baseline AgentSession (valid by default) and applies
// any modifications via opts. The default runtime exits 0 quickly so the
// happy-path test is fast.
func newAgentSession(namespace, name string, opts ...agentSessionOption) *relayv1alpha1.AgentSession {
	s := &relayv1alpha1.AgentSession{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: relayv1alpha1.AgentSessionSpec{
			Task: relayv1alpha1.SessionTaskSpec{
				Description: "e2e test session",
				Prompt:      "noop",
			},
			Model: relayv1alpha1.ModelSpec{
				Provider: "openai",
				Name:     "gpt-4.1",
			},
			Runtime: relayv1alpha1.RuntimeSpec{
				Orchestrator: "kubernetes-job",
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

// waitForPhase polls until the AgentSession reaches one of the expected phases
// (or the timeout elapses). On timeout it dumps the current AgentSession so
// failures are debuggable from CI logs.
func waitForPhase(ctx context.Context, key client.ObjectKey, want []relayv1alpha1.AgentSessionPhase, timeout, poll time.Duration) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		var got relayv1alpha1.AgentSession
		g.Expect(k8sClient.Get(ctx, key, &got)).To(Succeed())
		for _, p := range want {
			if got.Status.Phase == p {
				return
			}
		}
		g.Expect(got.Status.Phase).To(BeElementOf(want),
			"AgentSession %s currently has phase %q (want one of %v); full status: %+v",
			key, got.Status.Phase, want, got.Status)
	}, timeout, poll).Should(Succeed())
}

// expectJobExists waits until a Job with the deterministic name exists in the
// given namespace, and returns it.
func expectJobExists(ctx context.Context, ns, jobName string) *batchv1.Job {
	GinkgoHelper()
	var job batchv1.Job
	Eventually(func() error {
		return k8sClient.Get(ctx, client.ObjectKey{Namespace: ns, Name: jobName}, &job)
	}, 30*time.Second, 500*time.Millisecond).Should(Succeed(),
		"Job %s/%s never appeared", ns, jobName)
	return &job
}

// expectNoJob asserts that the controller did NOT create a Job for a session.
// Used by the Denied path; we wait briefly to give a misbehaving controller a
// chance to fail loudly.
func expectNoJob(ctx context.Context, ns, jobName string) {
	GinkgoHelper()
	// Give the controller a fair window in which it COULD have created the Job.
	// If it never does within this window, we're satisfied.
	Consistently(func() bool {
		err := k8sClient.Get(ctx, client.ObjectKey{Namespace: ns, Name: jobName}, &batchv1.Job{})
		return apierrors.IsNotFound(err)
	}, 5*time.Second, 500*time.Millisecond).Should(BeTrue(),
		"Job %s/%s should never have been created for a Denied AgentSession", ns, jobName)
}

// getCondition fetches a named condition from the AgentSession's status.
// Returns nil if not present.
func getCondition(s *relayv1alpha1.AgentSession, condType string) *metav1.Condition {
	for i := range s.Status.Conditions {
		if s.Status.Conditions[i].Type == condType {
			return &s.Status.Conditions[i]
		}
	}
	return nil
}
