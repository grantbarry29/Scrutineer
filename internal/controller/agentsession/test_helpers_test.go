/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package agentsession

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/rand"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	scrutineerjob "github.com/grantbarry29/scrutineer/internal/controller/job"
	"github.com/grantbarry29/scrutineer/internal/enforcement/networkpolicy"
)

func jobNameFor(session *scrutineerv1alpha1.AgentSession) string {
	return scrutineerjob.NameFor(session)
}

func netpolNameFor(session *scrutineerv1alpha1.AgentSession) string {
	return networkpolicy.NameFor(session.Namespace, session.Name)
}

const (
	controllerPollInterval = 250 * time.Millisecond
	controllerWaitTimeout  = 15 * time.Second
)

func newTestNamespace() string {
	name := "scrutineer-ctrl-" + rand.String(5)
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	Expect(k8sClient.Create(testCtx, ns)).To(Succeed())
	DeferCleanup(func() {
		_ = k8sClient.Delete(testCtx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}})
	})
	return name
}

func minimalAgentSession(namespace, name string) *scrutineerv1alpha1.AgentSession {
	return &scrutineerv1alpha1.AgentSession{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: scrutineerv1alpha1.AgentSessionSpec{
			Task: scrutineerv1alpha1.SessionTaskSpec{
				Description: "envtest session",
				Prompt:      "run the task",
			},
			Model: scrutineerv1alpha1.ModelSpec{
				Provider: "openai",
				Name:     "gpt-4",
			},
			Runtime: scrutineerv1alpha1.RuntimeSpec{
				Orchestrator: OrchestratorKubernetesJob,
				Image:        "busybox:latest",
				Command:      []string{"true"},
			},
		},
	}
}

func waitForPhase(key types.NamespacedName, want scrutineerv1alpha1.AgentSessionPhase) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		var session scrutineerv1alpha1.AgentSession
		g.Expect(k8sClient.Get(testCtx, key, &session)).To(Succeed())
		g.Expect(session.Status.Phase).To(Equal(want))
	}, controllerWaitTimeout, controllerPollInterval).Should(Succeed())
}

func waitForFinalizer(key types.NamespacedName) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		var session scrutineerv1alpha1.AgentSession
		g.Expect(k8sClient.Get(testCtx, key, &session)).To(Succeed())
		g.Expect(controllerutil.ContainsFinalizer(&session, AgentSessionFinalizer)).To(BeTrue())
	}, controllerWaitTimeout, controllerPollInterval).Should(Succeed())
}

func waitForAgentSessionDeleted(key types.NamespacedName) {
	GinkgoHelper()
	Eventually(func() bool {
		var session scrutineerv1alpha1.AgentSession
		err := k8sClient.Get(testCtx, key, &session)
		return apierrors.IsNotFound(err)
	}, controllerWaitTimeout, controllerPollInterval).Should(BeTrue())
}

func waitForJob(ns string, session *scrutineerv1alpha1.AgentSession) {
	GinkgoHelper()
	jobKey := types.NamespacedName{Namespace: ns, Name: jobNameFor(session)}
	Eventually(func(g Gomega) {
		var job batchv1.Job
		g.Expect(k8sClient.Get(testCtx, jobKey, &job)).To(Succeed())
	}, controllerWaitTimeout, controllerPollInterval).Should(Succeed())
}

func jobAbsent(ns string, session *scrutineerv1alpha1.AgentSession) bool {
	var job batchv1.Job
	err := k8sClient.Get(testCtx, types.NamespacedName{Namespace: ns, Name: jobNameFor(session)}, &job)
	if apierrors.IsNotFound(err) {
		return true
	}
	return err == nil && !job.DeletionTimestamp.IsZero()
}

func expectJobAbsent(ns string, session *scrutineerv1alpha1.AgentSession) {
	GinkgoHelper()
	Expect(jobAbsent(ns, session)).To(BeTrue())
}

func getCondition(session *scrutineerv1alpha1.AgentSession, condType string) *metav1.Condition {
	for i := range session.Status.Conditions {
		if session.Status.Conditions[i].Type == condType {
			return &session.Status.Conditions[i]
		}
	}
	return nil
}

func expectCancelled(session *scrutineerv1alpha1.AgentSession) {
	GinkgoHelper()
	Expect(session.Status.Phase).To(Equal(scrutineerv1alpha1.PhaseCancelled))
	completed := getCondition(session, ConditionCompleted)
	Expect(completed).NotTo(BeNil())
	Expect(completed.Reason).To(Equal("SessionCancelled"))
}

func expectCancelledG(g Gomega, session *scrutineerv1alpha1.AgentSession) {
	g.Expect(session.Status.Phase).To(Equal(scrutineerv1alpha1.PhaseCancelled))
	completed := getCondition(session, ConditionCompleted)
	g.Expect(completed).NotTo(BeNil())
	g.Expect(completed.Reason).To(Equal("SessionCancelled"))
}

func setJobSucceeded(job *batchv1.Job) {
	job.Status.Succeeded = 1
	job.Status.Active = 0
	job.Status.Conditions = []batchv1.JobCondition{{
		Type:   batchv1.JobComplete,
		Status: corev1.ConditionTrue,
		Reason: "Completed",
	}}
	Expect(k8sClient.Status().Update(testCtx, job)).To(Succeed())
}

func envMap(vars []corev1.EnvVar) map[string]string {
	out := make(map[string]string, len(vars))
	for _, e := range vars {
		out[e.Name] = e.Value
	}
	return out
}
