/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package envoy holds addressing and agent-wiring for the per-session Envoy egress
// proxy — the out-of-pod chokepoint that carries agent egress in the evidence-integrity
// design (docs/design/evidence-integrity.md, issue #8, Slice A #60).
//
// This file is the deterministic, unit-testable foundation: how the agent is pointed at
// its Envoy (explicit-proxy env) and how the Envoy Service is named/addressed. The Envoy
// bootstrap config and the controller wiring that creates the Envoy Pod/Service/ConfigMap
// as owner-referenced per-session resources are separate follow-ups that must be
// e2e-validated against a running Envoy (see the design doc).
package envoy

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
)

const (
	// DefaultEnvoyImage is the upstream distroless Envoy image for the egress proxy,
	// pinned by digest for reproducibility (the tag stays for readability). The digest is
	// the multi-arch OCI index (amd64 + arm64) for Envoy 1.31.10, validated with
	// `envoy --mode validate` and the networking e2e. Keep in sync with ENVOY_IMG in the
	// Makefile; bump both together on an intentional upgrade.
	DefaultEnvoyImage = "envoyproxy/envoy:distroless-v1.31-latest@sha256:451ad9c42b4a706092455d524e836365d265760e3e6337c1f42980b18db4c247"

	// ProxyPort is the port Envoy listens on as an HTTP forward proxy (plain HTTP and
	// HTTPS via CONNECT). Agents reach it via the explicit-proxy env vars below.
	ProxyPort = 15001

	// serviceSuffix is appended to the AgentSession name to form the Envoy Service name.
	serviceSuffix = "-egress"

	// maxServiceNameLen is the Kubernetes name length limit (RFC 1035 label).
	maxServiceNameLen = 63
)

// ServiceName returns the per-session Envoy Service name derived from the session name,
// bounded to the Kubernetes 63-char limit.
func ServiceName(sessionName string) string {
	base := sessionName + serviceSuffix
	if len(base) <= maxServiceNameLen {
		return base
	}
	// Truncate the session portion, preserving the suffix so the resource stays
	// recognizable. (Uniqueness is scoped per-session and enforced by owner refs;
	// a stronger hash-based scheme can replace this if collisions ever matter.)
	keep := maxServiceNameLen - len(serviceSuffix)
	return sessionName[:keep] + serviceSuffix
}

// ProxyURL is the in-cluster URL agents target as their HTTP(S) proxy.
func ProxyURL(sessionName, namespace string) string {
	return fmt.Sprintf("http://%s.%s.svc:%d", ServiceName(sessionName), namespace, ProxyPort)
}

// ExplicitProxyEnv returns the env vars that route an agent container's egress through
// its per-session Envoy. Both upper- and lower-case forms are set because common tools
// differ on which they read (BusyBox wget reads only lowercase; Go/curl/Python read
// either) — omitting a variant would let that tooling bypass the proxy. NO_PROXY keeps
// loopback direct so in-pod health/localhost traffic is unaffected.
//
// Security note: these env vars only *route well-behaved traffic*. The mandatory,
// non-bypassable enforcement is the default-deny egress NetworkPolicy (Slice B, #61);
// rewriting or ignoring these vars only self-sabotages, since Envoy is the sole
// reachable egress.
func ExplicitProxyEnv(proxyURL string) []corev1.EnvVar {
	const noProxy = "localhost,127.0.0.1"
	return []corev1.EnvVar{
		{Name: "HTTP_PROXY", Value: proxyURL},
		{Name: "HTTPS_PROXY", Value: proxyURL},
		{Name: "NO_PROXY", Value: noProxy},
		{Name: "http_proxy", Value: proxyURL},
		{Name: "https_proxy", Value: proxyURL},
		{Name: "no_proxy", Value: noProxy},
	}
}
