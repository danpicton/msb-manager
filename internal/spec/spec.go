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

	"gopkg.in/yaml.v3"
)

// Spec describes a sandbox to create. Field set grows as later steps land
// (secrets/ssh/script/network at step 6; snapshot at 7).
type Spec struct {
	Name   string            `yaml:"name" json:"name"`
	Image  string            `yaml:"image" json:"image"`
	CPUs   int               `yaml:"cpus,omitempty" json:"cpus,omitempty"`
	Memory int               `yaml:"memory,omitempty" json:"memory,omitempty"` // MiB
	Volume *Volume           `yaml:"volume,omitempty" json:"volume,omitempty"`
	Env    map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
	Ports  []PortMapping     `yaml:"ports,omitempty" json:"ports,omitempty"`
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

// Validate checks the required-field invariants and field-level format checks.
func (s Spec) Validate() error {
	if s.Name == "" {
		return errors.New("spec: name is required")
	}
	if s.Image == "" {
		return errors.New("spec: image is required")
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
	return nil
}
