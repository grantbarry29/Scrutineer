/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package audit

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/log/logtest"
)

func TestSetup_noopWhenEndpointEmpty(t *testing.T) {
	t.Parallel()

	shutdown, err := Setup(context.Background(), Config{})
	if err != nil {
		t.Fatal(err)
	}
	Emit(context.Background(), PolicyViolation("ns", "s", "network", "evil.example", "denied", "self-reported", time.Now()))
	if err := shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestEmit_otlpSink(t *testing.T) {
	t.Parallel()

	recorder := logtest.NewRecorder()
	global.SetLoggerProvider(recorder)
	activeSink = otlpSink{logger: recorder.Logger(loggerName)}

	at := time.Unix(1_700_000_000, 0)
	Emit(context.Background(), SessionPhaseChange("team-a", "session-1", "Pending", "Running", at))

	records := recorder.Result()
	if len(records) != 1 || len(records[0].Records) != 1 {
		t.Fatalf("records = %+v", records)
	}
	got := records[0].Records[0]
	if got.Body().AsString() != "session phase changed" {
		t.Fatalf("body = %q", got.Body().AsString())
	}
}

func TestRecordBuilders(t *testing.T) {
	t.Parallel()

	v := PolicyViolation("ns", "s", "tool", "kubectl", "denied", "self-reported", time.Time{})
	if v.EventType != EventPolicyViolation || v.Type != "tool" || v.Assurance != "self-reported" {
		t.Fatalf("violation record = %+v", v)
	}
	p := SessionPhaseChange("ns", "s", "Starting", "Running", time.Time{})
	if p.FromPhase != "Starting" || p.Phase != "Running" {
		t.Fatalf("phase record = %+v", p)
	}
	r := RuntimeReport("ns", "s", "dns-proxy", 2, "self-reported", time.Time{})
	if r.Backend != "dns-proxy" || r.Count != 2 || r.Assurance != "self-reported" {
		t.Fatalf("report record = %+v", r)
	}

	granted := ApprovalDecision("ns", "s", "deploy", "alice", "human approval granted", true, time.Time{})
	if granted.EventType != EventApprovalGranted || granted.Action != "granted" ||
		granted.Actor != "alice" || granted.Target != "deploy" || granted.Type != "approval" ||
		granted.Assurance != "controller" {
		t.Fatalf("granted record = %+v", granted)
	}
	denied := ApprovalDecision("ns", "s", "deploy", "", "human approval was denied", false, time.Time{})
	if denied.EventType != EventApprovalDenied || denied.Action != "denied" || denied.Actor != "scrutineer-controller" {
		t.Fatalf("denied record = %+v", denied)
	}
}
