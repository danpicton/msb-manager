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
