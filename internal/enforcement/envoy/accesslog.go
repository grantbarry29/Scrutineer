/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package envoy

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/enforcement"
)

const (
	// AccessLogDir is the shared emptyDir mount where Envoy writes its JSON access log
	// and the egress-reporter container tails it (Slice C, #62).
	AccessLogDir = "/var/log/envoy"
	// AccessLogPath is the JSON-lines access log file inside AccessLogDir.
	AccessLogPath = AccessLogDir + "/access.json"

	// AccessLogActor identifies the egress proxy as the decision actor in evidence records.
	AccessLogActor = "envoy-egress-proxy"

	// accessLogTimeLayout matches Envoy's default %START_TIME% rendering.
	accessLogTimeLayout = "2006-01-02T15:04:05.000Z"
)

// AccessLogEntry is one line of the Envoy JSON access log, as configured by
// BootstrapYAML's json_format (keys here must stay in sync with it). Numeric operators
// render as JSON numbers or null, never "-" placeholders.
type AccessLogEntry struct {
	Method        string `json:"method"`
	Authority     string `json:"authority"`
	ResponseCode  int    `json:"response_code"`
	ResponseFlags string `json:"flags"`
	BytesSent     int64  `json:"bytes_sent"`
	BytesReceived int64  `json:"bytes_received"`
	DurationMS    int64  `json:"duration_ms"`
	StartTime     string `json:"start_time"`
}

// ParseAccessLogLine parses one JSON access-log line. Lines without an authority are
// rejected: they carry no egress target and would produce meaningless evidence.
func ParseAccessLogLine(line []byte) (AccessLogEntry, error) {
	var e AccessLogEntry
	if err := json.Unmarshal(line, &e); err != nil {
		return AccessLogEntry{}, fmt.Errorf("parse access log line: %w", err)
	}
	if strings.TrimSpace(e.Authority) == "" {
		return AccessLogEntry{}, fmt.Errorf("access log line has no authority")
	}
	return e, nil
}

// Evidence reason codes for egress decisions (match the dns-proxy's values so status
// filtering is consistent across both egress backends).
const (
	ReasonEgressObserved      = "EgressObserved"
	ReasonDeniedDomains       = "DeniedDomains"
	ReasonNotInAllowedDomains = "NotInAllowedDomains"
)

// EgressPolicy is the effective FQDN policy the egress-reporter classifies observed
// authorities against. It mirrors the Envoy RBAC (same enforcement.MatchDomain semantics),
// so evidence and enforcement always agree. Enforce reflects enforced mode: a would-be
// denial is recorded as Deny when enforcing (Envoy also blocked it) or DryRun in audit
// mode (Envoy let it through). An empty policy classifies everything as allow.
type EgressPolicy struct {
	Enforce        bool
	AllowedDomains []string
	DeniedDomains  []string
}

// classify returns the action + reason for an observed authority under this policy.
// Deny wins over allow-list, matching the RBAC filter order and the dns-proxy.
func (p EgressPolicy) classify(authority string) (scrutineerv1alpha1.PolicyDecisionAction, string) {
	if enforcement.MatchDomain(p.DeniedDomains, authority) {
		return p.deniedAction(), ReasonDeniedDomains
	}
	if len(p.AllowedDomains) > 0 && !enforcement.MatchDomain(p.AllowedDomains, authority) {
		return p.deniedAction(), ReasonNotInAllowedDomains
	}
	return scrutineerv1alpha1.PolicyDecisionAllow, ReasonEgressObserved
}

func (p EgressPolicy) deniedAction() scrutineerv1alpha1.PolicyDecisionAction {
	if p.Enforce {
		return scrutineerv1alpha1.PolicyDecisionDeny
	}
	return scrutineerv1alpha1.PolicyDecisionDryRun
}

// Decision converts an observed access-log entry into a runtime network decision,
// classified against the effective FQDN policy (#32). In enforced mode a denied authority
// was already blocked by the Envoy RBAC (the log shows a 403); in audit mode it flowed and
// is recorded as dry-run. AssuranceLevel is deliberately left empty: the data plane never
// self-attests assurance; the reporter stamps it server-side from the caller's identity
// (observed only for the Envoy pod's identity — see internal/reporter).
func (e AccessLogEntry) Decision(policy EgressPolicy) scrutineerv1alpha1.PolicyDecision {
	var ts metav1.Time
	if t, err := time.Parse(accessLogTimeLayout, e.StartTime); err == nil {
		ts = metav1.NewTime(t)
	} else if t, err := time.Parse(time.RFC3339Nano, e.StartTime); err == nil {
		ts = metav1.NewTime(t)
	}

	action, reason := policy.classify(e.Authority)
	return scrutineerv1alpha1.PolicyDecision{
		Time:   ts,
		Phase:  scrutineerv1alpha1.PolicyDecisionPhaseRuntime,
		Type:   "network",
		Action: action,
		Actor:  AccessLogActor,
		Target: e.Authority,
		Reason: reason,
		Message: fmt.Sprintf("egress %s %s -> %d (%s) tx=%dB rx=%dB %dms",
			e.Method, e.Authority, e.ResponseCode, e.ResponseFlags, e.BytesSent, e.BytesReceived, e.DurationMS),
	}
}
