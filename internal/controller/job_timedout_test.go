/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
)

var _ = Describe("jobTimedOut", func() {
	It("detects JobFailed with DeadlineExceeded", func() {
		job := &batchv1.Job{
			Status: batchv1.JobStatus{
				Conditions: []batchv1.JobCondition{{
					Type:   batchv1.JobFailed,
					Status: corev1.ConditionTrue,
					Reason: "DeadlineExceeded",
				}},
			},
		}
		Expect(jobTimedOut(job)).To(BeTrue())
	})

	It("detects FailureTarget with DeadlineExceeded (Kubernetes 1.31+)", func() {
		job := &batchv1.Job{
			Status: batchv1.JobStatus{
				Conditions: []batchv1.JobCondition{{
					Type:   batchv1.JobFailureTarget,
					Status: corev1.ConditionTrue,
					Reason: "DeadlineExceeded",
				}},
			},
		}
		Expect(jobTimedOut(job)).To(BeTrue())
	})

	It("returns false for a generic JobFailed", func() {
		job := &batchv1.Job{
			Status: batchv1.JobStatus{
				Conditions: []batchv1.JobCondition{{
					Type:   batchv1.JobFailed,
					Status: corev1.ConditionTrue,
					Reason: "BackoffLimitExceeded",
				}},
			},
		}
		Expect(jobTimedOut(job)).To(BeFalse())
	})

	It("returns false when no deadline condition is present", func() {
		job := &batchv1.Job{Status: batchv1.JobStatus{Failed: 1}}
		Expect(jobTimedOut(job)).To(BeFalse())
	})
})
