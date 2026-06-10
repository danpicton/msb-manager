package api

import (
	"encoding/json"
	"testing"

	"msb-manager/internal/msb"
)

// The contract these tests pin: a DTO's JSON encoding is byte-for-byte
// identical to the msb adapter type it is mapped from. That is what makes the
// internal/api seam a *pure refactor* (ADR-0006) — the wire shape clients see
// does not move, but it is now a thing we own rather than an accident of an
// internal struct's tags.

// sameJSON marshals both values and fails unless the bytes match exactly.
func sameJSON(t *testing.T, dto, adapter any) {
	t.Helper()
	gotDTO, err := json.Marshal(dto)
	if err != nil {
		t.Fatalf("marshal DTO: %v", err)
	}
	gotAdapter, err := json.Marshal(adapter)
	if err != nil {
		t.Fatalf("marshal adapter type: %v", err)
	}
	if string(gotDTO) != string(gotAdapter) {
		t.Errorf("wire shape changed:\n  DTO:     %s\n  adapter: %s", gotDTO, gotAdapter)
	}
}

func TestNewSandboxSummary_WireShapeUnchanged(t *testing.T) {
	s := msb.Sandbox{
		Name:      "jsontest",
		Image:     "alpine",
		Status:    "Running",
		CreatedAt: "2026-05-31 18:25:06",
	}
	sameJSON(t, NewSandboxSummary(s), s)
}

func TestNewSandboxSummaries_EmptyIsNonNilSlice(t *testing.T) {
	// The list endpoints must serialise [] not null for an empty result; the
	// mapping helper owns that guarantee so handlers don't repeat the nil check.
	got := NewSandboxSummaries(nil)
	if got == nil {
		t.Fatal("NewSandboxSummaries(nil) = nil, want non-nil empty slice")
	}
	sameJSON(t, got, []msb.Sandbox{})
}

func TestNewSandboxDetail_WireShapeUnchanged(t *testing.T) {
	d := msb.SandboxDetail{
		Name:      "jsontest",
		Status:    "Running",
		CreatedAt: "2026-05-31 18:25:06",
		UpdatedAt: "2026-05-31 18:30:00",
		Image:     "alpine",
		CPUs:      2,
		MemoryMiB: 512,
		Workdir:   "/workspace",
		Env:       map[string]string{"PATH": "/usr/bin"},
		Mounts: []msb.Mount{
			{Guest: "/tmp", Type: "Tmpfs", SizeMiB: 64},
			{Guest: "/workspace", Type: "Named", Name: "myvol", ReadOnly: false},
		},
	}
	sameJSON(t, NewSandboxDetail(d), d)
}

func TestNewSandboxDetail_OmitemptyMatches(t *testing.T) {
	// A bare detail (no env, no mounts) must omit those keys exactly as the
	// adapter type does, so omitempty parity is part of the contract.
	d := msb.SandboxDetail{Name: "bare", Status: "Stopped", Image: "alpine"}
	sameJSON(t, NewSandboxDetail(d), d)
}

func TestNewSnapshot_WireShapeUnchanged_WithParent(t *testing.T) {
	parent := "sha256:parentdigest"
	s := msb.Snapshot{
		Name: "probe-snap", Digest: "sha256:digestx", ImageRef: "alpine",
		Format: "raw", CreatedAt: "2026-06-06 07:52:18",
		ArtifactPath: "/x/probe-snap", ParentDigest: &parent, SizeBytes: 4294967296,
	}
	sameJSON(t, NewSnapshot(s), s)
}

func TestNewSnapshot_NullParentRoundTrips(t *testing.T) {
	// msb returns parent_digest: null (not absent) for a parentless snapshot;
	// the DTO must re-encode null, not omit the key.
	s := msb.Snapshot{Name: "root", Digest: "sha256:x", ParentDigest: nil}
	sameJSON(t, NewSnapshot(s), s)
}

func TestNewSnapshots_EmptyIsNonNilSlice(t *testing.T) {
	got := NewSnapshots(nil)
	if got == nil {
		t.Fatal("NewSnapshots(nil) = nil, want non-nil empty slice")
	}
	sameJSON(t, got, []msb.Snapshot{})
}

func TestNewMetrics_WireShapeUnchanged(t *testing.T) {
	m := msb.Metrics{
		Name: "probe", CPUPercent: 1.5, MemoryBytes: 80666624,
		MemoryLimitBytes: 268435456, DiskReadBytes: 1024, DiskWriteBytes: 2048,
		NetRxBytes: 512, NetTxBytes: 256, UptimeSecs: 2.004,
		Timestamp: "2026-06-06T08:12:50.545+00:00",
	}
	sameJSON(t, NewMetrics(m), m)
}

func TestNewVolume_WireShapeUnchanged(t *testing.T) {
	v := msb.Volume{Name: "v1", QuotaMiB: 1024, UsedBytes: 0, CreatedAt: "2026-06-04 17:45:29"}
	sameJSON(t, NewVolume(v), v)
}

func TestNewVolumes_EmptyIsNonNilSlice(t *testing.T) {
	got := NewVolumes(nil)
	if got == nil {
		t.Fatal("NewVolumes(nil) = nil, want non-nil empty slice")
	}
	sameJSON(t, got, []msb.Volume{})
}
