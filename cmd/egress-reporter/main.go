/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Command egress-reporter runs beside Envoy in the per-session egress-proxy pod (NOT in
// the agent pod — the proxy pod is outside the agent's trust domain, see
// docs/design/evidence-integrity.md). It tails Envoy's JSON access log from the shared
// emptyDir and submits each entry as runtime egress evidence to the controller-owned
// reporter, authenticated with the proxy pod's own per-session ServiceAccount token —
// the identity the reporter requires before stamping evidence `observed` (Slice C, #62).
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/enforcement"
	"github.com/grantbarry29/scrutineer/internal/enforcement/egressmetrics"
	"github.com/grantbarry29/scrutineer/internal/enforcement/envoy"
	"github.com/grantbarry29/scrutineer/internal/enforcement/reporterclient"
	"github.com/grantbarry29/scrutineer/internal/enforcement/sidecarenv"
)

const (
	// EnvAccessLogPath optionally overrides the access-log file location (defaults to
	// envoy.AccessLogPath, where the bootstrap writes it).
	EnvAccessLogPath = "SCRUTINEER_ACCESS_LOG_PATH"

	// EnvMetricsAddr overrides the Prometheus /metrics bind (#55). Defaults to
	// ":<envoy.ReporterMetricsPort>"; the value "disabled" turns the endpoint off.
	EnvMetricsAddr = "SCRUTINEER_METRICS_ADDR"
)

func main() {
	base, err := sidecarenv.LoadBase("")
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	path := strings.TrimSpace(os.Getenv(EnvAccessLogPath))
	if path == "" {
		path = envoy.AccessLogPath
	}

	client := reporterclient.New(base.ReporterURL, base.ReporterToken, enforcement.BackendEgressProxy, nil)
	session := reporterclient.SessionRef{Namespace: base.SessionNamespace, Name: base.SessionName}

	tailer := &envoy.Tailer{
		Path:   path,
		Policy: envoy.PolicyFromEnv(),
		Submit: func(ctx context.Context, decisions []scrutineerv1alpha1.PolicyDecision) error {
			return client.Submit(ctx, session, enforcement.RuntimeReport{Decisions: decisions})
		},
	}

	metrics := egressmetrics.New(func() float64 { return float64(tailer.Dropped()) })
	tailer.OnDecision = metrics.ObserveDecision
	tailer.OnMalformed = metrics.Malformed.Inc
	tailer.OnRejected = metrics.ObserveRejected
	tailer.Submit = metrics.WrapSubmit(tailer.Submit)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	metricsAddr := strings.TrimSpace(os.Getenv(EnvMetricsAddr))
	if metricsAddr == "" {
		metricsAddr = fmt.Sprintf(":%d", envoy.ReporterMetricsPort)
	}
	if metricsAddr != "disabled" {
		// Metrics are auxiliary: a bind failure is logged, never fatal — the evidence
		// pipeline must keep running without telemetry.
		go func() {
			if err := metrics.Serve(ctx, metricsAddr); err != nil {
				log.Printf("egress-reporter: metrics endpoint unavailable (%s): %v", metricsAddr, err)
			}
		}()
	}

	log.Printf("scrutineer egress-reporter tailing %s (session %s/%s)", path, base.SessionNamespace, base.SessionName)
	tailer.Run(ctx)
}
