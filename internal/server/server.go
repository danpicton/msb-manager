// Package server wires the msb-manager HTTP control-plane API: routing,
// authentication, and the operational endpoints. It owns no msb interaction
// itself — that lives behind the MsbClient interface, satisfied in production
// by *msb.Client.
package server

import (
	"context"
	"net/http"

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
}

// New builds the control-plane HTTP handler. Every route except /healthz sits
// behind the bearer token.
func New(cfg Config, client MsbClient) http.Handler {
	protected := http.NewServeMux()
	protected.HandleFunc("GET /sandboxes", handleListSandboxes(client))
	protected.HandleFunc("GET /sandboxes/{name}", handleInspectSandbox(client))

	root := http.NewServeMux()
	root.HandleFunc("GET /healthz", handleHealthz)
	root.Handle("/", requireBearer(cfg.Token, protected))
	return root
}

// handleHealthz reports liveness. It is the one unauthenticated endpoint.
func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}
