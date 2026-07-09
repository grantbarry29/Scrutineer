/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package envoy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
)

func logLine(authority string) string {
	return fmt.Sprintf(`{"method":"GET","authority":"%s","response_code":200,"flags":"-","start_time":"2026-07-01T05:00:00.000Z"}`+"\n", authority)
}

// capturingSubmit records every submitted batch and can be told to fail.
type capturingSubmit struct {
	batches [][]scrutineerv1alpha1.PolicyDecision
	fail    bool
}

func (c *capturingSubmit) submit(_ context.Context, decisions []scrutineerv1alpha1.PolicyDecision) error {
	if c.fail {
		return fmt.Errorf("reporter unavailable")
	}
	cp := append([]scrutineerv1alpha1.PolicyDecision(nil), decisions...)
	c.batches = append(c.batches, cp)
	return nil
}

func (c *capturingSubmit) targets() []string {
	var out []string
	for _, b := range c.batches {
		for _, d := range b {
			out = append(out, d.Target)
		}
	}
	return out
}

func newTestTailer(t *testing.T, sink *capturingSubmit) (*Tailer, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "access.json")
	return &Tailer{Path: path, Submit: sink.submit, BatchMax: 2}, path
}

func appendFile(t *testing.T, path, content string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
}

func TestTailer_batchesInOrder(t *testing.T) {
	sink := &capturingSubmit{}
	tailer, path := newTestTailer(t, sink)

	appendFile(t, path, logLine("a.example")+logLine("b.example")+logLine("c.example"))
	if err := tailer.Poll(context.Background()); err != nil {
		t.Fatalf("poll: %v", err)
	}

	// BatchMax=2 → two batches, order preserved.
	if len(sink.batches) != 2 || len(sink.batches[0]) != 2 || len(sink.batches[1]) != 1 {
		t.Fatalf("batches = %v", sink.batches)
	}
	got := sink.targets()
	want := []string{"a.example", "b.example", "c.example"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("targets = %v, want %v", got, want)
		}
	}

	// Nothing new → no further submissions.
	if err := tailer.Poll(context.Background()); err != nil {
		t.Fatalf("second poll: %v", err)
	}
	if len(sink.batches) != 2 {
		t.Fatalf("re-poll produced duplicates: %v", sink.batches)
	}
}

func TestTailer_holdsPartialLine(t *testing.T) {
	sink := &capturingSubmit{}
	tailer, path := newTestTailer(t, sink)

	full := logLine("done.example")
	appendFile(t, path, full[:20]) // no newline yet
	if err := tailer.Poll(context.Background()); err != nil {
		t.Fatalf("poll: %v", err)
	}
	if len(sink.batches) != 0 {
		t.Fatalf("partial line must not be submitted: %v", sink.batches)
	}

	appendFile(t, path, full[20:])
	if err := tailer.Poll(context.Background()); err != nil {
		t.Fatalf("poll: %v", err)
	}
	if got := sink.targets(); len(got) != 1 || got[0] != "done.example" {
		t.Fatalf("targets = %v", got)
	}
}

func TestTailer_retriesAfterSubmitFailureWithoutLossOrDup(t *testing.T) {
	sink := &capturingSubmit{fail: true}
	tailer, path := newTestTailer(t, sink)

	appendFile(t, path, logLine("keep.example"))
	if err := tailer.Poll(context.Background()); err == nil {
		t.Fatal("expected poll to surface the submit failure")
	}
	if len(sink.batches) != 0 {
		t.Fatalf("failed submit must not record a batch: %v", sink.batches)
	}

	sink.fail = false
	if err := tailer.Poll(context.Background()); err != nil {
		t.Fatalf("retry poll: %v", err)
	}
	if got := sink.targets(); len(got) != 1 || got[0] != "keep.example" {
		t.Fatalf("targets after retry = %v", got)
	}

	// And no re-delivery on the next poll.
	if err := tailer.Poll(context.Background()); err != nil {
		t.Fatalf("post-retry poll: %v", err)
	}
	if len(sink.targets()) != 1 {
		t.Fatalf("duplicate delivery: %v", sink.targets())
	}
}

func TestTailer_skipsMalformedLines(t *testing.T) {
	sink := &capturingSubmit{}
	tailer, path := newTestTailer(t, sink)

	appendFile(t, path, "not json at all\n"+logLine("ok.example")+`{"method":"GET"}`+"\n")
	if err := tailer.Poll(context.Background()); err != nil {
		t.Fatalf("poll: %v", err)
	}
	if got := sink.targets(); len(got) != 1 || got[0] != "ok.example" {
		t.Fatalf("targets = %v", got)
	}
}

// OnDecision fires once per queued decision and OnMalformed once per skipped line — the
// egress-reporter's instrumentation points (#55). Plain callbacks keep the Prometheus
// dependency out of this shared package (the manager imports it too).
func TestTailer_hooksObserveDecisionsAndMalformed(t *testing.T) {
	sink := &capturingSubmit{}
	tailer, path := newTestTailer(t, sink)
	var actions []string
	malformed := 0
	tailer.OnDecision = func(d scrutineerv1alpha1.PolicyDecision) { actions = append(actions, string(d.Action)) }
	tailer.OnMalformed = func() { malformed++ }

	appendFile(t, path, logLine("a.example")+"not-json\n"+logLine("b.example"))
	if err := tailer.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(actions) != 2 {
		t.Fatalf("OnDecision calls = %d, want 2 (%v)", len(actions), actions)
	}
	if malformed != 1 {
		t.Fatalf("OnMalformed calls = %d, want 1", malformed)
	}
}

func TestTailer_truncationResets(t *testing.T) {
	sink := &capturingSubmit{}
	tailer, path := newTestTailer(t, sink)

	appendFile(t, path, logLine("before.example"))
	if err := tailer.Poll(context.Background()); err != nil {
		t.Fatalf("poll: %v", err)
	}

	// Truncate + write fresh content shorter than the old offset.
	if err := os.WriteFile(path, []byte(logLine("after.example")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := tailer.Poll(context.Background()); err != nil {
		t.Fatalf("poll after truncate: %v", err)
	}
	got := sink.targets()
	if len(got) != 2 || got[1] != "after.example" {
		t.Fatalf("targets = %v", got)
	}
}

func TestTailer_missingFileIsNotAnError(t *testing.T) {
	sink := &capturingSubmit{}
	tailer, _ := newTestTailer(t, sink)
	// Envoy has not created the access log yet (no traffic): keep polling quietly.
	if err := tailer.Poll(context.Background()); err != nil {
		t.Fatalf("poll on missing file: %v", err)
	}
	if len(sink.batches) != 0 {
		t.Fatalf("batches = %v", sink.batches)
	}
}

// #97: catch-up over a file larger than one chunk must read in bounded chunks —
// per-cycle memory O(chunk), never O(file) — while preserving order and completeness.
// ChunkSize is set smaller than one log line, so every line spans chunk boundaries.
func TestTailer_multiChunkCatchUpSpanningChunkBoundaries(t *testing.T) {
	sink := &capturingSubmit{}
	tailer, path := newTestTailer(t, sink)
	tailer.BatchMax = 64
	tailer.ChunkSize = 48

	const n = 100
	var lines string
	want := make([]string, 0, n)
	for i := 0; i < n; i++ {
		h := fmt.Sprintf("h%03d.example", i)
		want = append(want, h)
		lines += logLine(h)
	}
	appendFile(t, path, lines)

	if err := tailer.Poll(context.Background()); err != nil {
		t.Fatalf("poll: %v", err)
	}
	got := sink.targets()
	if len(got) != n {
		t.Fatalf("delivered %d decisions, want %d", len(got), n)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("targets[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if tailer.Dropped() != 0 {
		t.Fatalf("dropped = %d, want 0", tailer.Dropped())
	}

	// No re-delivery once caught up.
	if err := tailer.Poll(context.Background()); err != nil {
		t.Fatalf("re-poll: %v", err)
	}
	if len(sink.targets()) != n {
		t.Fatalf("re-poll duplicated deliveries: %d, want %d", len(sink.targets()), n)
	}
}

// #97: reading is gated on delivery — while the reporter is down the tailer must not
// pull the backlog into the pending queue (the file, not memory, buffers it). With the
// old read-everything behavior a tiny MaxPending would drop most of this backlog; now
// nothing is lost.
func TestTailer_backlogWaitsInFileWhileSubmitFails(t *testing.T) {
	sink := &capturingSubmit{fail: true}
	tailer, path := newTestTailer(t, sink)
	tailer.ChunkSize = 64
	tailer.MaxPending = 4

	const n = 50
	var lines string
	want := make([]string, 0, n)
	for i := 0; i < n; i++ {
		h := fmt.Sprintf("h%03d.example", i)
		want = append(want, h)
		lines += logLine(h)
	}
	appendFile(t, path, lines)

	if err := tailer.Poll(context.Background()); err == nil {
		t.Fatal("expected poll to surface the submit failure")
	}
	if len(tailer.pending) > tailer.MaxPending {
		t.Fatalf("pending = %d decisions during outage, want <= MaxPending (%d)", len(tailer.pending), tailer.MaxPending)
	}

	sink.fail = false
	if err := tailer.Poll(context.Background()); err != nil {
		t.Fatalf("recovery poll: %v", err)
	}
	got := sink.targets()
	if len(got) != n {
		t.Fatalf("delivered %d decisions after recovery, want %d (none dropped)", len(got), n)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("targets[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if tailer.Dropped() != 0 {
		t.Fatalf("dropped = %d, want 0 (backlog must wait in the file)", tailer.Dropped())
	}
}

// #97: a newline-free run longer than maxPartialLine (corrupt log) must not accumulate
// unboundedly in the partial buffer — it is dropped and counted malformed, and tailing
// resumes at the next complete line.
func TestTailer_dropsOversizedPartialLine(t *testing.T) {
	sink := &capturingSubmit{}
	tailer, path := newTestTailer(t, sink)
	malformed := 0
	tailer.OnMalformed = func() { malformed++ }

	appendFile(t, path, strings.Repeat("x", maxPartialLine+1))
	if err := tailer.Poll(context.Background()); err != nil {
		t.Fatalf("poll over garbage: %v", err)
	}
	if len(tailer.partial) > maxPartialLine {
		t.Fatalf("partial buffer grew to %d bytes, want <= %d", len(tailer.partial), maxPartialLine)
	}
	if malformed == 0 {
		t.Fatal("oversized partial line was not counted malformed")
	}
	if len(sink.batches) != 0 {
		t.Fatalf("garbage produced deliveries: %v", sink.batches)
	}

	appendFile(t, path, "\n"+logLine("ok.example"))
	if err := tailer.Poll(context.Background()); err != nil {
		t.Fatalf("poll after newline: %v", err)
	}
	if got := sink.targets(); len(got) != 1 || got[0] != "ok.example" {
		t.Fatalf("targets = %v, want [ok.example]", got)
	}
}

func TestTailer_boundsPendingQueue(t *testing.T) {
	sink := &capturingSubmit{fail: true}
	tailer, path := newTestTailer(t, sink)
	tailer.MaxPending = 3

	var lines string
	for i := 0; i < 5; i++ {
		lines += logLine(fmt.Sprintf("h%d.example", i))
	}
	appendFile(t, path, lines)
	_ = tailer.Poll(context.Background()) // submit fails; queue capped at 3 (oldest dropped)

	sink.fail = false
	if err := tailer.Poll(context.Background()); err != nil {
		t.Fatalf("drain poll: %v", err)
	}
	got := sink.targets()
	want := []string{"h2.example", "h3.example", "h4.example"}
	if len(got) != len(want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("targets = %v, want %v (oldest beyond MaxPending dropped)", got, want)
		}
	}
	if tailer.Dropped() != 2 {
		t.Fatalf("dropped = %d, want 2", tailer.Dropped())
	}
}
