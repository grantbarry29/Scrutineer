/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package job

import (
	"fmt"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
)

// DescribePhase produces a short human-readable phase string from a Job's status.
func DescribePhase(j *batchv1.Job) string {
	if j == nil {
		return "unknown"
	}
	switch {
	case j.Status.Succeeded > 0:
		return "succeeded"
	case j.Status.Failed > 0:
		return fmt.Sprintf("failed (%d retries)", j.Status.Failed)
	case j.Status.Active > 0:
		return "running"
	default:
		return "pending"
	}
}

// TimedOut reports whether the Job failed due to activeDeadlineSeconds.
func TimedOut(j *batchv1.Job) bool {
	if j == nil {
		return false
	}
	for _, c := range j.Status.Conditions {
		if c.Status != corev1.ConditionTrue {
			continue
		}
		if c.Reason != "DeadlineExceeded" {
			continue
		}
		if c.Type == batchv1.JobFailed || c.Type == batchv1.JobFailureTarget {
			return true
		}
	}
	return false
}

// BackoffExhausted reports whether the Job has exhausted its backoff limit.
func BackoffExhausted(j *batchv1.Job) bool {
	if j == nil || j.Spec.BackoffLimit == nil {
		return false
	}
	return j.Status.Failed > 0 && j.Status.Failed > *j.Spec.BackoffLimit
}
