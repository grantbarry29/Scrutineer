/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package tracing

import (
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// HTTPMiddleware extracts W3C trace context from the request and starts a span for
// downstream handlers. Sidecars may continue agent-runtime traces via traceparent headers.
func HTTPMiddleware(tracer trace.Tracer, spanName string, next http.Handler) http.Handler {
	if tracer == nil {
		tracer = ReporterTracer()
	}
	propagator := otel.GetTextMapPropagator()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := propagator.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		ctx, span := tracer.Start(ctx, spanName)
		defer span.End()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
