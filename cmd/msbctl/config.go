package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// defaultURL is the built-in target when nothing else is configured: the
// loopback address msb-manager binds (Caddy fronts it for remote access).
const defaultURL = "http://127.0.0.1:8080"

// cliFlags carries the global target/auth flags. Kept as a plain struct so the
// resolver is a pure function the precedence tests can drive directly.
type cliFlags struct {
	server  string
	token   string
	profile string
}

// target is the resolved connection: where to talk and with what credential.
type target struct {
	URL   string
	Token string
}

// profile is one named target in the config file.
type profile struct {
	URL   string `yaml:"url"`
	Token string `yaml:"token"`
}

// configFile is the on-disk msbctl config: named profiles plus a selectable
// default. Its only job is to be one (lowest-priority) source for resolveTarget.
type configFile struct {
	DefaultProfile string             `yaml:"default_profile"`
	Profiles       map[string]profile `yaml:"profiles"`
}

// resolveTarget computes the effective {url, token} from the precedence chain
// (highest wins): flags > env > config-file profile > built-in defaults
// (ADR-0007). It is pure — env access is injected — so the precedence is
// unit-testable without touching the real environment or filesystem.
//
// The selected profile is itself chosen by precedence: --profile flag, then
// $MSB_MANAGER_PROFILE, then the config's default_profile. A named profile that
// does not exist is an error rather than a silent fall-through, so a typo fails
// loudly instead of quietly hitting the loopback default.
func resolveTarget(f cliFlags, getenv func(string) string, cfg *configFile) (target, error) {
	profName := firstNonEmpty(f.profile, getenv("MSB_MANAGER_PROFILE"))
	if profName == "" && cfg != nil {
		profName = cfg.DefaultProfile
	}

	var prof profile
	if profName != "" {
		if cfg == nil {
			return target{}, fmt.Errorf("profile %q requested but no config file was found", profName)
		}
		p, ok := cfg.Profiles[profName]
		if !ok {
			return target{}, fmt.Errorf("profile %q not found in config", profName)
		}
		prof = p
	}

	return target{
		URL:   firstNonEmpty(f.server, getenv("MSB_MANAGER_URL"), prof.URL, defaultURL),
		Token: firstNonEmpty(f.token, getenv("MSB_MANAGER_TOKEN"), prof.Token),
	}, nil
}

// loadConfig reads and parses the config file at path. A missing file is not an
// error — the caller falls back to env/flags/defaults. The token lives here, so
// a file looser than 0600 earns a warning (via warn); it is not fatal, matching
// the "warn, do not fail" rule (ADR-0007).
func loadConfig(path string, warn func(string)) (*configFile, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("stat config %s: %w", path, err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		warn(fmt.Sprintf("config file %s has permissions %#o; it holds a bearer token, tighten it to 0600", path, perm))
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	var cfg configFile
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true) // a typo'd key is a config bug, not a thing to ignore
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	return &cfg, nil
}

// configPath returns the config-file location: $XDG_CONFIG_HOME/msbctl/config.yaml,
// falling back to ~/.config/msbctl/config.yaml.
func configPath(getenv func(string) string) string {
	base := getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(getenv("HOME"), ".config")
	}
	return filepath.Join(base, "msbctl", "config.yaml")
}

// firstNonEmpty returns the first non-empty string, or "" if all are empty.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
