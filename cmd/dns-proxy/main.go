/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/grantbarry29/scrutineer/internal/enforcement/dnsproxy"
)

func main() {
	env, err := dnsproxy.LoadRuntimeEnv()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	proxy := &dnsproxy.Proxy{
		Env:      env,
		Reporter: dnsproxy.NewReporterClient(env.ReporterURL, env.ReporterToken, nil),
	}

	srv := &http.Server{
		Addr:    env.ListenAddr,
		Handler: proxy,
	}

	go func() {
		log.Printf("scrutineer dns-proxy listening on %s (session %s/%s, mode=%s)",
			env.ListenAddr, env.SessionNamespace, env.SessionName, env.Mode)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		_ = srv.Close()
	}
}
