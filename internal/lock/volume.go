// Package lock implements msb-manager's one-running-sandbox-per-volume
// invariant. State is in-memory (no persistence) and re-seeded from msb's
// actual running sandboxes at startup; reconciliation guards against
// out-of-band msb usage.
//
// Why not a filesystem lockfile? msb already knows the truth (`msb inspect`
// echoes the volume name on each mount — CONTEXT verification #1). The
// authoritative state belongs to msb; this package only ensures msb-manager
// doesn't race itself.
package lock

import (
	"errors"
	"fmt"
	"sync"
)

// ErrVolumeBusy is returned by Acquire/AcquireMany when a volume is already
// claimed by a different sandbox. The wrapped error message names the holder
// so the HTTP layer can surface "in use by <other>".
var ErrVolumeBusy = errors.New("volume in use by another running sandbox")

// VolumeLock tracks which named microsandbox volumes are claimed by which
// currently-running sandboxes. Safe for concurrent use.
type VolumeLock struct {
	mu   sync.Mutex
	held map[string]string // volume name -> sandbox name
}

// New returns an empty VolumeLock. Use Reconcile to seed it from msb state.
func New() *VolumeLock {
	return &VolumeLock{held: make(map[string]string)}
}

// Acquire claims one volume for sandbox. Idempotent when the same sandbox
// re-acquires its own claim.
func (l *VolumeLock) Acquire(volume, sandbox string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if h, ok := l.held[volume]; ok && h != sandbox {
		return fmt.Errorf("%w: %q held by %q", ErrVolumeBusy, volume, h)
	}
	l.held[volume] = sandbox
	return nil
}

// AcquireMany claims all volumes for sandbox atomically: either every volume
// becomes the sandbox's claim, or none do (no partial state on conflict).
func (l *VolumeLock) AcquireMany(volumes []string, sandbox string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, v := range volumes {
		if h, ok := l.held[v]; ok && h != sandbox {
			return fmt.Errorf("%w: %q held by %q", ErrVolumeBusy, v, h)
		}
	}
	for _, v := range volumes {
		l.held[v] = sandbox
	}
	return nil
}

// Release frees every volume currently held by sandbox.
func (l *VolumeLock) Release(sandbox string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for v, h := range l.held {
		if h == sandbox {
			delete(l.held, v)
		}
	}
}

// Holder returns the sandbox currently holding volume, if any. Useful for
// pre-checks ("can I delete this volume?") that don't want Acquire's
// claim-as-side-effect semantics.
func (l *VolumeLock) Holder(volume string) (sandbox string, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	sandbox, ok = l.held[volume]
	return
}

// Reconcile replaces the entire in-memory state with the given snapshot.
// Called at startup to seed from `msb` truth; can also be called periodically
// or before risky operations to defend against out-of-band msb usage.
func (l *VolumeLock) Reconcile(state map[string]string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.held = make(map[string]string, len(state))
	for v, s := range state {
		l.held[v] = s
	}
}

// heldForTest exposes the internal map to package tests only.
func (l *VolumeLock) heldForTest() map[string]string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make(map[string]string, len(l.held))
	for k, v := range l.held {
		out[k] = v
	}
	return out
}
