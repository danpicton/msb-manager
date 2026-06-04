// Package server wires the msb-manager HTTP control-plane API: routing,
// authentication, and the operational endpoints. It owns no msb interaction
// itself — that lives behind the MsbClient interface, satisfied in production
// by *msb.Client.
package server

import (
	"context"
	"net/http"

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
	VolumeList(ctx context.Context) ([]msb.Volume, error)
	VolumeCreate(ctx context.Context, name, size string) error
	VolumeRm(ctx context.Context, name string) error
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
	protected.HandleFunc("GET /volumes", handleListVolumes(client))
	protected.HandleFunc("POST /volumes", handleCreateVolume(client))
	protected.HandleFunc("DELETE /volumes/{name}", handleDeleteVolume(client))

	root := http.NewServeMux()
	root.HandleFunc("GET /healthz", handleHealthz)
	root.Handle("/", requireBearer(cfg.Token, protected))
	return root
}

// handleHealthz reports liveness. It is the one unauthenticated endpoint.
func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}
