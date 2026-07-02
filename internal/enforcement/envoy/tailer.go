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
	"errors"
	"io"
	"log"
	"os"
	"time"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
)

// Tailer defaults; BatchMax mirrors the reporter's MaxDecisionsPerReport cap.
const (
	DefaultBatchMax     = 128
	DefaultPollInterval = 2 * time.Second
	DefaultMaxPending   = 4096
	finalDrainTimeout   = 5 * time.Second
)

// Tailer incrementally reads Envoy's JSON access log and submits parsed egress decisions
// to the reporter. It is the engine of the egress-reporter container (Slice C, #62):
// evidence flows access.json → ParseAccessLogLine → Submit batches.
//
// Delivery is at-least-once: the in-memory offset is lost on container restart, so the
// whole file is re-read and re-submitted — the controller's status merge dedups decisions
// by key (time is pinned from %START_TIME%, so re-delivered records are identical). A
// failed Submit keeps the batch pending and retries next poll; the pending queue is
// bounded by MaxPending, dropping the OLDEST entries beyond it (logged and counted) so a
// long reporter outage cannot grow memory unboundedly.
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

	offset  int64
	partial []byte
	pending []scrutineerv1alpha1.PolicyDecision
	dropped int64
}

// Dropped reports how many parsed decisions were discarded because the pending queue
// overflowed MaxPending (i.e. evidence lost to a prolonged reporter outage).
func (t *Tailer) Dropped() int64 { return t.dropped }

// Poll performs one read-parse-submit cycle. A missing file is not an error (Envoy has
// not received traffic yet). A Submit failure is returned after retaining the batch for
// the next cycle.
func (t *Tailer) Poll(ctx context.Context) error {
	if err := t.readNew(); err != nil {
		return err
	}
	return t.flush(ctx)
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

// readNew reads bytes appended since the last poll, parses complete lines, and queues
// their decisions. Partial trailing lines are buffered until completed. A file shorter
// than the current offset means truncation/recreation: restart from the beginning.
func (t *Tailer) readNew() error {
	f, err := os.Open(t.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() < t.offset {
		t.offset = 0
		t.partial = nil
	}
	if info.Size() == t.offset {
		return nil
	}

	if _, err := f.Seek(t.offset, io.SeekStart); err != nil {
		return err
	}
	buf, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	t.offset += int64(len(buf))

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
			continue
		}
		t.queue(entry.Decision())
	}
	t.partial = data
	return nil
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

func (t *Tailer) flush(ctx context.Context) error {
	batchMax := t.BatchMax
	if batchMax <= 0 {
		batchMax = DefaultBatchMax
	}
	for len(t.pending) > 0 {
		n := len(t.pending)
		if n > batchMax {
			n = batchMax
		}
		if err := t.Submit(ctx, t.pending[:n]); err != nil {
			return err
		}
		t.pending = t.pending[n:]
	}
	return nil
}
