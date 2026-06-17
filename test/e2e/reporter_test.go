//go:build e2e

/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
	"github.com/secureai/relay/internal/reporter"
)

// stubVerifier bypasses TokenReview/pod-ownership auth so the e2e spec can drive
// the reporter HTTP -> status path against the real apiserver. The auth path
// itself is covered by unit tests (internal/reporter).
type stubVerifier struct{}

func (stubVerifier) Verify(_ context.Context, _ *http.Request, s reporter.SessionRef) (reporter.CallerIdentity, error) {
	return reporter.CallerIdentity{Namespace: s.Namespace, PodName: "stub-pod"}, nil
}

var _ = Describe("Runtime reporter e2e against kind", func() {
	It("ingests a runtime report and populates status decisions, violations, and events", func(ctx SpecContext) {
		ns := newTestNamespace("relay-e2e-reporter")
		session := newAgentSession(ns, "reporter", withLongRunningCommand())
		key := createAgentSession(ctx, session)

		// Session must exist with a Job before reporting; wait until Running.
		expectJobForSession(ctx, ns, session)

		By("standing up the reporter handler in front of the live cluster")
		h := &reporter.Handler{
			Writer:   k8sClient.Status(),
			Reader:   k8sClient,
			Verifier: stubVerifier{},
		}
		srv := httptest.NewServer(h)
		DeferCleanup(srv.Close)

		// A fixed timestamp makes re-delivery idempotent by design (the dedup key
		// for decisions/violations includes the observation time).
		obsTime := metav1.NewTime(time.Unix(1_700_000_000, 0))
		report := reporter.ReportRequest{
			Session: reporter.SessionRef{Namespace: ns, Name: session.Name},
			Backend: "egress-proxy",
			Decisions: []relayv1alpha1.PolicyDecision{{
				Time:    obsTime,
				Phase:   relayv1alpha1.PolicyDecisionPhaseRuntime,
				Type:    "network",
				Action:  relayv1alpha1.PolicyDecisionDeny,
				Reason:  "DeniedDomain",
				Target:  "evil.example.com",
				Message: "egress to evil.example.com blocked",
			}},
			Events: []relayv1alpha1.SessionEvent{{
				Time:    obsTime,
				Type:    relayv1alpha1.SessionEventTypeNetwork,
				Source:  "egress-proxy",
				Action:  "deny",
				Target:  "evil.example.com",
				Message: "egress blocked",
				EventID: "evt-e2e-1",
			}},
		}

		By("posting the report twice to prove idempotency")
		postRuntimeReport(ctx, srv.URL, report)
		postRuntimeReport(ctx, srv.URL, report)

		Eventually(func(g Gomega) {
			got := getSession(ctx, key)
			// The reconciler may also record merge-time decisions; assert on the
			// runtime-phase evidence specifically rather than total counts.
			var runtimeDecisions []relayv1alpha1.PolicyDecision
			for _, d := range got.Status.PolicyDecisions {
				if d.Phase == relayv1alpha1.PolicyDecisionPhaseRuntime && d.Target == "evil.example.com" {
					runtimeDecisions = append(runtimeDecisions, d)
				}
			}
			g.Expect(runtimeDecisions).To(HaveLen(1))
			g.Expect(got.Status.Violations).To(HaveLen(1))
			g.Expect(got.Status.Events).To(HaveLen(1))
			g.Expect(got.Status.Events[0].EventID).To(Equal("evt-e2e-1"))
		}, 20*time.Second, 500*time.Millisecond).Should(Succeed())

		// Cancel to free the long-running Job.
		requestCancellation(ctx, key)
		waitForTerminalPhase(ctx, key, relayv1alpha1.PhaseCancelled)
	})

	It("rejects a report for a non-existent session with 404", func(ctx SpecContext) {
		ns := newTestNamespace("relay-e2e-reporter-404")
		h := &reporter.Handler{Writer: k8sClient.Status(), Reader: k8sClient, Verifier: stubVerifier{}}
		srv := httptest.NewServer(h)
		DeferCleanup(srv.Close)

		body, err := json.Marshal(reporter.ReportRequest{
			Session: reporter.SessionRef{Namespace: ns, Name: "missing"},
			Backend: "egress-proxy",
			Decisions: []relayv1alpha1.PolicyDecision{{
				Phase: relayv1alpha1.PolicyDecisionPhaseRuntime, Type: "network",
				Action: relayv1alpha1.PolicyDecisionDeny, Reason: "x",
			}},
		})
		Expect(err).NotTo(HaveOccurred())

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/v1/report", bytes.NewReader(body))
		Expect(err).NotTo(HaveOccurred())
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer stub")
		resp, err := http.DefaultClient.Do(req)
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		Expect(resp.StatusCode).To(Equal(http.StatusNotFound))
	})
})

func postRuntimeReport(ctx context.Context, baseURL string, report reporter.ReportRequest) {
	GinkgoHelper()
	body, err := json.Marshal(report)
	Expect(err).NotTo(HaveOccurred())

	// Reporter status merges race with the reconciler; the handler returns 409 when
	// optimistic concurrency retries are exhausted — retry the POST like a real sidecar.
	Eventually(func(g Gomega) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/report", bytes.NewReader(body))
		g.Expect(err).NotTo(HaveOccurred())
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer stub")

		resp, err := http.DefaultClient.Do(req)
		g.Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		g.Expect(resp.StatusCode).To(Equal(http.StatusAccepted), "reporter returned %d", resp.StatusCode)
	}, 15*time.Second, 200*time.Millisecond).Should(Succeed())
}
