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
	if len(req.Decisions) == 0 && len(req.Violations) == 0 && len(req.Events) == 0 {
		return enforcement.RuntimeReport{}, fmt.Errorf("%w: decisions, violations, or events required", ErrBadRequest)
	}

	now := metav1.NewTime(receivedAt)
	maxFuture := receivedAt.Add(maxFutureSkew)
	decisions := make([]relayv1alpha1.PolicyDecision, 0, len(req.Decisions))
	for i, d := range req.Decisions {
		if d.Phase != "" && d.Phase != relayv1alpha1.PolicyDecisionPhaseRuntime {
			return enforcement.RuntimeReport{}, fmt.Errorf("%w: decisions[%d].phase must be runtime", ErrBadRequest, i)
		}
		d.Phase = relayv1alpha1.PolicyDecisionPhaseRuntime
		if d.Time.IsZero() {
			d.Time = now
		} else if d.Time.Time.After(maxFuture) {
			d.Time = now
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
		if violations[i].Time.IsZero() {
			violations[i].Time = now
		} else if violations[i].Time.Time.After(maxFuture) {
			violations[i].Time = now
		}
	}

	events := make([]relayv1alpha1.SessionEvent, 0, len(req.Events))
	for _, e := range req.Events {
		if strings.TrimSpace(string(e.Type)) == "" {
			return enforcement.RuntimeReport{}, fmt.Errorf("%w: event type is required", ErrBadRequest)
		}
		if e.Time.IsZero() {
			e.Time = now
		} else if e.Time.Time.After(maxFuture) {
			e.Time = now
		}
		if strings.TrimSpace(e.Source) == "" {
			e.Source = req.Backend
		}
		events = append(events, e)
	}

	return enforcement.RuntimeReport{
		Decisions:  decisions,
		Violations: violations,
		Events:     events,
	}, nil
}
