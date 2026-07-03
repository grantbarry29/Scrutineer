/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package lockverify

import (
	"fmt"
	"net"
	"time"
)

const (
	// dialTimeout bounds a single TCP connection attempt. A NetworkPolicy drop is
	// silent (no RST), so the timeout is how a blocked attempt manifests.
	dialTimeout = 3 * time.Second
	// settleWindow is how long the probe keeps dialing before reporting a verdict.
	// A newly-scheduled pod can dial before the CNI finishes programming an existing
	// egress policy, so a single immediate dial races enforcement and would falsely
	// report "connected". Dialing across this window and reporting the STEADY STATE
	// (the final attempt) lets programming settle first — the same reason the
	// networking e2e probe waits for its deny-all to take effect.
	settleWindow = 20 * time.Second
	// dialInterval spaces attempts within the settle window.
	dialInterval = 2 * time.Second
)

// RunProbe is the in-pod half of the differential probe. It repeatedly dials target
// (host:port) across the settle window and returns the STEADY-STATE outcome: nil if
// the final attempt connected (reachable), a non-nil error if it was blocked. The
// process exit code (0 / 1) is what the verifier reads back per pod.
//
// Steady state — not first-attempt — is deliberate: under a deny-all egress policy an
// enforcing CNI may still let the pod's very first dial through before the policy is
// programmed; only the settled result distinguishes "enforced" from "not enforced".
func RunProbe(target string, window time.Duration) error {
	if window <= 0 {
		window = settleWindow
	}
	deadline := time.Now().Add(window)
	var lastErr error
	for {
		lastErr = dialOnce(target)
		if time.Now().After(deadline) {
			return lastErr
		}
		time.Sleep(dialInterval)
	}
}

func dialOnce(target string) error {
	conn, err := net.DialTimeout("tcp", target, dialTimeout)
	if err != nil {
		return fmt.Errorf("probe dial %s: %w", target, err)
	}
	_ = conn.Close()
	return nil
}
