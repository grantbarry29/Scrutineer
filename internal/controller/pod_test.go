/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package controller

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("pod selection for status.podName", func() {
	var jobUID types.UID = "job-uid-123"

	It("podOwnedByJob matches Job owner reference by UID and kind", func() {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: batchv1.SchemeGroupVersion.String(),
					Kind:       "Job",
					UID:        jobUID,
				}},
			},
		}
		Expect(podOwnedByJob(pod, jobUID)).To(BeTrue())
		Expect(podOwnedByJob(pod, "other-uid")).To(BeFalse())
	})

	It("newestPodOwnedByJob picks the latest CreationTimestamp", func() {
		older := metav1.NewTime(time.Now().Add(-2 * time.Minute))
		newer := metav1.NewTime(time.Now().Add(-1 * time.Minute))

		pods := []corev1.Pod{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "pod-old",
					OwnerReferences: []metav1.OwnerReference{{
						Kind: "Job",
						UID:  jobUID,
					}},
					CreationTimestamp: older,
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "pod-new",
					OwnerReferences: []metav1.OwnerReference{{
						Kind: "Job",
						UID:  jobUID,
					}},
					CreationTimestamp: newer,
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "pod-other-job",
					OwnerReferences: []metav1.OwnerReference{{
						Kind: "Job",
						UID:  "other",
					}},
					CreationTimestamp: metav1.NewTime(time.Now()),
				},
			},
		}

		chosen := newestPodOwnedByJob(pods, jobUID)
		Expect(chosen).NotTo(BeNil())
		Expect(chosen.Name).To(Equal("pod-new"))
	})

	It("newestPodOwnedByJob returns nil when no Pods are owned by the Job", func() {
		pods := []corev1.Pod{{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "unrelated",
				Labels: map[string]string{LabelSessionRef: "session-a"},
			},
		}}
		Expect(newestPodOwnedByJob(pods, jobUID)).To(BeNil())
	})

	It("ignores labeled Pods owned by a different Job UID even when newer", func() {
		ts := metav1.NewTime(time.Now())
		pods := []corev1.Pod{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "pod-current-job",
					OwnerReferences: []metav1.OwnerReference{{
						Kind: "Job",
						UID:  jobUID,
					}},
					CreationTimestamp: metav1.NewTime(ts.Add(-time.Minute)),
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "pod-stale-job",
					Labels: map[string]string{LabelSessionRef: "session-a"},
					OwnerReferences: []metav1.OwnerReference{{
						Kind: "Job",
						UID:  "stale-job-uid",
					}},
					CreationTimestamp: ts,
				},
			},
		}
		chosen := newestPodOwnedByJob(pods, jobUID)
		Expect(chosen).NotTo(BeNil())
		Expect(chosen.Name).To(Equal("pod-current-job"))
	})

	It("breaks CreationTimestamp ties by lexicographic Pod name", func() {
		ts := metav1.NewTime(time.Now())
		jobRef := metav1.OwnerReference{Kind: "Job", UID: jobUID}
		pods := []corev1.Pod{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "pod-a",
					OwnerReferences:   []metav1.OwnerReference{jobRef},
					CreationTimestamp: ts,
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "pod-z",
					OwnerReferences:   []metav1.OwnerReference{jobRef},
					CreationTimestamp: ts,
				},
			},
		}
		chosen := newestPodOwnedByJob(pods, jobUID)
		Expect(chosen).NotTo(BeNil())
		Expect(chosen.Name).To(Equal("pod-z"))
	})
})
