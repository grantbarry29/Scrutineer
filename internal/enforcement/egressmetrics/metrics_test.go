/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package egressmetrics

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/enforcement/reporterclient"
)

func TestObserveDecision_countsByAction(t *testing.T) {
	m := New(nil)
	m.ObserveDecision(scrutineerv1alpha1.PolicyDecision{Action: scrutineerv1alpha1.PolicyDecisionAllow})
	m.ObserveDecision(scrutineerv1alpha1.PolicyDecision{Action: scrutineerv1alpha1.PolicyDecisionAllow})
	m.ObserveDecision(scrutineerv1alpha1.PolicyDecision{Action: scrutineerv1alpha1.PolicyDecisionDeny})

	if got := testutil.ToFloat64(m.Decisions.WithLabelValues("allow")); got != 2 {
		t.Fatalf("allow = %v, want 2", got)
	}
	if got := testutil.ToFloat64(m.Decisions.WithLabelValues("deny")); got != 1 {
		t.Fatalf("deny = %v, want 1", got)
	}
}

func TestWrapSubmit_outcomesAndLatency(t *testing.T) {
	m := New(nil)
	fail := true
	submit := m.WrapSubmit(func(context.Context, []scrutineerv1alpha1.PolicyDecision) error {
		if fail {
			return fmt.Errorf("reporter unavailable")
		}
		return nil
	})

	if err := submit(context.Background(), nil); err == nil {
		t.Fatal("expected wrapped error to propagate")
	}
	fail = false
	if err := submit(context.Background(), nil); err != nil {
		t.Fatal(err)
	}

	if got := testutil.ToFloat64(m.Submissions.WithLabelValues("error")); got != 1 {
		t.Fatalf("error outcome = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.Submissions.WithLabelValues("ok")); got != 1 {
		t.Fatalf("ok outcome = %v, want 1", got)
	}
	if got := testutil.CollectAndCount(m.SubmitSeconds); got != 1 {
		t.Fatalf("histogram series = %d, want 1", got)
	}
}

// #100: a 429 is flow control the tailer paces around, not a delivery failure —
// counting it as outcome="error" would page operators for healthy backpressure.
func TestWrapSubmit_countsRateLimitAsFlowControl(t *testing.T) {
	m := New(nil)
	submit := m.WrapSubmit(func(context.Context, []scrutineerv1alpha1.PolicyDecision) error {
		return &reporterclient.StatusError{StatusCode: http.StatusTooManyRequests}
	})

	if err := submit(context.Background(), nil); err == nil {
		t.Fatal("expected wrapped error to propagate")
	}
	if got := testutil.ToFloat64(m.Submissions.WithLabelValues("rate_limited")); got != 1 {
		t.Fatalf("rate_limited outcome = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.Submissions.WithLabelValues("error")); got != 0 {
		t.Fatalf("error outcome = %v, want 0 (429 is not a failure)", got)
	}
}

// The exported page carries the scrutineer_egress_reporter_* family, including the
// dropped-decisions CounterFunc fed by the Tailer's existing counter.
func TestHandler_exportsFamilies(t *testing.T) {
	droppedVal := 7.0
	m := New(func() float64 { return droppedVal })
	m.Malformed.Inc()
	m.ObserveDecision(scrutineerv1alpha1.PolicyDecision{Action: scrutineerv1alpha1.PolicyDecisionDryRun})

	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body, _ := io.ReadAll(rec.Result().Body)

	for _, want := range []string{
		`scrutineer_egress_reporter_decisions_total{action="dry-run"} 1`,
		"scrutineer_egress_reporter_malformed_lines_total 1",
		"scrutineer_egress_reporter_dropped_decisions_total 7",
	} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("metrics page missing %q:\n%s", want, body)
		}
	}
}
