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
}

// Dropped reports how many parsed decisions were discarded because the pending queue
// overflowed MaxPending (i.e. evidence lost to a prolonged reporter outage).
func (t *Tailer) Dropped() int64 { return t.dropped }

// Poll performs one catch-up cycle: it first retries any decisions left pending by an
// earlier failed Submit, then alternates bounded chunk reads with flushes until EOF or
// a Submit failure. Gating each read on the previous flush means the tailer never holds
// more than roughly one chunk's decisions in memory — the access log itself buffers any
// backlog. A missing file is not an error (Envoy has not received traffic yet). A Submit
// failure is returned after retaining the batch for the next cycle.
func (t *Tailer) Poll(ctx context.Context) error {
	if err := t.flush(ctx); err != nil {
		return err
	}
	for {
		more, err := t.readChunk()
		if err != nil {
			return err
		}
		if err := t.flush(ctx); err != nil {
			return err
		}
		if !more {
			return nil
		}
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

	f, err := os.Open(t.Path)
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
