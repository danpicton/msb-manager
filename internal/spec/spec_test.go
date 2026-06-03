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

func TestParse_FullYAML(t *testing.T) {
	body := []byte(`
name: voltest
image: alpine
cpus: 2
memory: 512
volume:
  name: myvol
  mount: /workspace
env:
  PATH: /usr/bin
  FOO: bar
ports:
  - host: 8080
    guest: 80
  - host: 9090
    guest: 90
`)
	s, err := Parse(body)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if s.CPUs != 2 {
		t.Errorf("CPUs = %d, want 2", s.CPUs)
	}
	if s.Memory != 512 {
		t.Errorf("Memory = %d, want 512", s.Memory)
	}
	if s.Volume == nil || s.Volume.Name != "myvol" || s.Volume.Mount != "/workspace" {
		t.Errorf("Volume = %+v, want {myvol, /workspace}", s.Volume)
	}
	if s.Env["PATH"] != "/usr/bin" || s.Env["FOO"] != "bar" {
		t.Errorf("Env = %+v, want PATH+FOO", s.Env)
	}
	if len(s.Ports) != 2 || s.Ports[0].Host != 8080 || s.Ports[0].Guest != 80 {
		t.Errorf("Ports = %+v, want [8080:80, 9090:90]", s.Ports)
	}

	if err := s.Validate(); err != nil {
		t.Errorf("Validate(full): unexpected error: %v", err)
	}
}

func TestValidate_RejectsNegativeCPUsMemory(t *testing.T) {
	cases := []Spec{
		{Name: "x", Image: "alpine", CPUs: -1},
		{Name: "x", Image: "alpine", Memory: -1},
	}
	for _, s := range cases {
		if err := s.Validate(); err == nil {
			t.Errorf("Validate(%+v): got nil, want error", s)
		}
	}
}

func TestValidate_VolumeRequiresBothFields(t *testing.T) {
	cases := []Spec{
		{Name: "x", Image: "alpine", Volume: &Volume{Name: "myvol"}},
		{Name: "x", Image: "alpine", Volume: &Volume{Mount: "/workspace"}},
		{Name: "x", Image: "alpine", Volume: &Volume{Name: "myvol", Mount: "relative"}},
	}
	for _, s := range cases {
		if err := s.Validate(); err == nil {
			t.Errorf("Validate(%+v): got nil, want error", s)
		}
	}
}

func TestValidate_PortsInRange(t *testing.T) {
	cases := []Spec{
		{Name: "x", Image: "alpine", Ports: []PortMapping{{Host: 0, Guest: 80}}},
		{Name: "x", Image: "alpine", Ports: []PortMapping{{Host: 8080, Guest: 0}}},
		{Name: "x", Image: "alpine", Ports: []PortMapping{{Host: 70000, Guest: 80}}},
	}
	for _, s := range cases {
		if err := s.Validate(); err == nil {
			t.Errorf("Validate(%+v): got nil, want error", s)
		}
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
