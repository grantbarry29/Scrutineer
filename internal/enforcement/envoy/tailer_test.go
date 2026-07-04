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
