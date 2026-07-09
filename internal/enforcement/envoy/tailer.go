/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package envoy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/enforcement/reporterclient"
)

// Tailer defaults; BatchMax mirrors the reporter's MaxDecisionsPerReport cap.
const (
	DefaultBatchMax     = 128
	DefaultPollInterval = 2 * time.Second
	DefaultMaxPending   = 4096
	// DefaultBatchMaxBytes caps a batch by encoded size (#96): the reporter rejects
	// bodies over MaxReportBytes (64KiB, internal/reporter/types.go — keep in sync);
	// 48KiB leaves headroom for the request envelope and JSON scaffolding, so a
	// count-capped batch of long-target decisions can never draw a 413.
	DefaultBatchMaxBytes = 48 << 10
	// batchEnvelopeBytes overestimates the non-decision part of a report body
	// (session ref ≤ ~320B of names, backend, field names).
	batchEnvelopeBytes = 1 << 10
	// DefaultChunkSize bounds a single read from the access log. Catch-up (e.g. a
	// restart re-reading the whole file, #97) happens chunk-by-chunk with a flush
	// between chunks, so memory stays O(chunk), never O(file) — the log can reach its
	// 256Mi emptyDir sizeLimit while this container is capped at 128Mi (resources.go).
	// 256KiB is well under a thousand access-log lines, comfortably inside MaxPending.
	DefaultChunkSize = 256 << 10
	// maxPartialLine caps the buffered tail of an incomplete line. A well-formed access
	// log line is a few hundred bytes; a newline-free run this long is a corrupt log,
	// and buffering it unboundedly would recreate the O(file) memory path.
	maxPartialLine    = 1 << 20
	finalDrainTimeout = 5 * time.Second

	// DefaultRotateAfterBytes is the live-log size that triggers a rotation cycle (#98)
	// — well under the 256Mi emptyDir cap so the remainder + fresh growth during the
	// drain fit comfortably. Rotation only ever removes fully-ingested bytes; overflow
	// beyond what the reporter can ingest still evicts the pod (fail closed).
	DefaultRotateAfterBytes = 64 << 20
	// rotatingSuffix names the renamed, being-drained log during a rotation cycle.
	rotatingSuffix = ".rotating"
)

// Tailer incrementally reads Envoy's JSON access log and submits parsed egress decisions
// to the reporter. It is the engine of the egress-reporter container (Slice C, #62):
// evidence flows access.json → ParseAccessLogLine → Submit batches.
//
// Delivery is at-least-once: the in-memory offset is lost on container restart, so the
// whole file is re-read and re-submitted — the controller's status merge dedups decisions
// by key (time is pinned from %START_TIME%, so re-delivered records are identical).
// Reads are bounded and gated on delivery (#97): each cycle alternates a ≤ChunkSize read
// with a flush, so a backlog — restart catch-up or a reporter outage — waits in the file,
// not in memory. A transiently failed Submit keeps the batch pending and retries next
// poll; permanent reporter rejections (contract §4.4: 400/403/404/413) split or drop the
// offending decisions instead of wedging the queue head (#96, counted via OnRejected).
// The pending queue is additionally bounded by MaxPending, dropping the OLDEST entries
// beyond it (logged and counted).
type Tailer struct {
	// Path is the access-log file (AccessLogPath in the shared emptyDir).
	Path string
	// Submit delivers one batch of decisions (≤ BatchMax) to the reporter.
	Submit func(ctx context.Context, decisions []scrutineerv1alpha1.PolicyDecision) error
	// BatchMax caps decisions per Submit call. Defaults to DefaultBatchMax.
	BatchMax int
	// PollInterval is the Run loop cadence. Defaults to DefaultPollInterval.
	PollInterval time.Duration
	// MaxPending bounds the undelivered-decision queue. Defaults to DefaultMaxPending.
	MaxPending int
	// ChunkSize caps the bytes pulled from the access log per read. Defaults to
	// DefaultChunkSize.
	ChunkSize int
	// BatchMaxBytes caps the encoded size of one Submit batch. Defaults to
	// DefaultBatchMaxBytes.
	BatchMaxBytes int
	// OnRejected, if set, observes decisions dropped after a permanent reporter
	// rejection (contract §4.4: 400/403/404/413) — evidence lost, by HTTP status.
	OnRejected func(count, httpStatus int)
	// RotateAfterBytes triggers a rotation cycle once the fully-ingested live log
	// reaches this size (#98). Defaults to DefaultRotateAfterBytes. Rotation runs only
	// when Reopen is set.
	RotateAfterBytes int64
	// Reopen asks Envoy to reopen its access log (admin POST /reopen_logs) so a renamed
	// log is replaced by a fresh file at Path. Nil disables starting rotations — the
	// log then grows to the emptyDir cap and overflow evicts the pod (the fail-closed
	// pre-rotation posture).
	Reopen func(context.Context) error
	// OnRotated, if set, observes each completed rotation cycle (metrics hook).
	OnRotated func()
	// Policy is the effective FQDN policy each observed authority is classified against
	// (#32). Zero value classifies everything as allow.
	Policy EgressPolicy
	// OnDecision, if set, observes each decision as it is queued (metrics hook, #55).
	// Plain callbacks keep the Prometheus dependency out of this shared package.
	OnDecision func(scrutineerv1alpha1.PolicyDecision)
	// OnMalformed, if set, observes each skipped malformed access-log line (#55).
	OnMalformed func()

	offset  int64
	partial []byte
	pending []scrutineerv1alpha1.PolicyDecision
	dropped int64

	// rotating: the tailer is draining Path+rotatingSuffix (offset/partial refer to that
	// file) before deleting it and switching back to Path. rotateStable: the remainder
	// was already fully drained on an earlier poll — the one-cycle grace for Envoy's log
	// flusher to land writes still buffered for the renamed file's fd.
	rotating     bool
	rotateStable bool
}

// Dropped reports how many parsed decisions were discarded because the pending queue
// overflowed MaxPending (i.e. evidence lost to a prolonged reporter outage).
func (t *Tailer) Dropped() int64 { return t.dropped }

// Poll performs one catch-up cycle: it first retries any decisions left pending by an
// earlier failed Submit, then alternates bounded chunk reads with flushes until EOF or
// a Submit failure. Gating each read on the previous flush means the tailer never holds
// more than roughly one chunk's decisions in memory — the access log itself buffers any
// backlog. A missing file is not an error (Envoy has not received traffic yet). A Submit
// failure is returned after retaining the batch for the next cycle. Once fully drained,
// rotation state advances (#98): a live log past the threshold is renamed and Envoy
// reopened; a renamed remainder that has stayed drained is deleted and tailing switches
// back to the fresh live file.
func (t *Tailer) Poll(ctx context.Context) error {
	if err := t.flush(ctx); err != nil {
		return err
	}
	t.detectRotationInProgress()
	for {
		more, err := t.readChunk()
		if err != nil {
			return err
		}
		if err := t.flush(ctx); err != nil {
			return err
		}
		if more {
			t.rotateStable = false
			continue
		}
		// Fully drained: EOF reached and nothing pending.
		if t.rotating {
			if t.finishRotation(ctx) {
				continue // switched to the live file; drain it too
			}
			return nil
		}
		if t.startRotation(ctx) {
			continue // now draining the renamed remainder
		}
		return nil
	}
}

// Run polls until ctx is cancelled, then makes a final best-effort drain so evidence
// written just before session teardown still reaches the reporter.
func (t *Tailer) Run(ctx context.Context) {
	interval := t.PollInterval
	if interval <= 0 {
		interval = DefaultPollInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			drainCtx, cancel := context.WithTimeout(context.Background(), finalDrainTimeout)
			defer cancel()
			if err := t.Poll(drainCtx); err != nil {
				log.Printf("egress-reporter: final drain: %v", err)
			}
			return
		case <-ticker.C:
			if err := t.Poll(ctx); err != nil {
				log.Printf("egress-reporter: poll: %v", err)
			}
		}
	}
}

// readChunk reads at most ChunkSize bytes appended since the last read, parses complete
// lines, and queues their decisions. Partial trailing lines are buffered (bounded by
// maxPartialLine) until completed. A file shorter than the current offset means
// truncation/recreation: restart from the beginning. The returned more reports whether
// the chunk was full, i.e. further bytes may remain past the offset.
func (t *Tailer) readChunk() (more bool, err error) {
	chunkSize := t.ChunkSize
	if chunkSize <= 0 {
		chunkSize = DefaultChunkSize
	}

	f, err := os.Open(t.activePath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return false, err
	}
	if info.Size() < t.offset {
		t.offset = 0
		t.partial = nil
	}
	if info.Size() == t.offset {
		return false, nil
	}

	if _, err := f.Seek(t.offset, io.SeekStart); err != nil {
		return false, err
	}
	buf := make([]byte, chunkSize)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return false, err
	}
	buf = buf[:n]
	t.offset += int64(n)

	data := append(t.partial, buf...)
	for {
		nl := bytes.IndexByte(data, '\n')
		if nl < 0 {
			break
		}
		line := bytes.TrimSpace(data[:nl])
		data = data[nl+1:]
		if len(line) == 0 {
			continue
		}
		entry, err := ParseAccessLogLine(line)
		if err != nil {
			log.Printf("egress-reporter: skipping malformed access-log line: %v", err)
			if t.OnMalformed != nil {
				t.OnMalformed()
			}
			continue
		}
		d := entry.Decision(t.Policy)
		if t.OnDecision != nil {
			t.OnDecision(d)
		}
		t.queue(d)
	}
	switch {
	case len(data) > maxPartialLine:
		log.Printf("egress-reporter: dropping oversized partial access-log line (%d bytes; corrupt log?)", len(data))
		if t.OnMalformed != nil {
			t.OnMalformed()
		}
		t.partial = nil
	case len(data) == 0:
		t.partial = nil
	default:
		// Copy the tail so the retained partial line does not pin the chunk buffer.
		t.partial = append([]byte(nil), data...)
	}
	return n == chunkSize, nil
}

// activePath is the file the offset/partial state refers to: the renamed remainder
// while a rotation cycle is draining, the live log otherwise.
func (t *Tailer) activePath() string {
	if t.rotating {
		return t.Path + rotatingSuffix
	}
	return t.Path
}

// detectRotationInProgress resumes a rotation a restart left on disk: a leftover
// renamed file is drained before the live log. In-memory offsets were lost with the
// restart, so the remainder is re-read from zero — at-least-once, absorbed by the
// server-side dedup like any other restart re-delivery.
func (t *Tailer) detectRotationInProgress() {
	if t.rotating {
		return
	}
	if _, err := os.Stat(t.Path + rotatingSuffix); err == nil {
		t.rotating = true
		t.rotateStable = false
		t.offset = 0
		t.partial = nil
	}
}

// startRotation begins a rotation cycle once the fully-ingested live log passes the
// threshold (#98): rename the log out of the way (Envoy keeps writing to the same inode
// through its open fd) and ask Envoy to reopen, so new lines land in a fresh file at
// Path. Everything in the renamed file — including lines written between the rename and
// the reopen — is drained by the normal chunk loop before finishRotation deletes it, so
// rotation never discards un-ingested evidence: an agent flooding the log gains nothing
// (growth beyond what the reporter ingests still ends in fail-closed eviction).
func (t *Tailer) startRotation(ctx context.Context) bool {
	if t.Reopen == nil {
		return false
	}
	threshold := t.RotateAfterBytes
	if threshold <= 0 {
		threshold = DefaultRotateAfterBytes
	}
	info, err := os.Stat(t.Path)
	if err != nil || info.Size() < threshold {
		return false
	}
	if err := os.Rename(t.Path, t.Path+rotatingSuffix); err != nil {
		log.Printf("egress-reporter: access-log rotation: rename: %v", err)
		return false
	}
	t.rotating = true
	t.rotateStable = false
	if err := t.Reopen(ctx); err != nil {
		log.Printf("egress-reporter: access-log rotation: envoy reopen: %v", err)
		// Restore the world and retry next cycle; if the rename-back fails too, stay
		// in the rotating state — finishRotation keeps retrying the reopen.
		if rerr := os.Rename(t.Path+rotatingSuffix, t.Path); rerr == nil {
			t.rotating = false
			return false
		}
	}
	return true
}

// finishRotation completes a cycle once the renamed remainder is fully drained. It
// requires the live file to exist (proof Envoy reopened — otherwise retry the reopen),
// then one further drained poll as grace for Envoy's log flusher to land anything still
// buffered for the old fd (flush interval ≪ poll interval), and only then deletes the
// remainder and switches to the live file at offset zero. Reports whether the switch
// happened.
func (t *Tailer) finishRotation(ctx context.Context) bool {
	if _, err := os.Stat(t.Path); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("egress-reporter: access-log rotation: stat live log: %v", err)
			return false
		}
		// No live file: Envoy never reopened (crash between rename and reopen, or the
		// reopen failed). Without it the proxy has nowhere to log — keep retrying.
		if t.Reopen != nil {
			if err := t.Reopen(ctx); err != nil {
				log.Printf("egress-reporter: access-log rotation: envoy reopen: %v", err)
			}
		}
		return false
	}
	if !t.rotateStable {
		t.rotateStable = true
		return false
	}
	if err := os.Remove(t.Path + rotatingSuffix); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("egress-reporter: access-log rotation: remove rotated log: %v", err)
		return false
	}
	t.rotating = false
	t.rotateStable = false
	t.offset = 0
	t.partial = nil
	if t.OnRotated != nil {
		t.OnRotated()
	}
	return true
}

func (t *Tailer) queue(d scrutineerv1alpha1.PolicyDecision) {
	maxPending := t.MaxPending
	if maxPending <= 0 {
		maxPending = DefaultMaxPending
	}
	t.pending = append(t.pending, d)
	if over := len(t.pending) - maxPending; over > 0 {
		t.pending = t.pending[over:]
		t.dropped += int64(over)
		log.Printf("egress-reporter: pending queue overflow, dropped %d oldest decisions (total dropped %d)", over, t.dropped)
	}
}

// flush submits pending decisions in batches capped by count and encoded bytes,
// classifying failures per the reporter contract §4.4 (#96): permanent rejections
// (400/403/404/413) must not wedge the queue head — a too-large batch is split, a
// too-large single decision is poison and dropped (counted via OnRejected), other
// permanent rejections drop the batch. Everything else (network errors, 5xx, 429,
// 401, 409) is transient: pending is retained and the error returned for retry.
func (t *Tailer) flush(ctx context.Context) error {
	batchMax := t.BatchMax
	if batchMax <= 0 {
		batchMax = DefaultBatchMax
	}
	maxBytes := t.BatchMaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultBatchMaxBytes
	}

	splitTo := 0 // when >0, a 413 told us to try at most this many decisions
	for len(t.pending) > 0 {
		n := t.batchLen(batchMax, maxBytes)
		if splitTo > 0 && n > splitTo {
			n = splitTo
		}
		err := t.Submit(ctx, t.pending[:n])
		if err == nil {
			t.pending = t.pending[n:]
			splitTo = 0
			continue
		}

		status := 0
		var se *reporterclient.StatusError
		if errors.As(err, &se) {
			status = se.StatusCode
		}
		switch status {
		case http.StatusRequestEntityTooLarge:
			if n > 1 {
				// Our byte estimate undershot the server's cap: split and retry.
				splitTo = n / 2
				continue
			}
			// A single decision the reporter can never accept is poison: drop it,
			// or it would block all newer evidence forever.
			t.reject(1, status, err)
			splitTo = 0
		case http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound:
			// Permanent for this batch (contract: fix payload / misconfiguration /
			// session deleted) — retrying verbatim can never succeed.
			t.reject(n, status, err)
			splitTo = 0
		default:
			// Transient (network error, 5xx, 429, 401, 409): keep pending, retry
			// next poll — at-least-once.
			return err
		}
	}
	return nil
}

// batchLen returns how many pending decisions fit the count and byte caps. Always at
// least 1 when pending is non-empty: an oversized head decision must still be attempted
// (and then handled by the 413 poison path) or the queue could never advance.
func (t *Tailer) batchLen(maxCount, maxBytes int) int {
	n := 0
	size := batchEnvelopeBytes
	for n < len(t.pending) && n < maxCount {
		enc, err := json.Marshal(t.pending[n])
		sz := len(enc) + 1
		if err != nil {
			sz = maxBytes // unsizeable: isolate it in its own batch
		}
		if n > 0 && size+sz > maxBytes {
			break
		}
		size += sz
		n++
	}
	return n
}

// reject drops the first n pending decisions after a permanent reporter rejection,
// logging and reporting the loss (OnRejected). This is deliberate evidence loss in the
// same class as pending-queue overflow: recorded, bounded, never silent.
func (t *Tailer) reject(n, status int, err error) {
	log.Printf("egress-reporter: dropping %d decision(s) after permanent reporter rejection (%v)", n, err)
	if t.OnRejected != nil {
		t.OnRejected(n, status)
	}
	t.pending = t.pending[n:]
}
