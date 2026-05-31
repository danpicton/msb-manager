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
