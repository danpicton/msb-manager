package msb

import (
	"os"
	"path/filepath"
	"testing"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

// Snapshot test: the `msb ls --format json` parser against a captured fixture
// (msb v0.5.2). If msb's output schema changes, this is the one place to fix.
func TestParseList(t *testing.T) {
	got, err := parseList(readFixture(t, "ls.json"))
	if err != nil {
		t.Fatalf("parseList: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("got %d sandboxes, want 1", len(got))
	}
	sb := got[0]
	if sb.Name != "jsontest" {
		t.Errorf("Name = %q, want %q", sb.Name, "jsontest")
	}
	if sb.Image != "alpine" {
		t.Errorf("Image = %q, want %q", sb.Image, "alpine")
	}
	if sb.Status != "Running" {
		t.Errorf("Status = %q, want %q", sb.Status, "Running")
	}
	if sb.CreatedAt != "2026-05-31 18:25:06" {
		t.Errorf("CreatedAt = %q, want %q", sb.CreatedAt, "2026-05-31 18:25:06")
	}
}

func TestParseListEmpty(t *testing.T) {
	got, err := parseList([]byte("[]"))
	if err != nil {
		t.Fatalf("parseList([]): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d sandboxes, want 0", len(got))
	}
}

// Snapshot test: the `msb inspect --format json` parser. This fixture answers
// open verification #1 — inspect echoes both env and mounts.
func TestParseInspect(t *testing.T) {
	got, err := parseInspect(readFixture(t, "inspect.json"))
	if err != nil {
		t.Fatalf("parseInspect: %v", err)
	}

	if got.Name != "jsontest" {
		t.Errorf("Name = %q, want %q", got.Name, "jsontest")
	}
	if got.Status != "Running" {
		t.Errorf("Status = %q, want %q", got.Status, "Running")
	}
	if got.CreatedAt != "2026-05-31 18:25:06" {
		t.Errorf("CreatedAt = %q, want %q", got.CreatedAt, "2026-05-31 18:25:06")
	}
	if got.UpdatedAt != "2026-05-31 18:25:06" {
		t.Errorf("UpdatedAt = %q, want %q", got.UpdatedAt, "2026-05-31 18:25:06")
	}
	if got.Image != "alpine" {
		t.Errorf("Image = %q, want %q (from config.image.Oci.reference)", got.Image, "alpine")
	}
	if got.CPUs != 1 {
		t.Errorf("CPUs = %d, want 1", got.CPUs)
	}
	if got.MemoryMiB != 256 {
		t.Errorf("MemoryMiB = %d, want 256", got.MemoryMiB)
	}
	if got.Workdir != "/" {
		t.Errorf("Workdir = %q, want %q", got.Workdir, "/")
	}

	// env: [["PATH", "..."]] tuples fold into a map.
	if got.Env["PATH"] != "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin" {
		t.Errorf("Env[PATH] = %q, unexpected", got.Env["PATH"])
	}

	// mounts: the auto Tmpfs at /tmp. (A named-volume fixture is still needed to
	// confirm the volume source/name is surfaced — see CONTEXT verification #1.)
	if len(got.Mounts) != 1 {
		t.Fatalf("got %d mounts, want 1", len(got.Mounts))
	}
	m := got.Mounts[0]
	if m.Guest != "/tmp" {
		t.Errorf("Mount.Guest = %q, want %q", m.Guest, "/tmp")
	}
	if m.Type != "Tmpfs" {
		t.Errorf("Mount.Type = %q, want %q", m.Type, "Tmpfs")
	}
	if m.ReadOnly {
		t.Error("Mount.ReadOnly = true, want false")
	}
	if m.SizeMiB != 64 {
		t.Errorf("Mount.SizeMiB = %d, want 64", m.SizeMiB)
	}
}

// Closes open verification #1: a named-volume mount surfaces its source name
// and a "Named" type, so the one-VM-per-volume lock is derivable from msb
// state alone (no server-owned volume map needed).
func TestParseInspect_NamedVolume(t *testing.T) {
	got, err := parseInspect(readFixture(t, "inspect_named_volume.json"))
	if err != nil {
		t.Fatalf("parseInspect: %v", err)
	}

	var named *Mount
	for i := range got.Mounts {
		if got.Mounts[i].Type == "Named" {
			named = &got.Mounts[i]
			break
		}
	}
	if named == nil {
		t.Fatalf("no Named mount found in %+v", got.Mounts)
	}
	if named.Name != "myvol" {
		t.Errorf("Mount.Name = %q, want %q", named.Name, "myvol")
	}
	if named.Guest != "/workspace" {
		t.Errorf("Mount.Guest = %q, want %q", named.Guest, "/workspace")
	}
	if named.ReadOnly {
		t.Error("Mount.ReadOnly = true, want false")
	}

	// VolumeNames is the convenience the lock will key on.
	vols := got.VolumeNames()
	if len(vols) != 1 || vols[0] != "myvol" {
		t.Errorf("VolumeNames() = %v, want [myvol]", vols)
	}
}
