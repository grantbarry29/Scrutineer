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
	"fmt"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/log/noop"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
)

const loggerName = "scrutineer.audit"

// Config controls OTLP audit log export. An empty Endpoint disables export.
type Config struct {
	ServiceName string
	Endpoint    string
	Insecure    bool
}

var (
	setupOnce  sync.Once
	setupErr   error
	activeSink sink = noopSink{}
	shutdownFn      = func(context.Context) error { return nil }
)

type sink interface {
	emit(context.Context, Record)
}

type noopSink struct{}

func (noopSink) emit(context.Context, Record) {}

type otlpSink struct {
	logger otellog.Logger
}

// Setup configures the global audit sink. Safe to call once at process startup.
func Setup(ctx context.Context, cfg Config) (func(context.Context) error, error) {
	setupOnce.Do(func() {
		endpoint := strings.TrimSpace(cfg.Endpoint)
		if endpoint == "" {
			global.SetLoggerProvider(noop.NewLoggerProvider())
			activeSink = noopSink{}
			return
		}

		serviceName := cfg.ServiceName
		if serviceName == "" {
			serviceName = "scrutineer-controller"
		}

		opts := []otlploghttp.Option{
			otlploghttp.WithEndpointURL(normalizeEndpoint(endpoint)),
		}
		if cfg.Insecure {
			opts = append(opts, otlploghttp.WithInsecure())
		}

		exporter, err := otlploghttp.New(ctx, opts...)
		if err != nil {
			setupErr = fmt.Errorf("create OTLP audit log exporter: %w", err)
			return
		}

		res, err := resource.Merge(
			resource.Default(),
			resource.NewSchemaless(attribute.String("service.name", serviceName)),
		)
		if err != nil {
			setupErr = fmt.Errorf("create audit log resource: %w", err)
			return
		}

		provider := sdklog.NewLoggerProvider(
			sdklog.WithProcessor(sdklog.NewBatchProcessor(exporter)),
			sdklog.WithResource(res),
		)
		global.SetLoggerProvider(provider)
		shutdownFn = provider.Shutdown
		activeSink = otlpSink{logger: provider.Logger(loggerName)}
	})
	return shutdownFn, setupErr
}

// Emit writes an audit record to the configured sink. Never blocks reconciliation on export errors.
func Emit(ctx context.Context, rec Record) {
	if rec.Time.IsZero() {
		rec.Time = time.Now()
	}
	activeSink.emit(ctx, rec)
}

func (s otlpSink) emit(ctx context.Context, rec Record) {
	if s.logger == nil {
		return
	}
	var lr otellog.Record
	lr.SetTimestamp(rec.Time)
	lr.SetObservedTimestamp(time.Now())
	lr.SetSeverity(otellog.SeverityInfo)
	lr.SetSeverityText("INFO")
	if rec.Message != "" {
		lr.SetBody(otellog.StringValue(rec.Message))
	}

	attrs := []otellog.KeyValue{
		otellog.String("scrutineer.audit.event_type", string(rec.EventType)),
	}
	if rec.Namespace != "" {
		attrs = append(attrs, otellog.String("scrutineer.session.namespace", rec.Namespace))
	}
	if rec.Session != "" {
		attrs = append(attrs, otellog.String("scrutineer.session.name", rec.Session))
	}
	if rec.Actor != "" {
		attrs = append(attrs, otellog.String("scrutineer.audit.actor", rec.Actor))
	}
	if rec.Phase != "" {
		attrs = append(attrs, otellog.String("scrutineer.session.phase", rec.Phase))
	}
	if rec.FromPhase != "" {
		attrs = append(attrs, otellog.String("scrutineer.session.from_phase", rec.FromPhase))
	}
	if rec.Action != "" {
		attrs = append(attrs, otellog.String("scrutineer.audit.action", rec.Action))
	}
	if rec.Target != "" {
		attrs = append(attrs, otellog.String("scrutineer.audit.target", rec.Target))
	}
	if rec.Type != "" {
		attrs = append(attrs, otellog.String("scrutineer.audit.type", rec.Type))
	}
	if rec.Backend != "" {
		attrs = append(attrs, otellog.String("scrutineer.report.backend", rec.Backend))
	}
	if rec.Assurance != "" {
		attrs = append(attrs, otellog.String("scrutineer.audit.assurance", rec.Assurance))
	}
	if rec.Count > 0 {
		attrs = append(attrs, otellog.Int("scrutineer.audit.count", rec.Count))
	}
	lr.AddAttributes(attrs...)
	s.logger.Emit(ctx, lr)
}

func normalizeEndpoint(endpoint string) string {
	if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
		return endpoint
	}
	return "http://" + endpoint
}
