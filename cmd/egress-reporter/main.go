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
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/enforcement"
	"github.com/grantbarry29/scrutineer/internal/enforcement/containerenv"
	"github.com/grantbarry29/scrutineer/internal/enforcement/egressmetrics"
	"github.com/grantbarry29/scrutineer/internal/enforcement/envoy"
	"github.com/grantbarry29/scrutineer/internal/enforcement/reporterclient"
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
	base, err := containerenv.LoadBase("")
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
		// Rotation (#98): once the fully-ingested log passes the threshold it is
		// renamed, Envoy is asked to reopen via the loopback admin API (same pod
		// netns), the remainder is drained, and only then deleted — a flood can never
		// discard un-ingested evidence.
		Reopen:           envoyReopenLogs,
		RotateAfterBytes: rotateAfterBytesFromEnv(),
	}

	metrics := egressmetrics.New(func() float64 { return float64(tailer.Dropped()) })
	tailer.OnDecision = metrics.ObserveDecision
	tailer.OnMalformed = metrics.Malformed.Inc
	tailer.OnRejected = metrics.ObserveRejected
	tailer.OnRotated = metrics.Rotations.Inc
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

// envoyReopenLogs POSTs the Envoy admin /reopen_logs endpoint so a renamed access log
// is replaced with a fresh file (#98). The admin API binds loopback-only inside the
// Envoy container; this container shares the pod's network namespace, so 127.0.0.1
// reaches it while nothing outside the pod can.
func envoyReopenLogs(ctx context.Context) error {
	url := fmt.Sprintf("http://127.0.0.1:%d/reopen_logs", envoy.AdminPort)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("envoy admin /reopen_logs returned %d", resp.StatusCode)
	}
	return nil
}

// rotateAfterBytesFromEnv reads the optional rotation-threshold override the controller
// sets on the container (containerenv.EnvRotateAfterBytes). Unset or invalid falls back
// to the Tailer default; rotation itself is always on (Reopen is always wired).
func rotateAfterBytesFromEnv() int64 {
	raw := strings.TrimSpace(os.Getenv(containerenv.EnvRotateAfterBytes))
	if raw == "" {
		return 0
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		log.Printf("egress-reporter: ignoring invalid %s=%q", containerenv.EnvRotateAfterBytes, raw)
		return 0
	}
	return n
}
