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

func TestValidate_RequiresImageOrSnapshot(t *testing.T) {
	// Neither -> error
	s := Spec{Name: "voltest"}
	if err := s.Validate(); err == nil {
		t.Fatal("Validate(no image, no snapshot): got nil, want error")
	}
}

func TestValidate_ImageAndSnapshotMutuallyExclusive(t *testing.T) {
	s := Spec{Name: "voltest", Image: "alpine", Snapshot: "probe-snap"}
	if err := s.Validate(); err == nil {
		t.Fatal("Validate(both image+snapshot): got nil, want error")
	}
}

func TestValidate_SnapshotAloneIsValid(t *testing.T) {
	s := Spec{Name: "voltest", Snapshot: "probe-snap"}
	if err := s.Validate(); err != nil {
		t.Fatalf("Validate(snapshot only): unexpected error: %v", err)
	}
}

func TestToCreateOpts_SnapshotMapped(t *testing.T) {
	s := Spec{Name: "x", Snapshot: "probe-snap"}
	opts := s.ToCreateOpts()
	if opts.Snapshot != "probe-snap" {
		t.Errorf("Snapshot = %q, want %q", opts.Snapshot, "probe-snap")
	}
	if opts.Image != "" {
		t.Errorf("Image = %q, want empty when Snapshot is set", opts.Image)
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
secrets:
  - key: GITHUB_TOKEN
    value: ghp_x
    host: github.com
ssh_keys:
  - ssh-ed25519 AAAAedkey dan@laptop
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
	if len(s.Secrets) != 1 || s.Secrets[0].Key != "GITHUB_TOKEN" ||
		s.Secrets[0].Value != "ghp_x" || s.Secrets[0].Host != "github.com" {
		t.Errorf("Secrets = %+v, want one GITHUB_TOKEN@github.com", s.Secrets)
	}
	if len(s.SSHKeys) != 1 || s.SSHKeys[0] != "ssh-ed25519 AAAAedkey dan@laptop" {
		t.Errorf("SSHKeys = %+v, want one ed25519 line", s.SSHKeys)
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

func TestValidate_SecretsRequireAllFields(t *testing.T) {
	cases := []Spec{
		{Name: "x", Image: "alpine", Secrets: []Secret{{Value: "v", Host: "h"}}}, // no key
		{Name: "x", Image: "alpine", Secrets: []Secret{{Key: "K", Host: "h"}}},   // no value
		{Name: "x", Image: "alpine", Secrets: []Secret{{Key: "K", Value: "v"}}},  // no host
	}
	for _, s := range cases {
		if err := s.Validate(); err == nil {
			t.Errorf("Validate(%+v): got nil, want error", s)
		}
	}
}

// The key/value/host string assembles into "KEY=VALUE@HOST"; any of those
// characters appearing inside the key or host would make the arg ambiguous to
// msb. (Value can contain '=' fine — only first '=' is the separator on msb's
// side; '@' inside value is also msb's call. We refuse the structural ones.)
func TestValidate_SecretFieldsRejectSeparatorChars(t *testing.T) {
	cases := []Spec{
		{Name: "x", Image: "alpine", Secrets: []Secret{{Key: "K=Y", Value: "v", Host: "h"}}}, // = in key
		{Name: "x", Image: "alpine", Secrets: []Secret{{Key: "K@Y", Value: "v", Host: "h"}}}, // @ in key
		{Name: "x", Image: "alpine", Secrets: []Secret{{Key: "K", Value: "v", Host: "h@i"}}}, // @ in host
	}
	for _, s := range cases {
		if err := s.Validate(); err == nil {
			t.Errorf("Validate(%+v): got nil, want error", s)
		}
	}
}

func TestValidate_SSHKeysRejectMalformedLines(t *testing.T) {
	cases := []Spec{
		{Name: "x", Image: "alpine", SSHKeys: []string{""}},
		{Name: "x", Image: "alpine", SSHKeys: []string{"ssh-ed25519 AAAA\nmalicious"}},
		{Name: "x", Image: "alpine", SSHKeys: []string{"ssh-ed25519 AAAA\x00trunc"}},
	}
	for _, s := range cases {
		if err := s.Validate(); err == nil {
			t.Errorf("Validate(%+v): got nil, want error", s)
		}
	}
}

// Identifier validation (issue #3): a name beginning with '-' would be parsed
// by msb as a flag, not a value. Reject malformed names/images/snapshots before
// they can reach the adapter.
func TestValidate_RejectsMalformedName(t *testing.T) {
	cases := []Spec{
		{Name: "--force", Image: "alpine"},
		{Name: "-f", Image: "alpine"},
		{Name: "bad name", Image: "alpine"},
		{Name: "a/b", Image: "alpine"},
		{Name: ".hidden", Image: "alpine"},
	}
	for _, s := range cases {
		if err := s.Validate(); err == nil {
			t.Errorf("Validate(%+v): got nil, want error for malformed name", s)
		}
	}
}

func TestValidate_RejectsMalformedImage(t *testing.T) {
	cases := []Spec{
		{Name: "x", Image: "-alpine"},
		{Name: "x", Image: "--rm"},
		{Name: "x", Image: "al pine"},
	}
	for _, s := range cases {
		if err := s.Validate(); err == nil {
			t.Errorf("Validate(%+v): got nil, want error for malformed image", s)
		}
	}
}

func TestValidate_RejectsMalformedSnapshot(t *testing.T) {
	cases := []Spec{
		{Name: "x", Snapshot: "--force"},
		{Name: "x", Snapshot: "-f"},
		{Name: "x", Snapshot: "a/b"},
	}
	for _, s := range cases {
		if err := s.Validate(); err == nil {
			t.Errorf("Validate(%+v): got nil, want error for malformed snapshot", s)
		}
	}
}

func TestValidate_RejectsMalformedVolumeName(t *testing.T) {
	cases := []Spec{
		{Name: "x", Image: "alpine", Volume: &Volume{Name: "--force", Mount: "/workspace"}},
		{Name: "x", Image: "alpine", Volume: &Volume{Name: "-v", Mount: "/workspace"}},
		{Name: "x", Image: "alpine", Volume: &Volume{Name: "bad name", Mount: "/workspace"}},
	}
	for _, s := range cases {
		if err := s.Validate(); err == nil {
			t.Errorf("Validate(%+v): got nil, want error for malformed volume name", s)
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

func TestToCreateOpts_MapsAllFields(t *testing.T) {
	s := Spec{
		Name:   "voltest",
		Image:  "alpine",
		CPUs:   2,
		Memory: 512,
		Volume: &Volume{Name: "myvol", Mount: "/workspace"},
		Env:    map[string]string{"FOO": "bar"},
		Ports:  []PortMapping{{Host: 8080, Guest: 80}},
		Secrets: []Secret{
			{Key: "GITHUB_TOKEN", Value: "ghp_x", Host: "github.com"},
		},
		SSHKeys: []string{"ssh-ed25519 AAAAedkey dan@laptop"},
	}
	opts := s.ToCreateOpts()

	if opts.Name != "voltest" || opts.Image != "alpine" {
		t.Errorf("Name/Image = %q/%q, want voltest/alpine", opts.Name, opts.Image)
	}
	if opts.CPUs != 2 || opts.MemoryMiB != 512 {
		t.Errorf("CPUs/MemoryMiB = %d/%d, want 2/512", opts.CPUs, opts.MemoryMiB)
	}
	if opts.Volume == nil || opts.Volume.Name != "myvol" || opts.Volume.Mount != "/workspace" {
		t.Errorf("Volume = %+v, want {myvol, /workspace}", opts.Volume)
	}
	if opts.Env["FOO"] != "bar" {
		t.Errorf("Env = %+v, want FOO=bar", opts.Env)
	}
	if len(opts.Ports) != 1 || opts.Ports[0].Host != 8080 || opts.Ports[0].Guest != 80 {
		t.Errorf("Ports = %+v, want [8080:80]", opts.Ports)
	}
	if len(opts.Secrets) != 1 || opts.Secrets[0].Key != "GITHUB_TOKEN" ||
		opts.Secrets[0].Value != "ghp_x" || opts.Secrets[0].Host != "github.com" {
		t.Errorf("Secrets = %+v, want one GITHUB_TOKEN@github.com", opts.Secrets)
	}
	if len(opts.SSHKeys) != 1 || opts.SSHKeys[0] != "ssh-ed25519 AAAAedkey dan@laptop" {
		t.Errorf("SSHKeys = %+v, want one ed25519 line", opts.SSHKeys)
	}
}
