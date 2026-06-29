/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package dnsproxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
)

// Proxy is a minimal HTTP(S) egress proxy that evaluates policy and reports evidence.
type Proxy struct {
	Env      RuntimeEnv
	Reporter *ReporterClient
	Now      func() time.Time
	Dial     func(network, address string) (net.Conn, error)
}

// ServeHTTP implements http.Handler.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host, port := hostPortFromRequest(r)
	if host == "" {
		http.Error(w, "missing host", http.StatusBadRequest)
		return
	}

	ctx := p.Env.SessionContext()
	egress := EgressRequest{Host: host, Port: port}
	auth := EvaluateEgress(ctx, egress)

	if shouldReport(auth) {
		report := runtimeReport(ctx, egress, auth, p.now())
		if p.Reporter != nil {
			_ = p.Reporter.Submit(r.Context(), p.Env, report)
		}
	}

	if auth.Blocked {
		http.Error(w, fmt.Sprintf("egress to %s denied by policy (%s)", host, auth.Reason), http.StatusForbidden)
		return
	}

	if r.Method == http.MethodConnect {
		p.handleConnect(w, r)
		return
	}
	p.handleHTTP(w, r)
}

func (p *Proxy) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *Proxy) dial(network, address string) (net.Conn, error) {
	if p.Dial != nil {
		return p.Dial(network, address)
	}
	return net.Dial(network, address)
}

func shouldReport(auth EgressAuthorization) bool {
	return auth.Reason != ReasonAllowed || auth.Action != scrutineerv1alpha1.PolicyDecisionAllow
}

func hostPortFromRequest(r *http.Request) (host string, port int32) {
	if r.Method == http.MethodConnect {
		return splitHostPort(r.Host)
	}
	if r.URL != nil && r.URL.Host != "" {
		return splitHostPort(r.URL.Host)
	}
	if h := strings.TrimSpace(r.Host); h != "" {
		h, p := splitHostPort(h)
		if p == 0 {
			if r.TLS != nil {
				return h, 443
			}
			return h, 80
		}
		return h, p
	}
	return "", 0
}

func splitHostPort(raw string) (host string, port int32) {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return "", 0
	}
	if strings.Contains(raw, ":") {
		h, ps, err := net.SplitHostPort(raw)
		if err == nil {
			if p, err := strconv.ParseInt(ps, 10, 32); err == nil {
				return strings.Trim(h, "[]"), int32(p)
			}
		}
	}
	return strings.Trim(raw, "[]"), 0
}

func (p *Proxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	destConn, err := p.dial("tcp", r.Host)
	if err != nil {
		http.Error(w, "upstream connect failed", http.StatusBadGateway)
		return
	}
	defer destConn.Close()

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "connect not supported", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, "hijack failed", http.StatusInternalServerError)
		return
	}
	defer clientConn.Close()

	_, _ = clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

	// Tunnel both directions concurrently. When a direction finishes (its source
	// hit EOF), half-close the peer's write side so the other end observes EOF and
	// the tunnel tears down promptly instead of lingering half-open — e.g. a client
	// that sends FIN on its write side while still reading. wg.Wait blocks until
	// both copies complete, so the deferred Closes above fully close both ends.
	var wg sync.WaitGroup
	wg.Add(2)
	go tunnelHalfDuplex(&wg, destConn, clientConn) // client -> dest
	go tunnelHalfDuplex(&wg, clientConn, destConn) // dest -> client
	wg.Wait()
}

// tunnelHalfDuplex copies one direction of a CONNECT tunnel, then half-closes the
// destination's write side so the peer reads EOF.
func tunnelHalfDuplex(wg *sync.WaitGroup, dst, src net.Conn) {
	defer wg.Done()
	_, _ = io.Copy(dst, src)
	closeWrite(dst)
}

// closeWrite shuts down only the write half of c when supported (e.g.
// *net.TCPConn, *tls.Conn), so the peer reads EOF while c's read half stays open
// to drain the other direction. Falls back to a full Close when half-close is
// unavailable (e.g. net.Pipe).
func closeWrite(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
		return
	}
	_ = c.Close()
}

func (p *Proxy) handleHTTP(w http.ResponseWriter, r *http.Request) {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return p.dial(network, addr)
		},
		DisableKeepAlives: true,
	}
	defer transport.CloseIdleConnections()

	outReq := r.Clone(r.Context())
	if outReq.URL.Scheme == "" {
		outReq.URL.Scheme = "http"
	}
	if outReq.URL.Host == "" {
		outReq.URL.Host = r.Host
	}

	resp, err := transport.RoundTrip(outReq)
	if err != nil {
		http.Error(w, "upstream request failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for k, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}
