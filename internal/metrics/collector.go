/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package metrics

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/client"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
)

// AgentSessionCollector exposes aggregate AgentSession gauges on scrape.
type AgentSessionCollector struct {
	Client client.Reader
}

var _ prometheus.Collector = (*AgentSessionCollector)(nil)

// Describe implements prometheus.Collector.
func (c *AgentSessionCollector) Describe(ch chan<- *prometheus.Desc) {
	agentsByPhase.Describe(ch)
	sessionViolations.Describe(ch)
	approvalQueueDepth.Describe(ch)
}

// Collect implements prometheus.Collector.
func (c *AgentSessionCollector) Collect(ch chan<- prometheus.Metric) {
	if c == nil || c.Client == nil {
		agentsByPhase.Collect(ch)
		sessionViolations.Collect(ch)
		approvalQueueDepth.Collect(ch)
		return
	}

	phaseCounts := make(map[string]map[string]int)
	violationTotals := make(map[string]int)

	var list relayv1alpha1.AgentSessionList
	if err := c.Client.List(context.Background(), &list); err == nil {
		for _, session := range list.Items {
			phase := string(session.Status.Phase)
			if phase == "" {
				phase = "Unknown"
			}
			if phaseCounts[session.Namespace] == nil {
				phaseCounts[session.Namespace] = make(map[string]int)
			}
			phaseCounts[session.Namespace][phase]++

			violationTotals[session.Namespace] += len(session.Status.Violations)
		}
	}

	// Approval queue depth is the count of ApprovalRequests still awaiting a human
	// decision — the true Phase 5 gate, not the prior runtime ApprovalRequired proxy.
	var approvalQueue int
	var reqs relayv1alpha1.ApprovalRequestList
	if err := c.Client.List(context.Background(), &reqs); err == nil {
		for i := range reqs.Items {
			if isPendingApproval(reqs.Items[i].Status.State) {
				approvalQueue++
			}
		}
	}

	agentsByPhase.Reset()
	for ns, phases := range phaseCounts {
		for phase, count := range phases {
			agentsByPhase.WithLabelValues(ns, phase).Set(float64(count))
		}
	}

	sessionViolations.Reset()
	for ns, total := range violationTotals {
		sessionViolations.WithLabelValues(ns).Set(float64(total))
	}

	approvalQueueDepth.Set(float64(approvalQueue))

	agentsByPhase.Collect(ch)
	sessionViolations.Collect(ch)
	approvalQueueDepth.Collect(ch)
}

// isPendingApproval reports whether an ApprovalRequest is still awaiting a human
// decision. An empty state is the transient window between the controller
// creating the request and stamping it Pending, and still represents an open gate.
func isPendingApproval(state relayv1alpha1.ApprovalState) bool {
	return state == relayv1alpha1.ApprovalStatePending || state == ""
}
