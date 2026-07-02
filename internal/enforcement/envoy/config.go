/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package envoy

import "fmt"

// BootstrapYAML renders the Envoy bootstrap config for the per-session egress proxy: an
// explicit HTTP forward proxy that terminates plain HTTP and tunnels HTTPS via CONNECT,
// resolving upstream names itself through a dynamic_forward_proxy DNS cache (so the agent
// needs no direct DNS — see the evidence-integrity design). Admin binds to loopback only.
//
// internal_address_config is set to trust no addresses: as a forward proxy we never honor
// a client-supplied X-Forwarded-For, and it also silences Envoy's default-trust warning.
//
// Two access logs record every proxied request: a human-readable stdout log (kubectl-logs
// visibility; also the Slice A e2e traversal proof) and a JSON file log at AccessLogPath in
// the shared access-log volume, tailed by the egress-reporter container and converted into
// `observed` egress evidence (Slice C, #62 — keys must stay in sync with AccessLogEntry).
// json_format renders numeric operators as JSON numbers/null, never "-" placeholders. The
// `%%` are fmt escapes for Envoy command operators; the rendered config contains single `%`.
//
// When cfg enforces an FQDN policy, an RBAC filter chain is inserted before the forward
// proxy so denied/not-allowed authorities are blocked at the chokepoint (#32); audit mode
// generates none (the egress-reporter records dry-run instead).
//
// Validated with `envoy --mode validate` (Envoy 1.31). Full CONNECT/forward-proxy behavior
// is proven by the Slice A e2e (issue #60, A4), not by unit tests.
func BootstrapYAML(cfg BootstrapConfig) string {
	return fmt.Sprintf(`admin:
  address:
    socket_address:
      address: 127.0.0.1
      port_value: 9901
static_resources:
  listeners:
  - name: http_proxy
    address:
      socket_address:
        address: 0.0.0.0
        port_value: %[1]d
    filter_chains:
    - filters:
      - name: envoy.filters.network.http_connection_manager
        typed_config:
          "@type": type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager
          stat_prefix: egress_http
          access_log:
          - name: envoy.access_loggers.stdout
            typed_config:
              "@type": type.googleapis.com/envoy.extensions.access_loggers.stream.v3.StdoutAccessLog
              log_format:
                text_format_source:
                  inline_string: "scrutineer-egress %%REQ(:METHOD)%% %%REQ(:AUTHORITY)%% -> %%RESPONSE_CODE%% %%RESPONSE_FLAGS%%\n"
          - name: envoy.access_loggers.file
            typed_config:
              "@type": type.googleapis.com/envoy.extensions.access_loggers.file.v3.FileAccessLog
              path: %[2]s
              log_format:
                json_format:
                  method: "%%REQ(:METHOD)%%"
                  authority: "%%REQ(:AUTHORITY)%%"
                  response_code: "%%RESPONSE_CODE%%"
                  flags: "%%RESPONSE_FLAGS%%"
                  bytes_sent: "%%BYTES_SENT%%"
                  bytes_received: "%%BYTES_RECEIVED%%"
                  duration_ms: "%%DURATION%%"
                  start_time: "%%START_TIME%%"
          internal_address_config:
            cidr_ranges: []
          upgrade_configs:
          - upgrade_type: CONNECT
          route_config:
            name: local_route
            virtual_hosts:
            - name: forward_proxy
              domains:
              - "*"
              routes:
              - match:
                  connect_matcher: {}
                route:
                  cluster: dynamic_forward_proxy_cluster
                  upgrade_configs:
                  - upgrade_type: CONNECT
                    connect_config: {}
              - match:
                  prefix: "/"
                route:
                  cluster: dynamic_forward_proxy_cluster
          http_filters:
%[3]s          - name: envoy.filters.http.dynamic_forward_proxy
            typed_config:
              "@type": type.googleapis.com/envoy.extensions.filters.http.dynamic_forward_proxy.v3.FilterConfig
              dns_cache_config:
                name: dynamic_forward_proxy_cache_config
                dns_lookup_family: V4_ONLY
          - name: envoy.filters.http.router
            typed_config:
              "@type": type.googleapis.com/envoy.extensions.filters.http.router.v3.Router
  clusters:
  - name: dynamic_forward_proxy_cluster
    lb_policy: CLUSTER_PROVIDED
    cluster_type:
      name: envoy.clusters.dynamic_forward_proxy
      typed_config:
        "@type": type.googleapis.com/envoy.extensions.clusters.dynamic_forward_proxy.v3.ClusterConfig
        dns_cache_config:
          name: dynamic_forward_proxy_cache_config
          dns_lookup_family: V4_ONLY
`, cfg.Port, AccessLogPath, rbacHTTPFilters(cfg))
}
