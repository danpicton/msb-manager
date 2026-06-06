// Package config loads msb-manager's runtime configuration from the
// environment. The single bearer token is required; everything else has a
// safe default (loopback bind, "msb" on PATH, /var/lib/msb-manager state).
package config

import "errors"

// Config is the resolved runtime configuration.
type Config struct {
	// Token is the single bearer token guarding every endpoint except /healthz.
	Token string

	// ListenAddr is the host:port the HTTP server binds. Defaults to loopback;
	// Caddy fronts the only external listener (CLAUDE.md invariant).
	ListenAddr string

	// MsbPath is the msb CLI binary to shell out to. Defaults to looking on PATH.
	MsbPath string

	// DataDir is the filesystem state root — used for the one-VM-per-volume lock
	// and any other minimal server-owned state. We never store a project
	// registry; msb ls remains the source of truth.
	DataDir string
}

// ErrTokenRequired is returned by Load when MSB_MANAGER_TOKEN is unset or empty.
// A token-less server would either fail closed on every request or, worse,
// accept anything — so we refuse to start.
var ErrTokenRequired = errors.New("config: MSB_MANAGER_TOKEN is required")

// Default values, exported for callers that want to surface them in --help etc.
const (
	DefaultListenAddr = "127.0.0.1:8080"
	DefaultMsbPath    = "msb"
	DefaultDataDir    = "/var/lib/msb-manager"
)

// Load resolves configuration from getenv (injected so tests don't touch real
// process env). Returns ErrTokenRequired if the bearer token is missing.
func Load(getenv func(string) string) (Config, error) {
	token := getenv("MSB_MANAGER_TOKEN")
	if token == "" {
		return Config{}, ErrTokenRequired
	}
	return Config{
		Token:      token,
		ListenAddr: orDefault(getenv("MSB_MANAGER_LISTEN_ADDR"), DefaultListenAddr),
		MsbPath:    orDefault(getenv("MSB_MANAGER_MSB_PATH"), DefaultMsbPath),
		DataDir:    orDefault(getenv("MSB_MANAGER_DATA_DIR"), DefaultDataDir),
	}, nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
