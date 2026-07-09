/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package egressmetrics is the Prometheus instrumentation for the egress-reporter
// sidecar (#55). It owns a dedicated registry — NOT the client_golang default — so the
// shared enforcement packages stay Prometheus-free (the manager imports them; package-
// level registration there would export zero-valued egress series from the manager) and
// tests can assert increments in isolation. Wiring lives in cmd/egress-reporter: the
// Tailer's OnDecision/OnMalformed hooks, a wrapped Submit, and a CounterFunc over
// Tailer.Dropped.
package egressmetrics

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
)

const (
	namespace = "scrutineer"
	subsystem = "egress_reporter"
)

// Metrics holds the egress-reporter's instruments on a dedicated registry.
type Metrics struct {
	registry *prometheus.Registry

	// Decisions counts access-log entries parsed into evidence, by action.
	Decisions *prometheus.CounterVec
	// Malformed counts skipped unparseable access-log lines.
	Malformed prometheus.Counter
	// Submissions counts reporter Submit calls by outcome (ok|error).
	Submissions *prometheus.CounterVec
	// SubmitSeconds observes reporter Submit latency.
	SubmitSeconds prometheus.Histogram
	// Rejected counts decisions dropped after a permanent reporter rejection
	// (contract §4.4: 400/403/404/413), by HTTP status — evidence lost (#96).
	// Bounded cardinality: only those four codes are classified permanent.
	Rejected *prometheus.CounterVec
	// Rotations counts completed access-log rotation cycles (#98).
	Rotations prometheus.Counter
}

// New builds the instrument set. dropped, when non-nil, is exported as the
// dropped-decisions counter (evidence lost to pending-queue overflow — the value the
// Tailer already tracks; a CounterFunc avoids double bookkeeping).
func New(dropped func() float64) *Metrics {
	reg := prometheus.NewRegistry()
	m := &Metrics{
		registry: reg,
		Decisions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: subsystem, Name: "decisions_total",
			Help: "Access-log entries parsed into egress evidence, by decision action.",
		}, []string{"action"}),
		Malformed: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: subsystem, Name: "malformed_lines_total",
			Help: "Unparseable Envoy access-log lines skipped.",
		}),
		Submissions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: subsystem, Name: "submissions_total",
			Help: "Evidence batch submissions to the controller reporter, by outcome.",
		}, []string{"outcome"}),
		SubmitSeconds: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: namespace, Subsystem: subsystem, Name: "submission_duration_seconds",
			Help:    "Latency of evidence batch submissions to the controller reporter.",
			Buckets: prometheus.DefBuckets,
		}),
		Rejected: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: subsystem, Name: "rejected_decisions_total",
			Help: "Decisions dropped after a permanent reporter rejection (evidence lost), by HTTP status.",
		}, []string{"status"}),
		Rotations: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: subsystem, Name: "log_rotations_total",
			Help: "Completed access-log rotation cycles (ingested prefix removed, Envoy reopened).",
		}),
	}
	reg.MustRegister(m.Decisions, m.Malformed, m.Submissions, m.SubmitSeconds, m.Rejected, m.Rotations)
	if dropped != nil {
		reg.MustRegister(prometheus.NewCounterFunc(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: subsystem, Name: "dropped_decisions_total",
			Help: "Parsed decisions discarded because the pending queue overflowed (evidence lost to a prolonged reporter outage).",
		}, dropped))
	}
	return m
}

// ObserveDecision is the Tailer OnDecision hook.
func (m *Metrics) ObserveDecision(d scrutineerv1alpha1.PolicyDecision) {
	m.Decisions.WithLabelValues(string(d.Action)).Inc()
}

// ObserveRejected is the Tailer OnRejected hook.
func (m *Metrics) ObserveRejected(count, httpStatus int) {
	m.Rejected.WithLabelValues(strconv.Itoa(httpStatus)).Add(float64(count))
}

// WrapSubmit instruments a Tailer Submit func with outcome counts and latency.
func (m *Metrics) WrapSubmit(next func(context.Context, []scrutineerv1alpha1.PolicyDecision) error) func(context.Context, []scrutineerv1alpha1.PolicyDecision) error {
	return func(ctx context.Context, decisions []scrutineerv1alpha1.PolicyDecision) error {
		start := time.Now()
		err := next(ctx, decisions)
		m.SubmitSeconds.Observe(time.Since(start).Seconds())
		outcome := "ok"
		if err != nil {
			outcome = "error"
		}
		m.Submissions.WithLabelValues(outcome).Inc()
		return err
	}
}

// Handler serves the dedicated registry at /metrics semantics.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// Serve runs a /metrics HTTP server on addr until ctx is cancelled. Metrics are
// auxiliary telemetry: callers must treat a returned error as log-and-continue — the
// evidence pipeline must never die because a metrics bind failed.
func (m *Metrics) Serve(ctx context.Context, addr string) error {
	mux := http.NewServeMux()
	mux.Handle("/metrics", m.Handler())
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
