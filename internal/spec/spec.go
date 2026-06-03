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

	"gopkg.in/yaml.v3"
)

// Spec describes a sandbox to create. Field set grows as later steps land
// (volume, env, ports now; secrets/ssh/script/network at step 6; snapshot at 7).
type Spec struct {
	Name  string `yaml:"name" json:"name"`
	Image string `yaml:"image" json:"image"`
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

// Validate checks the required-field invariants. Field-level format checks
// (e.g. name regex, port ranges) land alongside the fields that need them.
func (s Spec) Validate() error {
	if s.Name == "" {
		return errors.New("spec: name is required")
	}
	if s.Image == "" {
		return errors.New("spec: image is required")
	}
	return nil
}
