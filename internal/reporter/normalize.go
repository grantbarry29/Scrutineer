/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package reporter

import (
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
	"github.com/secureai/relay/internal/enforcement"
)

const maxFutureSkew = 2 * time.Minute

// ValidateAndNormalizeReport validates the wire payload and returns a RuntimeReport.
func ValidateAndNormalizeReport(req ReportRequest, receivedAt time.Time, effectiveMode relayv1alpha1.PolicyMode) (enforcement.RuntimeReport, error) {
	if strings.TrimSpace(req.Session.Namespace) == "" || strings.TrimSpace(req.Session.Name) == "" {
		return enforcement.RuntimeReport{}, fmt.Errorf("%w: session namespace and name are required", ErrBadRequest)
	}
	if strings.TrimSpace(req.Backend) == "" {
		return enforcement.RuntimeReport{}, fmt.Errorf("%w: backend is required", ErrBadRequest)
	}
	if len(req.Decisions) > MaxDecisionsPerReport {
		return enforcement.RuntimeReport{}, fmt.Errorf("%w: decisions exceed max %d", ErrBadRequest, MaxDecisionsPerReport)
	}
	if len(req.Events) > MaxEventsPerReport {
		return enforcement.RuntimeReport{}, fmt.Errorf("%w: events exceed max %d", ErrBadRequest, MaxEventsPerReport)
	}
	if len(req.Decisions) == 0 && len(req.Violations) == 0 && len(req.Events) == 0 && req.Usage == nil {
		return enforcement.RuntimeReport{}, fmt.Errorf("%w: decisions, violations, events, or usage required", ErrBadRequest)
	}
	if req.Usage != nil {
		if err := validateUsageDelta(req.Usage); err != nil {
			return enforcement.RuntimeReport{}, err
		}
	}

	// Pin every timestamp to RFC3339 (second) precision. The apiserver persists
	// metav1.Time at second precision, so retaining sub-second precision in memory
	// would make re-delivered reports look novel (key mismatch) and break the
	// idempotent dedup in AppendRuntime* helpers.
	now := metav1.NewTime(receivedAt).Rfc3339Copy()
	maxFuture := receivedAt.Add(maxFutureSkew)
	decisions := make([]relayv1alpha1.PolicyDecision, 0, len(req.Decisions))
	for i, d := range req.Decisions {
		if d.Phase != "" && d.Phase != relayv1alpha1.PolicyDecisionPhaseRuntime {
			return enforcement.RuntimeReport{}, fmt.Errorf("%w: decisions[%d].phase must be runtime", ErrBadRequest, i)
		}
		d.Phase = relayv1alpha1.PolicyDecisionPhaseRuntime
		if d.Time.IsZero() || d.Time.Time.After(maxFuture) {
			d.Time = now
		} else {
			d.Time = d.Time.Rfc3339Copy()
		}
		if strings.TrimSpace(d.Actor) == "" {
			d.Actor = req.Backend
		}
		if effectiveMode != "" {
			d.Mode = effectiveMode
		}
		decisions = append(decisions, d)
	}

	violations := append([]relayv1alpha1.PolicyViolation(nil), req.Violations...)
	for i := range violations {
		if violations[i].Time.IsZero() || violations[i].Time.Time.After(maxFuture) {
			violations[i].Time = now
		} else {
			violations[i].Time = violations[i].Time.Rfc3339Copy()
		}
	}

	events := make([]relayv1alpha1.SessionEvent, 0, len(req.Events))
	for _, e := range req.Events {
		if strings.TrimSpace(string(e.Type)) == "" {
			return enforcement.RuntimeReport{}, fmt.Errorf("%w: event type is required", ErrBadRequest)
		}
		if e.Time.IsZero() || e.Time.Time.After(maxFuture) {
			e.Time = now
		} else {
			e.Time = e.Time.Rfc3339Copy()
		}
		if strings.TrimSpace(e.Source) == "" {
			e.Source = req.Backend
		}
		events = append(events, e)
	}

	var usage *relayv1alpha1.SessionUsage
	if req.Usage != nil {
		cp := *req.Usage
		usage = &cp
	}

	return enforcement.RuntimeReport{
		Decisions:  decisions,
		Violations: violations,
		Events:     events,
		Usage:      usage,
	}, nil
}

func validateUsageDelta(u *relayv1alpha1.SessionUsage) error {
	if u == nil {
		return nil
	}
	if u.InputTokens < 0 || u.OutputTokens < 0 || u.ToolCalls < 0 || u.NetworkRequests < 0 {
		return fmt.Errorf("%w: usage counters must be non-negative", ErrBadRequest)
	}
	if u.InputTokens == 0 && u.OutputTokens == 0 && u.ToolCalls == 0 && u.NetworkRequests == 0 {
		return fmt.Errorf("%w: usage delta must include a positive counter", ErrBadRequest)
	}
	return nil
}
