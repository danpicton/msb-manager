package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"msb-manager/internal/lock"
	"msb-manager/internal/msb"
)

// fakeInspector drives reconcileVolumesWithRetry without a real msb: List
// fails a configurable number of times before succeeding, mirroring the
// transient hang/timeout CONTEXT verification #3 documents.
type fakeInspector struct {
	failuresLeft int
	listOut      []msb.Sandbox
	inspectOut   map[string]msb.SandboxDetail
	listCalls    int
}

func (f *fakeInspector) List(_ context.Context) ([]msb.Sandbox, error) {
	f.listCalls++
	if f.failuresLeft > 0 {
		f.failuresLeft--
		return nil, errors.New("msb ls timed out")
	}
	return f.listOut, nil
}

func (f *fakeInspector) Inspect(_ context.Context, name string) (msb.SandboxDetail, error) {
	d, ok := f.inspectOut[name]
	if !ok {
		return msb.SandboxDetail{}, msb.ErrSandboxNotFound
	}
	return d, nil
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// Issue #20: a transient reconcile failure at boot must not leave the server
// running with an empty lock. The fail-closed design retries until a
// reconcile succeeds before the listener starts, so mutations are gated by
// construction: serving cannot begin until the lock is seeded from msb truth.
func TestReconcileVolumesWithRetry_RetriesUntilSuccessAndSeedsLock(t *testing.T) {
	client := &fakeInspector{
		failuresLeft: 1, // transient: fails once, then msb has recovered
		listOut:      []msb.Sandbox{{Name: "web", Status: "Running"}},
		inspectOut: map[string]msb.SandboxDetail{
			"web": {
				Name:   "web",
				Mounts: []msb.Mount{{Type: "Named", Name: "data", Guest: "/workspace"}},
			},
		},
	}
	vlock := lock.New()

	err := reconcileVolumesWithRetry(context.Background(), client, vlock, discardLogger(), time.Millisecond, time.Millisecond)
	if err != nil {
		t.Fatalf("reconcileVolumesWithRetry: %v", err)
	}
	if client.listCalls != 2 {
		t.Errorf("List called %d times, want 2 (one failure, one success)", client.listCalls)
	}
	// The lock is seeded from msb truth: data is web's, so a conflicting
	// claim is refused.
	if err := vlock.Acquire("data", "other"); !errors.Is(err, lock.ErrVolumeBusy) {
		t.Errorf("data should be web's after reconcile; got %v", err)
	}
}

// A shutdown signal during the retry loop must abort startup rather than spin
// forever — the only exits are reconcile success or cancellation.
func TestReconcileVolumesWithRetry_AbortsOnContextCancel(t *testing.T) {
	client := &fakeInspector{failuresLeft: int(^uint(0) >> 1)} // never succeeds
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := reconcileVolumesWithRetry(ctx, client, lock.New(), discardLogger(), time.Millisecond, time.Millisecond)
	if err == nil {
		t.Fatal("reconcileVolumesWithRetry: got nil, want error after cancellation")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error %v should wrap context.Canceled", err)
	}
}
