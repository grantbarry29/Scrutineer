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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/controller/agentsession"
	scrutineerjob "github.com/grantbarry29/scrutineer/internal/controller/job"
)

var _ = Describe("kubernetes-pod backend e2e against kind", func() {

	It("runs a session as a bare Pod (no Job) and reaches Succeeded", func(ctx SpecContext) {
		ns := newTestNamespace("scrutineer-e2e-pod")
		session := newAgentSession(ns, "pod-happy",
			withOrchestrator(agentsession.OrchestratorKubernetesPod),
			withCommand("sh", "-c", "echo running; exit 0"),
		)
		key := createAgentSession(ctx, session)

		waitForTerminalPhase(ctx, key, scrutineerv1alpha1.PhaseSucceeded)

		got := getSession(ctx, key)

		// A pod-backed session must not create a Job for the session.
		expectNoJobForSession(ctx, ns, session)

		// Backend-neutral runtime identity points at the Pod, and podName is set.
		Expect(got.Status.RuntimeRef).NotTo(BeNil())
		Expect(got.Status.RuntimeRef.Kind).To(Equal("Pod"))
		Expect(got.Status.RuntimeRef.Name).To(Equal(jobNameForSession(session)))
		Expect(got.Status.PodName).To(Equal(jobNameForSession(session)))

		// The agent Pod is owned by the session (controller ref) and labeled for it.
		var pod corev1.Pod
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: ns, Name: got.Status.PodName}, &pod)).To(Succeed())
		Expect(pod.Labels[scrutineerjob.LabelSessionRef]).To(Equal(session.Name))
		owner := metav1.GetControllerOf(&pod)
		Expect(owner).NotTo(BeNil())
		Expect(owner.Kind).To(Equal("AgentSession"))
		Expect(owner.UID).To(Equal(got.UID))

		Expect(got.Status.StartTime).NotTo(BeNil())
		Expect(got.Status.CompletionTime).NotTo(BeNil())

		expectCondition(&got, agentsession.ConditionValidated, metav1.ConditionTrue, "SpecValid")
		expectCondition(&got, agentsession.ConditionRuntimeCreated, metav1.ConditionTrue, "")
	})
})
