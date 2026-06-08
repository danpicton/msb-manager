// Package server wires the msb-manager HTTP control-plane API: routing,
// authentication, and the operational endpoints. It owns no msb interaction
// itself — that lives behind the MsbClient interface, satisfied in production
// by *msb.Client.
package server

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"msb-manager/internal/lock"
	"msb-manager/internal/msb"
)

// Config holds the server's runtime configuration.
type Config struct {
	// Token is the single bearer token guarding every endpoint except /healthz.
	Token string
}

// MsbClient is the subset of the msb adapter the HTTP handlers consume.
// Defined here so tests can inject a fake without spawning a real msb.
type MsbClient interface {
	List(ctx context.Context) ([]msb.Sandbox, error)
	Inspect(ctx context.Context, name string) (msb.SandboxDetail, error)
	Create(ctx context.Context, opts msb.CreateOpts) error
	Start(ctx context.Context, name string) error
	Stop(ctx context.Context, name string) error
	Rm(ctx context.Context, name string) error
	Logs(ctx context.Context, name string, opts msb.LogsOpts) ([]byte, error)
	Metrics(ctx context.Context, name string) (msb.Metrics, error)
	VolumeList(ctx context.Context) ([]msb.Volume, error)
	VolumeCreate(ctx context.Context, name, size string) error
	VolumeRm(ctx context.Context, name string) error
	SnapshotList(ctx context.Context) ([]msb.Snapshot, error)
	SnapshotCreate(ctx context.Context, from, dest string, labels map[string]string, force bool) error
	SnapshotRm(ctx context.Context, name string) error
}

// New builds the control-plane HTTP handler with a fresh empty VolumeLock.
// Callers wanting a pre-seeded lock (e.g. main.go after startup reconcile)
// should use NewWithLock.
func New(cfg Config, client MsbClient) http.Handler {
	return NewWithLock(cfg, client, lock.New())
}

// NewWithLock is the full constructor. The VolumeLock enforces the
// one-running-sandbox-per-volume invariant; it should be reconciled from msb
// state at startup before being passed in.
func NewWithLock(cfg Config, client MsbClient, vlock *lock.VolumeLock) http.Handler {
	protected := http.NewServeMux()
	protected.HandleFunc("GET /sandboxes", handleListSandboxes(client))
	protected.HandleFunc("POST /sandboxes", handleCreateSandbox(client, vlock))
	protected.HandleFunc("GET /sandboxes/{name}", handleInspectSandbox(client))
	protected.HandleFunc("DELETE /sandboxes/{name}", handleDeleteSandbox(client, vlock))
	protected.HandleFunc("POST /sandboxes/{name}/start", handleStartSandbox(client, vlock))
	protected.HandleFunc("POST /sandboxes/{name}/stop", handleStopSandbox(client, vlock))
	protected.HandleFunc("GET /sandboxes/{name}/logs", handleLogs(client))
	protected.HandleFunc("GET /sandboxes/{name}/metrics", handleMetrics(client))
	protected.HandleFunc("GET /volumes", handleListVolumes(client))
	protected.HandleFunc("POST /volumes", handleCreateVolume(client))
	protected.HandleFunc("DELETE /volumes/{name}", handleDeleteVolume(client, vlock))
	protected.HandleFunc("GET /snapshots", handleListSnapshots(client))
	protected.HandleFunc("POST /snapshots", handleCreateSnapshot(client))
	protected.HandleFunc("DELETE /snapshots/{name}", handleDeleteSnapshot(client))

	root := http.NewServeMux()
	root.HandleFunc("GET /healthz", handleHealthz)
	root.HandleFunc("GET /readyz", handleReadyz(client, &readinessCache{ttl: readinessTTL}))
	root.Handle("/", requireBearer(cfg.Token, protected))
	return root
}

// handleHealthz reports liveness — the http.Server is accepting requests.
// Cheap and shallow; deliberately does not consult msb.
func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// readinessTTL is how long a /readyz result is reused before a fresh `msb ls`
// runs. Short enough that a probe sees a genuinely-recent signal, long enough
// that a burst of probes collapses to one subprocess.
const readinessTTL = 2 * time.Second

// readinessCache memoises the readiness probe result for a short TTL.
//
// DECISION (issue #6): /readyz stays unauthenticated so Caddy/systemd can probe
// it the same way as /healthz, but it must not let any unauthenticated caller
// drive unbounded `msb ls` subprocesses (a DoS-amplification vector, worse
// because `msb ls` itself can hang — CONTEXT verification #3). Caching, rather
// than requiring auth, was chosen because it preserves the simple probe contract
// and needs no token plumbing into Caddy's health check. The TTL bounds the
// subprocess rate to at most one per interval regardless of request volume.
type readinessCache struct {
	ttl       time.Duration
	mu        sync.Mutex
	checkedAt time.Time // zero value = never checked (cache cold)
	err       error
}

// ready returns the cached readiness result if it's within the TTL, otherwise
// runs a fresh `msb ls`. The lock is held across the List call so a concurrent
// burst produces a single subprocess (the rest wait, then read the cache). The
// zero value of checkedAt reads as "infinitely old", so a cold cache always
// misses without a separate flag.
func (rc *readinessCache) ready(ctx context.Context, client MsbClient) error {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if time.Since(rc.checkedAt) < rc.ttl {
		return rc.err
	}
	_, err := client.List(ctx)
	// A canceled context is the caller disconnecting, not msb failing — don't
	// poison the cache with it (review #2), or every probe in the next TTL
	// window would see a spurious 503 while msb is actually healthy.
	if errors.Is(err, context.Canceled) {
		return err
	}
	rc.checkedAt = time.Now()
	rc.err = err
	return err
}

// handleReadyz reports readiness — msb itself is reachable and serving. A
// successful `msb ls` is the cheapest end-to-end signal that the supervisor
// is up and the API can do real work. Returns 503 when msb errors so probes
// (Caddy active health checks, systemd) treat this instance as not-ready.
// Results are cached for a short TTL (see readinessCache).
func handleReadyz(client MsbClient, cache *readinessCache) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := cache.ready(r.Context(), client); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}
