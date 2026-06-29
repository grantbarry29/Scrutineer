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
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/enforcement"
)

const maxStatusPatchRetries = 5

// PatchRuntimePolicyReport merges runtime evidence into AgentSession status and updates
// the apiserver. It re-reads the live object, unions runtime decisions/violations with
// concurrent writers, and retries on optimistic-concurrency conflicts.
//
// The reader MUST be uncached. The retry loop below reads the live object to obtain a
// current resourceVersion for the optimistic-concurrency Update; a cached reader returns
// a stale version, so every Update would conflict and the loop would exhaust its retries
// on stale data. Callers (the reporter) pass mgr.GetAPIReader() — see the read-consistency
// policy on reporter.Options (#47).
func PatchRuntimePolicyReport(
	ctx context.Context,
	writer client.StatusWriter,
	reader client.Reader,
	sessionKey client.ObjectKey,
	report enforcement.RuntimeReport,
) error {
	var lastErr error
	for attempt := 0; attempt < maxStatusPatchRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * 20 * time.Millisecond):
			}
		}
		conflict, err := patchRuntimePolicyReportOnce(ctx, writer, reader, sessionKey, report)
		if err == nil {
			return nil
		}
		if conflict {
			lastErr = err
			continue
		}
		return err
	}
	if lastErr != nil {
		return fmt.Errorf("update AgentSession status after %d retries: %w", maxStatusPatchRetries, lastErr)
	}
	return fmt.Errorf("update AgentSession status: exhausted retries")
}

func patchRuntimePolicyReportOnce(
	ctx context.Context,
	writer client.StatusWriter,
	reader client.Reader,
	sessionKey client.ObjectKey,
	report enforcement.RuntimeReport,
) (conflict bool, err error) {
	var live scrutineerv1alpha1.AgentSession
	if err := reader.Get(ctx, sessionKey, &live); err != nil {
		if apierrors.IsNotFound(err) {
			return false, err
		}
		return false, fmt.Errorf("get AgentSession: %w", err)
	}

	original := live.DeepCopy()
	updated := live.DeepCopy()
	ApplyRuntimePolicyReport(updated, report)

	desired := updated.Status.DeepCopy()
	mergeStatusConditionsInPlace(&desired.Conditions, original.Status.Conditions)
	mergeRuntimePolicyDecisionsInPlace(&desired.PolicyDecisions, original.Status.PolicyDecisions)
	mergeViolationsInPlace(&desired.Violations, original.Status.Violations)
	mergeEventsInPlace(&desired.Events, original.Status.Events)
	mergeUsageInPlace(&desired.Usage, original.Status.Usage)

	var liveAgain scrutineerv1alpha1.AgentSession
	if err := reader.Get(ctx, sessionKey, &liveAgain); err != nil {
		return false, fmt.Errorf("re-read AgentSession: %w", err)
	}
	mergeStatusConditionsInPlace(&desired.Conditions, liveAgain.Status.Conditions)
	mergeRuntimePolicyDecisionsInPlace(&desired.PolicyDecisions, liveAgain.Status.PolicyDecisions)
	mergeViolationsInPlace(&desired.Violations, liveAgain.Status.Violations)
	mergeEventsInPlace(&desired.Events, liveAgain.Status.Events)
	mergeUsageInPlace(&desired.Usage, liveAgain.Status.Usage)

	if equalStatus(&liveAgain.Status, desired) {
		return false, nil
	}

	liveAgain.Status = *desired
	if err := writer.Update(ctx, &liveAgain); err != nil {
		if apierrors.IsConflict(err) {
			return true, err
		}
		return false, fmt.Errorf("update AgentSession status: %w", err)
	}
	return false, nil
}
