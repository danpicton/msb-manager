package config

import (
	"testing"
	"time"
)

// envFunc builds a getenv-style lookup from a map.
func envFunc(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

func TestLoadRequiresToken(t *testing.T) {
	_, err := Load(envFunc(map[string]string{}))

	if err == nil {
		t.Fatal("Load with no token: got nil error, want an error")
	}
}

func TestLoadAppliesDefaults(t *testing.T) {
	cfg, err := Load(envFunc(map[string]string{
		"MSB_MANAGER_TOKEN": "s3cret",
	}))
	if err != nil {
		t.Fatalf("Load with token: unexpected error: %v", err)
	}

	if cfg.Token != "s3cret" {
		t.Errorf("Token = %q, want %q", cfg.Token, "s3cret")
	}
	// Invariant (CLAUDE.md): binds loopback only. The default must not be
	// reachable off-host; Caddy fronts it.
	if cfg.ListenAddr != "127.0.0.1:8080" {
		t.Errorf("ListenAddr default = %q, want %q", cfg.ListenAddr, "127.0.0.1:8080")
	}
	if cfg.MsbPath != "msb" {
		t.Errorf("MsbPath default = %q, want %q", cfg.MsbPath, "msb")
	}
	if cfg.DataDir == "" {
		t.Error("DataDir default = empty, want a non-empty path")
	}
	if cfg.CmdTimeout != DefaultCmdTimeout {
		t.Errorf("CmdTimeout default = %v, want %v", cfg.CmdTimeout, DefaultCmdTimeout)
	}
}

func TestLoad_CmdTimeoutOverride(t *testing.T) {
	cfg, err := Load(envFunc(map[string]string{
		"MSB_MANAGER_TOKEN":       "s3cret",
		"MSB_MANAGER_CMD_TIMEOUT": "90s",
	}))
	if err != nil {
		t.Fatalf("Load: unexpected error: %v", err)
	}
	if cfg.CmdTimeout != 90*time.Second {
		t.Errorf("CmdTimeout = %v, want 90s", cfg.CmdTimeout)
	}
}

func TestLoad_CmdTimeoutInvalidErrors(t *testing.T) {
	cases := []string{"nonsense", "0", "-5s", "150s", "10m"}
	for _, v := range cases {
		_, err := Load(envFunc(map[string]string{
			"MSB_MANAGER_TOKEN":       "s3cret",
			"MSB_MANAGER_CMD_TIMEOUT": v,
		}))
		if err == nil {
			t.Errorf("Load(CMD_TIMEOUT=%q): got nil, want error", v)
		}
	}
}

// The cap exists so CmdTimeout can never exceed the HTTP WriteTimeout — a
// timed-out call must still be able to write its 504 (review #1). The boundary
// value (== MaxCmdTimeout) is allowed.
func TestLoad_CmdTimeoutAtCapAllowed(t *testing.T) {
	cfg, err := Load(envFunc(map[string]string{
		"MSB_MANAGER_TOKEN":       "s3cret",
		"MSB_MANAGER_CMD_TIMEOUT": MaxCmdTimeout.String(),
	}))
	if err != nil {
		t.Fatalf("Load(CMD_TIMEOUT=%s): unexpected error: %v", MaxCmdTimeout, err)
	}
	if cfg.CmdTimeout != MaxCmdTimeout {
		t.Errorf("CmdTimeout = %v, want %v", cfg.CmdTimeout, MaxCmdTimeout)
	}
}

func TestLoadHonoursOverrides(t *testing.T) {
	cfg, err := Load(envFunc(map[string]string{
		"MSB_MANAGER_TOKEN":       "s3cret",
		"MSB_MANAGER_LISTEN_ADDR": "127.0.0.1:9999",
		"MSB_MANAGER_MSB_PATH":    "/opt/microsandbox/bin/msb",
		"MSB_MANAGER_DATA_DIR":    "/srv/msb-manager",
	}))
	if err != nil {
		t.Fatalf("Load with overrides: unexpected error: %v", err)
	}

	if cfg.ListenAddr != "127.0.0.1:9999" {
		t.Errorf("ListenAddr = %q, want override %q", cfg.ListenAddr, "127.0.0.1:9999")
	}
	if cfg.MsbPath != "/opt/microsandbox/bin/msb" {
		t.Errorf("MsbPath = %q, want override %q", cfg.MsbPath, "/opt/microsandbox/bin/msb")
	}
	if cfg.DataDir != "/srv/msb-manager" {
		t.Errorf("DataDir = %q, want override %q", cfg.DataDir, "/srv/msb-manager")
	}
}
