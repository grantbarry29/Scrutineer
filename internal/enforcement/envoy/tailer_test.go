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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/enforcement/reporterclient"
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

// #96: batches must be capped by encoded bytes as well as count — a 128-decision batch
// of long-target decisions can cross the reporter's 64KiB body cap.
func TestTailer_flushCapsBatchesByBytes(t *testing.T) {
	sink := &capturingSubmit{}
	tailer, path := newTestTailer(t, sink)
	tailer.BatchMax = 100
	tailer.BatchMaxBytes = 2048

	const n = 10
	var lines string
	for i := 0; i < n; i++ {
		lines += logLine(fmt.Sprintf("h%02d.%s.example", i, strings.Repeat("x", 300)))
	}
	appendFile(t, path, lines)
	if err := tailer.Poll(context.Background()); err != nil {
		t.Fatalf("poll: %v", err)
	}

	if len(sink.targets()) != n {
		t.Fatalf("delivered %d decisions, want %d", len(sink.targets()), n)
	}
	if len(sink.batches) < 2 {
		t.Fatalf("expected byte cap to split into multiple batches, got %d", len(sink.batches))
	}
	for i, b := range sink.batches {
		size := 0
		for _, d := range b {
			enc, err := json.Marshal(d)
			if err != nil {
				t.Fatal(err)
			}
			size += len(enc) + 1
		}
		if size > tailer.BatchMaxBytes {
			t.Fatalf("batch %d encodes to %d bytes, want <= %d", i, size, tailer.BatchMaxBytes)
		}
	}
}

// statusSubmit fails with a given HTTP status error until a predicate stops matching.
type statusSubmit struct {
	inner  capturingSubmit
	status int
	// failWhen decides whether a batch gets the status error.
	failWhen func(decisions []scrutineerv1alpha1.PolicyDecision) bool
}

func (s *statusSubmit) submit(ctx context.Context, decisions []scrutineerv1alpha1.PolicyDecision) error {
	if s.failWhen != nil && s.failWhen(decisions) {
		return fmt.Errorf("submit: %w", &reporterclient.StatusError{StatusCode: s.status})
	}
	return s.inner.submit(ctx, decisions)
}

// #96: a 413 on a multi-decision batch splits; a 413 on a single decision is poison —
// dropped and counted, never retried forever. The pipeline must keep flowing.
func TestTailer_splitsOn413AndDropsPoisonDecision(t *testing.T) {
	sink := &statusSubmit{status: 413}
	poison := "poison.example"
	sink.failWhen = func(ds []scrutineerv1alpha1.PolicyDecision) bool {
		if len(ds) > 1 {
			return true // force splitting all the way down to singles
		}
		return ds[0].Target == poison
	}

	path := filepath.Join(t.TempDir(), "access.json")
	var rejected []int
	tailer := &Tailer{Path: path, Submit: sink.submit, BatchMax: 4,
		OnRejected: func(count, status int) { rejected = append(rejected, count, status) }}

	appendFile(t, path, logLine("a.example")+logLine(poison)+logLine("b.example"))
	if err := tailer.Poll(context.Background()); err != nil {
		t.Fatalf("poll: %v", err)
	}

	got := sink.inner.targets()
	want := []string{"a.example", "b.example"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("targets = %v, want %v", got, want)
	}
	if len(rejected) != 2 || rejected[0] != 1 || rejected[1] != 413 {
		t.Fatalf("OnRejected calls = %v, want [1 413]", rejected)
	}
}

// #96 / contract §4.4: 404 means the AgentSession is gone — drop the report, do not
// wedge. Delivery resumes if the rejection clears (not typical for 404, but flush must
// not remember).
func TestTailer_dropsBatchOnSessionNotFound(t *testing.T) {
	sink := &statusSubmit{status: 404, failWhen: func([]scrutineerv1alpha1.PolicyDecision) bool { return true }}
	path := filepath.Join(t.TempDir(), "access.json")
	dropped := 0
	tailer := &Tailer{Path: path, Submit: sink.submit, BatchMax: 2,
		OnRejected: func(count, _ int) { dropped += count }}

	appendFile(t, path, logLine("a.example")+logLine("b.example")+logLine("c.example"))
	if err := tailer.Poll(context.Background()); err != nil {
		t.Fatalf("poll must not surface a permanent rejection as retryable: %v", err)
	}
	if len(sink.inner.batches) != 0 {
		t.Fatalf("unexpected deliveries: %v", sink.inner.batches)
	}
	if dropped != 3 {
		t.Fatalf("dropped = %d, want 3", dropped)
	}
	if err := tailer.Poll(context.Background()); err != nil {
		t.Fatalf("re-poll: %v", err)
	}
	if dropped != 3 {
		t.Fatalf("re-poll re-processed dropped decisions: %d", dropped)
	}
}

// #96: 5xx and 429 stay transient — pending is retained and retried, at-least-once.
func TestTailer_transientStatusKeepsPending(t *testing.T) {
	for _, status := range []int{500, 429} {
		sink := &statusSubmit{status: status, failWhen: func([]scrutineerv1alpha1.PolicyDecision) bool { return true }}
		path := filepath.Join(t.TempDir(), "access.json")
		tailer := &Tailer{Path: path, Submit: sink.submit}

		appendFile(t, path, logLine("keep.example"))
		if err := tailer.Poll(context.Background()); err == nil {
			t.Fatalf("status %d: expected poll to surface the transient error", status)
		}

		sink.failWhen = nil
		if err := tailer.Poll(context.Background()); err != nil {
			t.Fatalf("status %d: retry poll: %v", status, err)
		}
		if got := sink.inner.targets(); len(got) != 1 || got[0] != "keep.example" {
			t.Fatalf("status %d: targets after retry = %v", status, got)
		}
	}
}

// fakeEnvoyWriter simulates Envoy's access-log writer: a kept-open O_APPEND fd that
// keeps writing to the same inode across a rename (exactly what Envoy does until
// /reopen_logs), and a reopen() that closes and re-opens the configured path (what
// Envoy does when the admin endpoint is hit).
type fakeEnvoyWriter struct {
	t       *testing.T
	path    string
	f       *os.File
	reopens int
}

func newFakeEnvoyWriter(t *testing.T, path string) *fakeEnvoyWriter {
	t.Helper()
	w := &fakeEnvoyWriter{t: t, path: path}
	if err := w.reopen(context.Background()); err != nil {
		t.Fatal(err)
	}
	w.reopens = 0
	return w
}

func (w *fakeEnvoyWriter) write(line string) {
	w.t.Helper()
	if _, err := w.f.WriteString(line); err != nil {
		w.t.Fatal(err)
	}
}

func (w *fakeEnvoyWriter) reopen(context.Context) error {
	if w.f != nil {
		_ = w.f.Close()
	}
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	w.f = f
	w.reopens++
	return nil
}

// #98: sustained traffic beyond the rotate threshold must rotate the ingested log away
// (rename → reopen → drain → delete) with full evidence continuity — every line
// delivered exactly once, in order, across multiple rotation cycles.
func TestTailer_rotatesIngestedLogWithEvidenceContinuity(t *testing.T) {
	sink := &capturingSubmit{}
	path := filepath.Join(t.TempDir(), "access.json")
	envoy := newFakeEnvoyWriter(t, path)
	rotations := 0
	tailer := &Tailer{
		Path:             path,
		Submit:           sink.submit,
		RotateAfterBytes: 300, // ~3 lines
		Reopen:           envoy.reopen,
		OnRotated:        func() { rotations++ },
	}

	const n = 30
	want := make([]string, 0, n)
	for i := 0; i < n; i++ {
		h := fmt.Sprintf("h%03d.example", i)
		want = append(want, h)
		envoy.write(logLine(h))
		if err := tailer.Poll(context.Background()); err != nil {
			t.Fatalf("poll %d: %v", i, err)
		}
	}
	// Drain any in-flight rotation to a stable end state.
	for i := 0; i < 6; i++ {
		if err := tailer.Poll(context.Background()); err != nil {
			t.Fatalf("drain poll: %v", err)
		}
	}

	got := sink.targets()
	if len(got) != n {
		t.Fatalf("delivered %d decisions, want %d (loss or duplication across rotation)", len(got), n)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("targets[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if rotations == 0 {
		t.Fatal("expected at least one rotation cycle")
	}
	if _, err := os.Stat(path + rotatingSuffix); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rotated file left behind: %v", err)
	}
	if info, err := os.Stat(path); err != nil || info.Size() >= 30*int64(len(logLine("h000.example"))) {
		t.Fatalf("live log was not rotated away (err=%v)", err)
	}
}

// #98: rotation is gated on ingest — while the reporter is down, nothing may be renamed
// or deleted no matter how large the log grows (an agent flooding the log must not be
// able to discard un-ingested evidence; overflow beyond ingest still fails closed).
func TestTailer_neverRotatesUningestedEvidence(t *testing.T) {
	sink := &statusSubmit{status: 500, failWhen: func([]scrutineerv1alpha1.PolicyDecision) bool { return true }}
	path := filepath.Join(t.TempDir(), "access.json")
	envoy := newFakeEnvoyWriter(t, path)
	tailer := &Tailer{
		Path:             path,
		Submit:           sink.submit,
		RotateAfterBytes: 100,
		Reopen:           envoy.reopen,
	}

	const n = 20
	want := make([]string, 0, n)
	for i := 0; i < n; i++ {
		h := fmt.Sprintf("h%03d.example", i)
		want = append(want, h)
		envoy.write(logLine(h))
		_ = tailer.Poll(context.Background()) // reporter down: polls error
	}
	if _, err := os.Stat(path + rotatingSuffix); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("rotation started while evidence was un-ingested")
	}
	if envoy.reopens != 0 {
		t.Fatalf("reopen called %d times during reporter outage", envoy.reopens)
	}

	// Reporter recovers: everything is delivered, then rotation may proceed.
	sink.failWhen = nil
	for i := 0; i < 6; i++ {
		if err := tailer.Poll(context.Background()); err != nil {
			t.Fatalf("recovery poll: %v", err)
		}
	}
	got := sink.inner.targets()
	if len(got) != n {
		t.Fatalf("delivered %d decisions after recovery, want %d", len(got), n)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("targets[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// #98: a reporter restart mid-rotation (offset lost, .rotating on disk, live file already
// reopened by Envoy) must drain the rotated remainder first, then the live file — order
// preserved, rotated file removed.
func TestTailer_recoversRotationAfterRestart(t *testing.T) {
	sink := &capturingSubmit{}
	path := filepath.Join(t.TempDir(), "access.json")
	appendFile(t, path+rotatingSuffix, logLine("old-a.example")+logLine("old-b.example"))
	appendFile(t, path, logLine("new-c.example"))

	tailer := &Tailer{Path: path, Submit: sink.submit} // fresh: offset 0, like a restart
	for i := 0; i < 6; i++ {
		if err := tailer.Poll(context.Background()); err != nil {
			t.Fatalf("poll: %v", err)
		}
	}

	got := sink.targets()
	want := []string{"old-a.example", "old-b.example", "new-c.example"}
	if len(got) != len(want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("targets[%d] = %q, want %q (rotated remainder must drain first)", i, got[i], want[i])
		}
	}
	if _, err := os.Stat(path + rotatingSuffix); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("rotated file not cleaned up after recovery")
	}
}

// #98: a crash after rename but before Envoy reopened (no live file) must retry the
// reopen so the proxy resumes logging, then finish the rotation.
func TestTailer_retriesReopenWhenLiveFileMissing(t *testing.T) {
	sink := &capturingSubmit{}
	path := filepath.Join(t.TempDir(), "access.json")
	appendFile(t, path+rotatingSuffix, logLine("stranded.example"))

	reopens := 0
	tailer := &Tailer{Path: path, Submit: sink.submit,
		Reopen: func(context.Context) error {
			reopens++
			return os.WriteFile(path, nil, 0o644) // Envoy recreates the live file
		}}
	for i := 0; i < 6; i++ {
		if err := tailer.Poll(context.Background()); err != nil {
			t.Fatalf("poll: %v", err)
		}
	}

	if reopens == 0 {
		t.Fatal("expected the tailer to retry the reopen")
	}
	if got := sink.targets(); len(got) != 1 || got[0] != "stranded.example" {
		t.Fatalf("targets = %v, want [stranded.example]", got)
	}
	if _, err := os.Stat(path + rotatingSuffix); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("rotated file not cleaned up")
	}
}

// #98: without a Reopen hook rotation is disabled entirely — the tailer must never
// rename or delete the log it cannot ask Envoy to recreate.
func TestTailer_noRotationWithoutReopen(t *testing.T) {
	sink := &capturingSubmit{}
	tailer, path := newTestTailer(t, sink)
	tailer.RotateAfterBytes = 50

	appendFile(t, path, logLine("a.example")+logLine("b.example"))
	for i := 0; i < 3; i++ {
		if err := tailer.Poll(context.Background()); err != nil {
			t.Fatalf("poll: %v", err)
		}
	}
	if _, err := os.Stat(path + rotatingSuffix); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("rotation ran without a Reopen hook")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("live log must be untouched: %v", err)
	}
}

// #100: a 429 is flow control, not failure. The tailer honors the server's
// Retry-After hint — it retries the same batch after the hinted wait and keeps that
// pace for the rest of the flush — so a multi-batch backlog drains at the reporter's
// sustained rate with a single 429, no evidence loss, and no error surfaced.
func TestTailer_honorsRetryAfterAndFallsToServerPace(t *testing.T) {
	sink := &capturingSubmit{}
	tailer, path := newTestTailer(t, sink)
	tailer.BatchMax = 1
	rejected := 0
	tailer.OnRejected = func(int, int) { rejected++ }

	const hint = 20 * time.Millisecond
	var calls []time.Time
	limited := true
	tailer.Submit = func(ctx context.Context, decisions []scrutineerv1alpha1.PolicyDecision) error {
		calls = append(calls, time.Now())
		if limited {
			limited = false
			return &reporterclient.StatusError{StatusCode: http.StatusTooManyRequests, RetryAfter: hint}
		}
		return sink.submit(ctx, decisions)
	}
	appendFile(t, path, logLine("a.example")+logLine("b.example")+logLine("c.example"))

	if err := tailer.Poll(context.Background()); err != nil {
		t.Fatalf("poll must absorb a honored 429, got: %v", err)
	}
	want := []string{"a.example", "b.example", "c.example"}
	if got := sink.targets(); !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %v, want %v (exactly once, in order)", got, want)
	}
	if rejected != 0 {
		t.Fatalf("OnRejected fired %d times; 429 must never count as a permanent rejection", rejected)
	}
	if len(calls) != 4 {
		t.Fatalf("submit calls = %d, want 4 (1 rate-limited + 3 delivered)", len(calls))
	}
	// Every call after the 429 — the retry AND the following batches — is paced.
	for i := 1; i < len(calls); i++ {
		if gap := calls[i].Sub(calls[i-1]); gap < hint {
			t.Fatalf("call %d came %v after call %d, want ≥ %v (Retry-After pacing)", i+1, gap, i, hint)
		}
	}
}

// A reporter that keeps 429ing after its own Retry-After was honored is wrong or
// overloaded; the tailer must not spin in flush forever. After a bounded number of
// consecutive 429s for the same batch it surfaces the error like any transient
// failure — pending is retained and the next healthy poll delivers everything.
func TestTailer_persistent429SurfacesAsTransientAfterBoundedRetries(t *testing.T) {
	sink := &capturingSubmit{}
	tailer, path := newTestTailer(t, sink)
	rejected := 0
	tailer.OnRejected = func(int, int) { rejected++ }

	attempts := 0
	limited := true
	tailer.Submit = func(ctx context.Context, decisions []scrutineerv1alpha1.PolicyDecision) error {
		if limited {
			attempts++
			return &reporterclient.StatusError{StatusCode: http.StatusTooManyRequests, RetryAfter: time.Millisecond}
		}
		return sink.submit(ctx, decisions)
	}
	appendFile(t, path, logLine("a.example"))

	err := tailer.Poll(context.Background())
	var se *reporterclient.StatusError
	if !errors.As(err, &se) || se.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("poll err = %v, want the persistent 429 surfaced", err)
	}
	if attempts != 1+maxRateLimitRetries {
		t.Fatalf("submit attempts = %d, want %d (initial + bounded retries)", attempts, 1+maxRateLimitRetries)
	}
	if rejected != 0 {
		t.Fatalf("OnRejected fired %d times; 429 must never drop evidence", rejected)
	}

	limited = false
	if err := tailer.Poll(context.Background()); err != nil {
		t.Fatalf("recovery poll: %v", err)
	}
	if got := sink.targets(); !reflect.DeepEqual(got, []string{"a.example"}) {
		t.Fatalf("targets = %v, want [a.example] (retained across the 429s)", got)
	}
}

// The final drain on SIGTERM runs under a short deadline; honoring a long
// Retry-After must not outlive the context.
func TestTailer_rateLimitPacingRespectsContext(t *testing.T) {
	sink := &capturingSubmit{}
	tailer, path := newTestTailer(t, sink)
	tailer.Submit = func(context.Context, []scrutineerv1alpha1.PolicyDecision) error {
		return &reporterclient.StatusError{StatusCode: http.StatusTooManyRequests, RetryAfter: time.Minute}
	}
	appendFile(t, path, logLine("a.example"))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := tailer.Poll(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("poll err = %v, want context deadline", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("poll blocked %v honoring Retry-After after the context expired", elapsed)
	}
}

// #105: Dropped() is read by the Prometheus scrape goroutine (CounterFunc in
// cmd/egress-reporter) while queue() increments the counter from the poll goroutine.
// This test makes the two overlap so `go test -race` (the CI unit tier) proves the
// counter is synchronized.
func TestTailer_droppedIsRaceFreeUnderConcurrentScrape(t *testing.T) {
	sink := &capturingSubmit{}
	tailer, path := newTestTailer(t, sink)
	tailer.MaxPending = 2 // a single chunk overflows the queue → drops during Poll

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // the metrics scrape
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = tailer.Dropped()
			}
		}
	}()

	for round := 0; round < 10; round++ {
		var lines strings.Builder
		for i := 0; i < 20; i++ {
			lines.WriteString(logLine(fmt.Sprintf("r%d-h%d.example", round, i)))
		}
		appendFile(t, path, lines.String())
		if err := tailer.Poll(context.Background()); err != nil {
			t.Fatalf("poll %d: %v", round, err)
		}
	}
	close(stop)
	wg.Wait()

	if tailer.Dropped() == 0 {
		t.Fatal("expected queue-overflow drops with MaxPending=2")
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
