// Package config loads msb-manager's runtime configuration from the
// environment. The single bearer token is required; everything else has a
// safe default (loopback bind, "msb" on PATH).
package config

import "errors"

// Config is the resolved runtime configuration.
type Config struct {
	// Token is the single bearer token guarding every endpoint except /healthz.
	Token string
}

// ErrTokenRequired is returned by Load when MSB_MANAGER_TOKEN is unset or empty.
// A token-less server would either fail closed on every request or, worse,
// accept anything — so we refuse to start.
var ErrTokenRequired = errors.New("config: MSB_MANAGER_TOKEN is required")

// Load resolves configuration from getenv (injected so tests don't touch real
// process env). Returns ErrTokenRequired if the bearer token is missing.
func Load(getenv func(string) string) (Config, error) {
	token := getenv("MSB_MANAGER_TOKEN")
	if token == "" {
		return Config{}, ErrTokenRequired
	}
	return Config{Token: token}, nil
}
