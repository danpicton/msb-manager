// Package server wires the msb-manager HTTP control-plane API: routing,
// authentication, and the operational endpoints. It owns no msb interaction
// itself — that lives behind the msb adapter.
package server

import "net/http"

// Config holds the server's runtime configuration.
type Config struct {
	// Token is the single bearer token guarding every endpoint except /healthz.
	Token string
}

// New builds the control-plane HTTP handler. Every route except /healthz sits
// behind the bearer token.
func New(cfg Config) http.Handler {
	// Protected routes are registered here as the slice grows.
	protected := http.NewServeMux()

	root := http.NewServeMux()
	root.HandleFunc("GET /healthz", handleHealthz)
	root.Handle("/", requireBearer(cfg.Token, protected))
	return root
}

// handleHealthz reports liveness. It is the one unauthenticated endpoint.
func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}
