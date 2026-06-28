/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Command manager is the Relay controller-manager entrypoint.
package main

import (
	"context"
	"flag"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
	"github.com/secureai/relay/internal/approval"
	"github.com/secureai/relay/internal/audit"
	"github.com/secureai/relay/internal/controller/agentsession"
	"github.com/secureai/relay/internal/metrics"
	"github.com/secureai/relay/internal/reporter"
	"github.com/secureai/relay/internal/tracing"
	relaywebhookv1alpha1 "github.com/secureai/relay/internal/webhook/v1alpha1"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(relayv1alpha1.AddToScheme(scheme))
}

func main() {
	var (
		metricsAddr          string
		probeAddr            string
		reporterAddr         string
		otelEndpoint         string
		otelServiceName      string
		otelInsecure         bool
		auditLogEndpoint     string
		auditLogInsecure     bool
		enableLeaderElection bool
		reporterOnly         bool
		reporterEnabled      bool
		approvalWebhookURL   string
		enableWebhooks       bool
	)
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "Address the metrics endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "Address the probe endpoint binds to.")
	flag.StringVar(&reporterAddr, "reporter-bind-address", reporter.DefaultBindAddress,
		"Address the runtime evidence reporter endpoint binds to (POST /v1/report).")
	flag.StringVar(&otelEndpoint, "otel-exporter-otlp-endpoint", "",
		"OTLP HTTP endpoint for trace export (e.g. http://localhost:4318). Empty disables tracing export.")
	flag.StringVar(&otelServiceName, "otel-service-name", "relay-controller",
		"OpenTelemetry service.name resource attribute.")
	flag.BoolVar(&otelInsecure, "otel-exporter-otlp-insecure", true,
		"Disable TLS verification for the OTLP trace exporter.")
	flag.StringVar(&auditLogEndpoint, "audit-log-otlp-endpoint", "",
		"OTLP HTTP endpoint for audit log export (e.g. http://localhost:4318/v1/logs). Empty disables audit export.")
	flag.BoolVar(&auditLogInsecure, "audit-log-otlp-insecure", true,
		"Disable TLS verification for the OTLP audit log exporter.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. Ensures only one active controller.")
	flag.BoolVar(&reporterOnly, "reporter-only", false,
		"Serve only the runtime evidence reporter (no AgentSession reconciler). Used for in-cluster e2e reporter deployments.")
	flag.BoolVar(&reporterEnabled, "enable-reporter", true,
		"Run the in-process runtime evidence reporter in the full manager. Set false when the reporter is deployed standalone (--reporter-only) so its RBAC need not be granted to the manager ServiceAccount.")
	flag.StringVar(&approvalWebhookURL, "approval-webhook-url", "",
		"Webhook URL notified (HTTP POST JSON) when a session opens a human-approval gate. Empty disables notifications.")
	flag.BoolVar(&enableWebhooks, "enable-webhooks", false,
		"Serve admission webhooks (requires TLS certs mounted at the webhook server cert dir). Enables the ApprovalRequest identity-stamping webhook that captures the authenticated approver identity.")

	opts := zap.Options{Development: true}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	ctx := context.Background()
	traceShutdown, err := tracing.Setup(ctx, tracing.Config{
		ServiceName: otelServiceName,
		Endpoint:    otelEndpoint,
		Insecure:    otelInsecure,
	})
	if err != nil {
		setupLog.Error(err, "unable to set up OpenTelemetry tracing")
		os.Exit(1)
	}
	defer func() {
		if err := traceShutdown(context.Background()); err != nil {
			setupLog.Error(err, "OpenTelemetry shutdown error")
		}
	}()

	auditShutdown, err := audit.Setup(ctx, audit.Config{
		ServiceName: otelServiceName,
		Endpoint:    auditLogEndpoint,
		Insecure:    auditLogInsecure,
	})
	if err != nil {
		setupLog.Error(err, "unable to set up audit log sink")
		os.Exit(1)
	}
	defer func() {
		if err := auditShutdown(context.Background()); err != nil {
			setupLog.Error(err, "audit log shutdown error")
		}
	}()

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "relay.secureai.dev",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err := metrics.Register(&metrics.AgentSessionCollector{Client: mgr.GetClient()}); err != nil {
		setupLog.Error(err, "unable to register Prometheus metrics")
		os.Exit(1)
	}

	if !reporterOnly {
		var notifier approval.Notifier
		if approvalWebhookURL != "" {
			notifier = approval.NewWebhookNotifier(approvalWebhookURL)
			setupLog.Info("approval notifications enabled", "webhook", approvalWebhookURL)
		}
		if err := (&agentsession.AgentSessionReconciler{
			Client:    mgr.GetClient(),
			APIReader: mgr.GetAPIReader(),
			Scheme:    mgr.GetScheme(),
			Recorder:  mgr.GetEventRecorderFor("agentsession-controller"),
			Notifier:  notifier,
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "AgentSession")
			os.Exit(1)
		}

		if enableWebhooks {
			if err := relaywebhookv1alpha1.SetupApprovalRequestWebhookWithManager(mgr); err != nil {
				setupLog.Error(err, "unable to set up admission webhook", "webhook", "ApprovalRequest")
				os.Exit(1)
			}
			setupLog.Info("admission webhooks enabled", "webhook", "ApprovalRequest identity stamper")
		}
	}

	// The runtime reporter runs in reporter-only mode (the dedicated reporter
	// Deployment) and, by default, in-process in the full manager. Set
	// --enable-reporter=false on the manager when the reporter is deployed
	// standalone so its RBAC need not be granted to the manager ServiceAccount.
	if reporterOnly || reporterEnabled {
		if err := mgr.Add(reporter.NewRunnable(reporter.Options{
			BindAddress: reporterAddr,
			Client:      mgr.GetClient(),
			APIReader:   mgr.GetAPIReader(),
			Recorder:    mgr.GetEventRecorderFor("relay-runtime-reporter"),
		})); err != nil {
			setupLog.Error(err, "unable to set up runtime reporter")
			os.Exit(1)
		}
	}

	// Registered in both modes so the standalone reporter Deployment is probeable
	// on the health port.
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting Relay manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
