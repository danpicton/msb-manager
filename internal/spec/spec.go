// Package spec is the user-facing create-sandbox spec: YAML/JSON deserialisation
// and validation. It is deliberately decoupled from the msb adapter; the
// handler maps a validated Spec to msb.CreateOpts when calling the adapter.
//
// YAML is the canonical format (compose-style). JSON is accepted transparently
// because yaml.v3 parses JSON as a YAML subset — no separate decoder needed.
package spec

import (
	"bytes"
	"errors"
	"fmt"
	"path"
	"strings"

	"gopkg.in/yaml.v3"

	"msb-manager/internal/msb"
)

// Spec describes a sandbox to create. Field set grows as later steps land
// (ssh-key install design pending; snapshot source at step 7).
type Spec struct {
	Name     string            `yaml:"name" json:"name"`
	Image    string            `yaml:"image,omitempty" json:"image,omitempty"`       // OR snapshot
	Snapshot string            `yaml:"snapshot,omitempty" json:"snapshot,omitempty"` // OR image
	CPUs     int               `yaml:"cpus,omitempty" json:"cpus,omitempty"`
	Memory   int               `yaml:"memory,omitempty" json:"memory,omitempty"` // MiB
	Volume   *Volume           `yaml:"volume,omitempty" json:"volume,omitempty"`
	Env      map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
	Ports    []PortMapping     `yaml:"ports,omitempty" json:"ports,omitempty"`
	Secrets  []Secret          `yaml:"secrets,omitempty" json:"secrets,omitempty"`
	SSHKeys  []string          `yaml:"ssh_keys,omitempty" json:"ssh_keys,omitempty"`
}

// Volume is a single named-volume mount. (Multi-volume creates aren't
// in v1 scope — one mount per sandbox.)
type Volume struct {
	Name  string `yaml:"name" json:"name"`
	Mount string `yaml:"mount" json:"mount"` // absolute guest path
}

// PortMapping is a host→guest port forward.
type PortMapping struct {
	Host  int `yaml:"host" json:"host"`
	Guest int `yaml:"guest" json:"guest"`
}

// Secret is an egress credential: Key/Value injected as an env var inside the
// sandbox, only released for outbound traffic destined for Host. Maps to msb's
// `--secret KEY=VALUE@HOST` flag.
type Secret struct {
	Key   string `yaml:"key" json:"key"`
	Value string `yaml:"value" json:"value"`
	Host  string `yaml:"host" json:"host"`
}

// Parse decodes a YAML (or JSON) spec body. Unknown fields are rejected so
// typos in a client's spec surface as errors rather than silent drops.
func Parse(data []byte) (Spec, error) {
	var s Spec
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil {
		return Spec{}, fmt.Errorf("parse spec: %w", err)
	}
	return s, nil
}

// ToCreateOpts maps a (validated) Spec onto the adapter's parameter object.
// The mapping is straight-through; the only translation is the volume shape.
// Callers should Validate() before mapping; ToCreateOpts trusts its input.
func (s Spec) ToCreateOpts() msb.CreateOpts {
	opts := msb.CreateOpts{
		Name:      s.Name,
		Image:     s.Image,
		Snapshot:  s.Snapshot,
		CPUs:      s.CPUs,
		MemoryMiB: s.Memory,
		Env:       s.Env,
	}
	if s.Volume != nil {
		opts.Volume = &msb.VolumeMount{Name: s.Volume.Name, Mount: s.Volume.Mount}
	}
	for _, p := range s.Ports {
		opts.Ports = append(opts.Ports, msb.PortMapping{Host: p.Host, Guest: p.Guest})
	}
	for _, sec := range s.Secrets {
		opts.Secrets = append(opts.Secrets, msb.Secret{Key: sec.Key, Value: sec.Value, Host: sec.Host})
	}
	opts.SSHKeys = append(opts.SSHKeys, s.SSHKeys...)
	return opts
}

// Validate checks the required-field invariants and field-level format checks.
func (s Spec) Validate() error {
	if s.Name == "" {
		return errors.New("spec: name is required")
	}
	// Reject identifiers msb would misparse as flags (issue #3). The check is
	// the adapter's single source of truth for safe identifier shape.
	if !msb.ValidName(s.Name) {
		return fmt.Errorf("spec: name %q is not a valid identifier (alphanumeric start, [A-Za-z0-9_.-], max 128)", s.Name)
	}
	if s.Image == "" && s.Snapshot == "" {
		return errors.New("spec: one of image or snapshot is required")
	}
	if s.Image != "" && s.Snapshot != "" {
		return errors.New("spec: image and snapshot are mutually exclusive")
	}
	if s.Image != "" && !msb.ValidImage(s.Image) {
		return fmt.Errorf("spec: image %q is not a valid reference (no leading '-', no whitespace)", s.Image)
	}
	if s.Snapshot != "" && !msb.ValidName(s.Snapshot) {
		return fmt.Errorf("spec: snapshot %q is not a valid identifier", s.Snapshot)
	}
	if s.CPUs < 0 {
		return fmt.Errorf("spec: cpus must be >= 0, got %d", s.CPUs)
	}
	if s.Memory < 0 {
		return fmt.Errorf("spec: memory must be >= 0, got %d", s.Memory)
	}
	if s.Volume != nil {
		if s.Volume.Name == "" {
			return errors.New("spec: volume.name is required when volume is set")
		}
		if !msb.ValidName(s.Volume.Name) {
			return fmt.Errorf("spec: volume.name %q is not a valid identifier", s.Volume.Name)
		}
		if s.Volume.Mount == "" {
			return errors.New("spec: volume.mount is required when volume is set")
		}
		if !path.IsAbs(s.Volume.Mount) {
			return fmt.Errorf("spec: volume.mount must be an absolute path, got %q", s.Volume.Mount)
		}
	}
	for i, p := range s.Ports {
		if p.Host < 1 || p.Host > 65535 {
			return fmt.Errorf("spec: ports[%d].host out of range (1-65535), got %d", i, p.Host)
		}
		if p.Guest < 1 || p.Guest > 65535 {
			return fmt.Errorf("spec: ports[%d].guest out of range (1-65535), got %d", i, p.Guest)
		}
	}
	for i, k := range s.SSHKeys {
		if k == "" {
			return fmt.Errorf("spec: ssh_keys[%d] is empty", i)
		}
		// Keys are single-line OpenSSH format; a newline or NUL would either
		// inject extra lines into authorized_keys or truncate the entry.
		if strings.ContainsAny(k, "\n\x00") {
			return fmt.Errorf("spec: ssh_keys[%d] contains newline or NUL byte", i)
		}
	}
	for i, sec := range s.Secrets {
		if sec.Key == "" {
			return fmt.Errorf("spec: secrets[%d].key is required", i)
		}
		if sec.Value == "" {
			return fmt.Errorf("spec: secrets[%d].value is required", i)
		}
		if sec.Host == "" {
			return fmt.Errorf("spec: secrets[%d].host is required", i)
		}
		// '=' / '@' inside key or '@' inside host would make the assembled
		// "KEY=VALUE@HOST" string unparseable by msb. Refuse them here so we
		// fail with a precise message rather than handing msb a bad arg.
		if strings.ContainsAny(sec.Key, "=@") {
			return fmt.Errorf("spec: secrets[%d].key must not contain '=' or '@'", i)
		}
		if strings.Contains(sec.Host, "@") {
			return fmt.Errorf("spec: secrets[%d].host must not contain '@'", i)
		}
		// FAIL-CLOSED (issue #22): which '@' msb v0.5.2 splits "VALUE@HOST" on
		// is unverified, and the @HOST part is the egress allow-list — a
		// first-'@' split would silently release the secret to a host derived
		// from the value. Refuse '@' in values until upstream's split-at-last-
		// '@' behaviour is verified (see CONTEXT.md "msb CLI surface").
		if strings.Contains(sec.Value, "@") {
			return fmt.Errorf("spec: secrets[%d].value must not contain '@' — it would make the assembled KEY=VALUE@HOST egress rule ambiguous", i)
		}
	}
	return nil
}
