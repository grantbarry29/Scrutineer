/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package metrics exposes Relay control-plane Prometheus metrics on the controller
// manager metrics endpoint (controller-runtime /metrics).
package metrics

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	namespace = "relay"
)

var (
	agentsByPhase = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "agentsessions",
		Help:      "Current AgentSession count by namespace and lifecycle phase.",
	}, []string{"namespace", "phase"})

	sessionViolations = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "agentsession_violations",
		Help:      "Total policy violations recorded on AgentSessions by namespace.",
	}, []string{"namespace"})

	approvalQueueDepth = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "approval_queue_depth",
		Help:      "ApprovalRequests awaiting a human decision (status.state Pending or unset).",
	})

	policyViolationsObserved = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "policy_violations_observed_total",
		Help:      "Novel policy violations appended to AgentSession status.",
	}, []string{"namespace", "type"})

	runtimeReportsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "runtime_reports_total",
		Help:      "Runtime evidence POST /v1/report outcomes.",
	}, []string{"result"})

	runtimeReportDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "runtime_report_duration_seconds",
		Help:      "Latency of POST /v1/report handling.",
		Buckets:   prometheus.DefBuckets,
	})
)

var (
	registerOnce sync.Once
	registerErr  error
)

// Register wires Relay metrics and the AgentSession collector into the controller-runtime registry.
func Register(collector *AgentSessionCollector) error {
	registerOnce.Do(func() {
		collectors := []prometheus.Collector{
			policyViolationsObserved,
			runtimeReportsTotal,
			runtimeReportDuration,
		}
		if collector != nil {
			collectors = append(collectors, collector)
		}
		for _, c := range collectors {
			if err := crmetrics.Registry.Register(c); err != nil {
				if _, ok := err.(prometheus.AlreadyRegisteredError); ok {
					continue
				}
				registerErr = err
				return
			}
		}
	})
	return registerErr
}

// ObserveNovelViolations increments counters for newly appended violations.
func ObserveNovelViolations(sessionNamespace string, violationTypes []string) {
	_ = Register(nil)
	for _, vType := range violationTypes {
		if vType == "" {
			vType = "unknown"
		}
		policyViolationsObserved.WithLabelValues(sessionNamespace, vType).Inc()
	}
}

// ObserveRuntimeReport records reporter handler outcome and latency.
func ObserveRuntimeReport(result string, duration time.Duration) {
	_ = Register(nil)
	if result == "" {
		result = "unknown"
	}
	runtimeReportsTotal.WithLabelValues(result).Inc()
	runtimeReportDuration.Observe(duration.Seconds())
}
