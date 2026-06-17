/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package main

import (
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/secureai/relay/internal/enforcement/workspace"
)

func main() {
	env, err := workspace.LoadRuntimeEnv()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	gw := &workspace.Gateway{
		Env:      env,
		Reporter: workspace.NewReporterClient(env.ReporterURL, env.ReporterToken, nil),
	}

	srv := &http.Server{
		Addr:    env.ListenHost,
		Handler: gw,
	}

	go func() {
		log.Printf("relay fs-gateway listening on %s (session %s/%s, mode=%s)",
			env.ListenHost, env.SessionNamespace, env.SessionName, env.Mode)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	_ = srv.Close()
}
