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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/secureai/relay/internal/controller/job"
)

var _ = Describe("pod watch mapping", func() {
	It("enqueues the labeled session for Job-owned Pods", func() {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "test-ns",
				Labels: map[string]string{
					job.LabelSessionRef: "session-a",
				},
				OwnerReferences: []metav1.OwnerReference{{
					Kind: "Job",
					Name: "relay-session-session-a",
				}},
			},
		}

		reqs := testReconciler().mapPodToSessions(context.Background(), pod)
		Expect(reqs).To(HaveLen(1))
		Expect(reqs[0].NamespacedName.Namespace).To(Equal("test-ns"))
		Expect(reqs[0].NamespacedName.Name).To(Equal("session-a"))
	})

	It("ignores Pods without a session label", func() {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "test-ns",
				OwnerReferences: []metav1.OwnerReference{{
					Kind: "Job",
					Name: "relay-session-session-a",
				}},
			},
		}

		reqs := testReconciler().mapPodToSessions(context.Background(), pod)
		Expect(reqs).To(BeEmpty())
	})

	It("ignores labeled Pods that are not Job-owned", func() {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "test-ns",
				Labels: map[string]string{
					job.LabelSessionRef: "session-a",
				},
				OwnerReferences: []metav1.OwnerReference{{
					Kind: "ReplicaSet",
					Name: "rs-a",
				}},
			},
		}

		reqs := testReconciler().mapPodToSessions(context.Background(), pod)
		Expect(reqs).To(BeEmpty())
	})
})
