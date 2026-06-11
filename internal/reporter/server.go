/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package reporter

import (
	"context"
	"errors"
	"net/http"
	"time"

	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// Options configures the runtime reporter HTTP server.
type Options struct {
	BindAddress string
	Client      client.Client
	APIReader   client.Reader
	Recorder    record.EventRecorder
	Audience    string
	Verifier    IdentityVerifier
}

// +kubebuilder:rbac:groups=authentication.k8s.io,resources=tokenreviews,verbs=create
// +kubebuilder:rbac:groups=relay.secureai.dev,resources=agentsessions,verbs=get
// +kubebuilder:rbac:groups=relay.secureai.dev,resources=agentsessions/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get
// +kubebuilder:rbac:groups="",resources=pods,verbs=get

// NewRunnable returns a manager.Runnable that serves POST /v1/report.
func NewRunnable(opts Options) manager.Runnable {
	bind := opts.BindAddress
	if bind == "" {
		bind = DefaultBindAddress
	}

	verifier := opts.Verifier
	if verifier == nil {
		verifier = &KubeIdentityVerifier{
			Client:   opts.Client,
			Reader:   opts.APIReader,
			Audience: opts.Audience,
		}
	}

	handler := &Handler{
		Writer:    opts.Client.Status(),
		Reader:    opts.APIReader,
		Verifier:  verifier,
		Recorder:  opts.Recorder,
		Limiter:   newSessionRateLimiter(time.Second),
		ReportIDs: newReportIDCache(DefaultReportIDCacheTTL),
	}

	mux := http.NewServeMux()
	mux.Handle(reportPath, handler)

	return &httpServer{
		addr: bind,
		mux:  mux,
	}
}

type httpServer struct {
	addr string
	mux  http.Handler
	srv  *http.Server
}

func (s *httpServer) Start(ctx context.Context) error {
	s.srv = &http.Server{
		Addr:    s.addr,
		Handler: s.mux,
	}

	errCh := make(chan error, 1)
	go func() {
		if err := s.srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.srv.Shutdown(shutdownCtx)
		return nil
	case err, ok := <-errCh:
		if !ok {
			return nil
		}
		return err
	}
}
