package config

import "testing"

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
