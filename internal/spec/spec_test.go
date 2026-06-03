package spec

import (
	"testing"
)

// Minimum viable: just name + image. Drives the Spec shape, YAML decoder, and
// the required-field validator.
func TestParse_MinimalYAML(t *testing.T) {
	body := []byte(`
name: voltest
image: alpine
`)
	s, err := Parse(body)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if s.Name != "voltest" {
		t.Errorf("Name = %q, want %q", s.Name, "voltest")
	}
	if s.Image != "alpine" {
		t.Errorf("Image = %q, want %q", s.Image, "alpine")
	}
	if err := s.Validate(); err != nil {
		t.Errorf("Validate(minimal): unexpected error: %v", err)
	}
}

// yaml.v3 handles JSON natively (JSON is a YAML subset), so the same parser
// covers both content types — no separate JSON branch needed.
func TestParse_AlsoAcceptsJSON(t *testing.T) {
	body := []byte(`{"name":"voltest","image":"alpine"}`)
	s, err := Parse(body)
	if err != nil {
		t.Fatalf("Parse(JSON): %v", err)
	}
	if s.Name != "voltest" || s.Image != "alpine" {
		t.Errorf("Spec = %+v, want {voltest, alpine}", s)
	}
}

func TestValidate_RequiresName(t *testing.T) {
	s := Spec{Image: "alpine"}
	if err := s.Validate(); err == nil {
		t.Fatal("Validate(no name): got nil, want error")
	}
}

func TestValidate_RequiresImage(t *testing.T) {
	s := Spec{Name: "voltest"}
	if err := s.Validate(); err == nil {
		t.Fatal("Validate(no image): got nil, want error")
	}
}

func TestParse_RejectsUnknownFields(t *testing.T) {
	body := []byte(`
name: voltest
image: alpine
fnord: yes
`)
	if _, err := Parse(body); err == nil {
		t.Fatal("Parse(unknown field): got nil, want strict error")
	}
}
