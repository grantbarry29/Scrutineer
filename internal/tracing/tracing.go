/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package tracing configures OpenTelemetry trace export for the Scrutineer control plane.
package tracing

import (
	"context"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

const instrumentationScope = "github.com/grantbarry29/scrutineer"

// Config controls OTLP trace export. An empty Endpoint disables export (noop provider).
type Config struct {
	ServiceName string
	Endpoint    string
	Insecure    bool
}

// Setup installs the global TracerProvider. Returns a shutdown function (no-op when disabled).
func Setup(ctx context.Context, cfg Config) (func(context.Context) error, error) {
	serviceName := cfg.ServiceName
	if serviceName == "" {
		serviceName = "scrutineer-controller"
	}

	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		otel.SetTracerProvider(noop.NewTracerProvider())
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		))
		return func(context.Context) error { return nil }, nil
	}

	opts := []otlptracehttp.Option{
		otlptracehttp.WithEndpointURL(normalizeEndpoint(endpoint)),
	}
	if cfg.Insecure {
		opts = append(opts, otlptracehttp.WithInsecure())
	}

	exporter, err := otlptracehttp.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("create OTLP trace exporter: %w", err)
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewSchemaless(
			attribute.String("service.name", serviceName),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("create otel resource: %w", err)
	}

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return provider.Shutdown, nil
}

func normalizeEndpoint(endpoint string) string {
	if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
		return endpoint
	}
	return "http://" + endpoint
}

// ReconcileTracer returns the AgentSession reconciler tracer.
func ReconcileTracer() trace.Tracer {
	return otel.Tracer(instrumentationScope + "/agentsession")
}

// ReporterTracer returns the runtime reporter tracer.
func ReporterTracer() trace.Tracer {
	return otel.Tracer(instrumentationScope + "/reporter")
}

// StartReconcileSpan begins a reconcile span with session identity attributes.
func StartReconcileSpan(ctx context.Context, namespace, name string) (context.Context, trace.Span) {
	return ReconcileTracer().Start(ctx, "agentsession.reconcile",
		trace.WithAttributes(
			attribute.String("scrutineer.session.namespace", namespace),
			attribute.String("scrutineer.session.name", name),
		),
	)
}

// SetReconcileSpanResult records terminal reconcile attributes on the active span.
func SetReconcileSpanResult(ctx context.Context, phase string, requeueAfterSec float64, err error) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}
	if phase != "" {
		span.SetAttributes(attribute.String("scrutineer.session.phase", phase))
	}
	if requeueAfterSec > 0 {
		span.SetAttributes(attribute.Float64("scrutineer.reconcile.requeue_after_seconds", requeueAfterSec))
	}
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
}

// SetReportSpanAttributes annotates the active reporter span.
func SetReportSpanAttributes(ctx context.Context, namespace, name, backend, result string, decisionCount int) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}
	attrs := []attribute.KeyValue{
		attribute.String("scrutineer.session.namespace", namespace),
		attribute.String("scrutineer.session.name", name),
		attribute.String("scrutineer.report.result", result),
	}
	if backend != "" {
		attrs = append(attrs, attribute.String("scrutineer.report.backend", backend))
	}
	if decisionCount >= 0 {
		attrs = append(attrs, attribute.Int("scrutineer.report.decisions", decisionCount))
	}
	span.SetAttributes(attrs...)
}
