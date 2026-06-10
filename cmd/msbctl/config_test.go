package main

import (
	"os"
	"path/filepath"
	"testing"
)

// noEnv is an empty environment lookup for tests exercising flag/config paths.
func noEnv(string) string { return "" }

// envMap adapts a map to the getenv func signature injected into resolveTarget.
func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestResolve_DefaultURLWhenNothingSet(t *testing.T) {
	got, err := resolveTarget(cliFlags{}, noEnv, nil)
	if err != nil {
		t.Fatalf("resolveTarget: %v", err)
	}
	if got.URL != defaultURL {
		t.Errorf("URL = %q, want default %q", got.URL, defaultURL)
	}
	if got.Token != "" {
		t.Errorf("Token = %q, want empty (no token by default)", got.Token)
	}
}

func TestResolve_FlagBeatsEnv(t *testing.T) {
	env := envMap(map[string]string{
		"MSB_MANAGER_URL":   "http://from-env:9000",
		"MSB_MANAGER_TOKEN": "env-token",
	})
	got, err := resolveTarget(cliFlags{server: "http://from-flag:8080", token: "flag-token"}, env, nil)
	if err != nil {
		t.Fatalf("resolveTarget: %v", err)
	}
	if got.URL != "http://from-flag:8080" {
		t.Errorf("URL = %q, want the flag value", got.URL)
	}
	if got.Token != "flag-token" {
		t.Errorf("Token = %q, want the flag value", got.Token)
	}
}

func TestResolve_EnvBeatsConfig(t *testing.T) {
	cfg := &configFile{
		DefaultProfile: "home",
		Profiles: map[string]profile{
			"home": {URL: "http://from-config:8080", Token: "config-token"},
		},
	}
	env := envMap(map[string]string{
		"MSB_MANAGER_URL":   "http://from-env:9000",
		"MSB_MANAGER_TOKEN": "env-token",
	})
	got, err := resolveTarget(cliFlags{}, env, cfg)
	if err != nil {
		t.Fatalf("resolveTarget: %v", err)
	}
	if got.URL != "http://from-env:9000" || got.Token != "env-token" {
		t.Errorf("got %+v, want env values to beat the config profile", got)
	}
}

func TestResolve_ConfigProfileUsedWhenNoFlagOrEnv(t *testing.T) {
	cfg := &configFile{
		DefaultProfile: "home",
		Profiles: map[string]profile{
			"home": {URL: "http://from-config:8080", Token: "config-token"},
		},
	}
	got, err := resolveTarget(cliFlags{}, noEnv, cfg)
	if err != nil {
		t.Fatalf("resolveTarget: %v", err)
	}
	if got.URL != "http://from-config:8080" || got.Token != "config-token" {
		t.Errorf("got %+v, want the default-profile values from config", got)
	}
}

func TestResolve_ProfileSelectionFlagBeatsDefault(t *testing.T) {
	cfg := &configFile{
		DefaultProfile: "home",
		Profiles: map[string]profile{
			"home": {URL: "http://home:8080", Token: "home-token"},
			"work": {URL: "http://work:8080", Token: "work-token"},
		},
	}
	got, err := resolveTarget(cliFlags{profile: "work"}, noEnv, cfg)
	if err != nil {
		t.Fatalf("resolveTarget: %v", err)
	}
	if got.URL != "http://work:8080" || got.Token != "work-token" {
		t.Errorf("got %+v, want the work profile (flag selects it over default)", got)
	}
}

func TestResolve_ProfileSelectionEnvBeatsDefault(t *testing.T) {
	cfg := &configFile{
		DefaultProfile: "home",
		Profiles: map[string]profile{
			"home": {URL: "http://home:8080"},
			"work": {URL: "http://work:8080"},
		},
	}
	got, err := resolveTarget(cliFlags{}, envMap(map[string]string{"MSB_MANAGER_PROFILE": "work"}), cfg)
	if err != nil {
		t.Fatalf("resolveTarget: %v", err)
	}
	if got.URL != "http://work:8080" {
		t.Errorf("URL = %q, want the env-selected work profile", got.URL)
	}
}

func TestResolve_MissingNamedProfileErrors(t *testing.T) {
	cfg := &configFile{Profiles: map[string]profile{"home": {URL: "x"}}}
	_, err := resolveTarget(cliFlags{profile: "nope"}, noEnv, cfg)
	if err == nil {
		t.Fatal("resolveTarget with an unknown profile should error, not silently fall through")
	}
}

func TestResolve_PartialProfileFallsBackToDefaults(t *testing.T) {
	// A profile that sets only a token must still get the default URL.
	cfg := &configFile{
		DefaultProfile: "home",
		Profiles:       map[string]profile{"home": {Token: "config-token"}},
	}
	got, err := resolveTarget(cliFlags{}, noEnv, cfg)
	if err != nil {
		t.Fatalf("resolveTarget: %v", err)
	}
	if got.URL != defaultURL {
		t.Errorf("URL = %q, want default when the profile omits url", got.URL)
	}
	if got.Token != "config-token" {
		t.Errorf("Token = %q, want the profile token", got.Token)
	}
}

// --- config file loading ---

func TestLoadConfig_MissingFileIsNotAnError(t *testing.T) {
	cfg, err := loadConfig(filepath.Join(t.TempDir(), "absent.yaml"), func(string) {})
	if err != nil {
		t.Fatalf("missing config should not error, got %v", err)
	}
	if cfg != nil {
		t.Errorf("missing config should yield nil cfg, got %+v", cfg)
	}
}

func TestLoadConfig_ParsesProfiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	writeFile(t, path, `default_profile: home
profiles:
  home:
    url: http://home:8080
    token: secret-home
`, 0o600)

	cfg, err := loadConfig(path, func(string) {})
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg == nil || cfg.DefaultProfile != "home" || cfg.Profiles["home"].URL != "http://home:8080" {
		t.Fatalf("parsed config = %+v, want home profile", cfg)
	}
}

func TestLoadConfig_WarnsButSucceedsOnLooseMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	writeFile(t, path, "profiles: {}\n", 0o644)

	var warned string
	cfg, err := loadConfig(path, func(msg string) { warned = msg })
	if err != nil {
		t.Fatalf("loose mode must warn, not fail; got error %v", err)
	}
	if cfg == nil {
		t.Fatal("loose-mode config should still load")
	}
	if warned == "" {
		t.Error("expected a permissions warning for a 0644 config file")
	}
}

func TestLoadConfig_NoWarnOn0600(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	writeFile(t, path, "profiles: {}\n", 0o600)

	var warned string
	if _, err := loadConfig(path, func(msg string) { warned = msg }); err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if warned != "" {
		t.Errorf("0600 config should not warn, got %q", warned)
	}
}

// --- config path resolution ---

func TestConfigPath_PrefersXDG(t *testing.T) {
	env := envMap(map[string]string{"XDG_CONFIG_HOME": "/xdg", "HOME": "/home/u"})
	if got := configPath(env); got != "/xdg/msbctl/config.yaml" {
		t.Errorf("configPath = %q, want XDG-based path", got)
	}
}

func TestConfigPath_FallsBackToHome(t *testing.T) {
	env := envMap(map[string]string{"HOME": "/home/u"})
	if got := configPath(env); got != "/home/u/.config/msbctl/config.yaml" {
		t.Errorf("configPath = %q, want ~/.config fallback", got)
	}
}

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	// WriteFile is subject to umask; force the exact mode we are testing.
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
}
