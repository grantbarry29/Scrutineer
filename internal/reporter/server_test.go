/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package reporter

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"testing"
	"time"
)

// #107: :8088 is deliberately reachable by workload pods, so the server itself must
// bound how long a connection may sit in each phase — slowloris and trickled bodies
// terminate instead of accumulating goroutines in the controller manager.
func TestNewServer_setsAllTimeouts(t *testing.T) {
	srv := (&httpServer{addr: ":0", mux: http.NewServeMux()}).newServer()
	if srv.ReadHeaderTimeout != serverReadHeaderTimeout || srv.ReadHeaderTimeout <= 0 {
		t.Fatalf("ReadHeaderTimeout = %v", srv.ReadHeaderTimeout)
	}
	if srv.ReadTimeout != serverReadTimeout || srv.ReadTimeout <= 0 {
		t.Fatalf("ReadTimeout = %v", srv.ReadTimeout)
	}
	if srv.WriteTimeout != serverWriteTimeout || srv.WriteTimeout <= 0 {
		t.Fatalf("WriteTimeout = %v", srv.WriteTimeout)
	}
	if srv.IdleTimeout != serverIdleTimeout || srv.IdleTimeout <= 0 {
		t.Fatalf("IdleTimeout = %v", srv.IdleTimeout)
	}
}

// A slowloris client — headers started, then silence — is disconnected by the
// production server configuration rather than held open forever.
func TestServer_terminatesStalledClient(t *testing.T) {
	srv := (&httpServer{addr: ":0", mux: http.NewServeMux()}).newServer()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := fmt.Fprint(conn, "POST /v1/report HTTP/1.1\r\nHost: reporter\r\n"); err != nil {
		t.Fatal(err)
	}
	// ...and stall. The server must give up on us around ReadHeaderTimeout; the
	// read deadline below only bounds the test if it does not.
	deadline := serverReadHeaderTimeout + 5*time.Second
	if err := conn.SetReadDeadline(time.Now().Add(deadline)); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = conn.Read(make([]byte, 1))
	if err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("server kept the stalled connection open ≥ %v (read err = %v)", deadline, err)
	}
	if elapsed := time.Since(start); elapsed > serverReadHeaderTimeout+3*time.Second {
		t.Fatalf("connection terminated after %v, want ≈ %v", elapsed, serverReadHeaderTimeout)
	}
}
