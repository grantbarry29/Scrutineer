/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package job

import (
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
)

func TestTimedOut(t *testing.T) {
	job := &batchv1.Job{
		Status: batchv1.JobStatus{
			Conditions: []batchv1.JobCondition{{
				Type:   batchv1.JobFailed,
				Status: corev1.ConditionTrue,
				Reason: "DeadlineExceeded",
			}},
		},
	}
	if !TimedOut(job) {
		t.Fatal("expected DeadlineExceeded on JobFailed")
	}

	job = &batchv1.Job{
		Status: batchv1.JobStatus{
			Conditions: []batchv1.JobCondition{{
				Type:   batchv1.JobFailureTarget,
				Status: corev1.ConditionTrue,
				Reason: "DeadlineExceeded",
			}},
		},
	}
	if !TimedOut(job) {
		t.Fatal("expected DeadlineExceeded on JobFailureTarget")
	}

	job = &batchv1.Job{
		Status: batchv1.JobStatus{
			Conditions: []batchv1.JobCondition{{
				Type:   batchv1.JobFailed,
				Status: corev1.ConditionTrue,
				Reason: "BackoffLimitExceeded",
			}},
		},
	}
	if TimedOut(job) {
		t.Fatal("expected false for BackoffLimitExceeded")
	}
}

func TestDescribePhase(t *testing.T) {
	if DescribePhase(nil) != "unknown" {
		t.Fatal("nil job")
	}
	if DescribePhase(&batchv1.Job{Status: batchv1.JobStatus{Succeeded: 1}}) != "succeeded" {
		t.Fatal("succeeded")
	}
	if DescribePhase(&batchv1.Job{Status: batchv1.JobStatus{Failed: 2}}) != "failed (2 retries)" {
		t.Fatal("failed")
	}
	if DescribePhase(&batchv1.Job{Status: batchv1.JobStatus{Active: 1}}) != "running" {
		t.Fatal("running")
	}
	if DescribePhase(&batchv1.Job{}) != "pending" {
		t.Fatal("pending")
	}
}

func TestBackoffExhausted(t *testing.T) {
	limit := int32(2)
	if BackoffExhausted(nil) || BackoffExhausted(&batchv1.Job{}) {
		t.Fatal("nil or missing backoff limit")
	}
	job := &batchv1.Job{
		Spec:   batchv1.JobSpec{BackoffLimit: &limit},
		Status: batchv1.JobStatus{Failed: 1},
	}
	if BackoffExhausted(job) {
		t.Fatal("failed count within limit")
	}
	job.Status.Failed = 3
	if !BackoffExhausted(job) {
		t.Fatal("expected backoff exhausted")
	}
}
