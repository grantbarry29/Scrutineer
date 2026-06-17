/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package tracing

import (
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestSetup_noopWhenEndpointEmpty(t *testing.T) {
	t.Parallel()

	shutdown, err := Setup(context.Background(), Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}

	_, span := ReconcileTracer().Start(context.Background(), "test")
	if span == nil {
		t.Fatal("expected span")
	}
	span.End()
}

func TestStartReconcileSpan_recordsAttributes(t *testing.T) {
	t.Parallel()

	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	ctx, span := StartReconcileSpan(context.Background(), "ns1", "session-a")
	SetReconcileSpanResult(ctx, "Running", 15, nil)
	span.End()

	spans := sr.Ended()
	if len(spans) != 1 {
		t.Fatalf("ended spans = %d, want 1", len(spans))
	}
	got := spans[0]
	if got.Name() != "agentsession.reconcile" {
		t.Fatalf("name = %q", got.Name())
	}
	if v, ok := spanAttribute(got, "relay.session.namespace"); !ok || v.AsString() != "ns1" {
		t.Fatalf("namespace attr missing or wrong: %+v", got.Attributes())
	}
}

func spanAttribute(sp sdktrace.ReadOnlySpan, key string) (attribute.Value, bool) {
	for _, attr := range sp.Attributes() {
		if string(attr.Key) == key {
			return attr.Value, true
		}
	}
	return attribute.Value{}, false
}

func TestSetReconcileSpanResult_recordsError(t *testing.T) {
	t.Parallel()

	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	ctx, span := StartReconcileSpan(context.Background(), "ns1", "session-a")
	SetReconcileSpanResult(ctx, "Failed", 0, errors.New("boom"))
	span.End()

	got := sr.Ended()[0]
	if got.Status().Code != codes.Error {
		t.Fatalf("status = %+v", got.Status())
	}
}
