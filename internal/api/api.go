// Package api holds the public response DTOs for the msb-manager HTTP API and
// the functions that map internal/msb adapter types onto them. It is the
// symmetric counterpart to the inbound spec.Spec -> msb.CreateOpts seam:
// handlers translate adapter types into these DTOs before serialising, so the
// wire contract is something we own and change deliberately rather than an
// accident of how an internal scratch struct happens to be JSON-tagged
// (ADR-0006).
//
// The mapping is also the right home for omitting anything the public API must
// never expose (cf. the deliberately-dropped network subtree carrying plaintext
// secrets, CONTEXT.md "msb CLI surface").
package api

import "msb-manager/internal/msb"

// SandboxSummary is the list view of a sandbox (GET /sandboxes).
type SandboxSummary struct {
	Name      string `json:"name"`
	Image     string `json:"image"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

// NewSandboxSummary maps an adapter Sandbox onto the list DTO.
func NewSandboxSummary(s msb.Sandbox) SandboxSummary {
	return SandboxSummary{
		Name:      s.Name,
		Image:     s.Image,
		Status:    s.Status,
		CreatedAt: s.CreatedAt,
	}
}

// NewSandboxSummaries maps a slice of adapter Sandboxes. The result is always
// a non-nil slice so an empty list serialises as [] rather than null.
func NewSandboxSummaries(in []msb.Sandbox) []SandboxSummary {
	out := make([]SandboxSummary, 0, len(in))
	for _, s := range in {
		out = append(out, NewSandboxSummary(s))
	}
	return out
}

// SandboxDetail is the inspect view of a sandbox (GET /sandboxes/{name}).
// It deliberately carries no network subtree: msb's inspect output echoes
// plaintext secret values there, which the public API must never expose
// (CONTEXT.md "msb CLI surface"). The adapter parser already drops it; keeping
// the DTO free of it makes the omission part of the contract.
type SandboxDetail struct {
	Name      string `json:"name"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`

	Image     string            `json:"image"`
	CPUs      int               `json:"cpus"`
	MemoryMiB int               `json:"memory_mib"`
	Workdir   string            `json:"workdir"`
	Env       map[string]string `json:"env,omitempty"`
	Mounts    []Mount           `json:"mounts,omitempty"`
}

// Mount is a guest mount point in a SandboxDetail.
type Mount struct {
	Guest    string `json:"guest"`
	Type     string `json:"type"`
	ReadOnly bool   `json:"readonly"`
	SizeMiB  int    `json:"size_mib,omitempty"`
	Name     string `json:"name,omitempty"`
}

// NewSandboxDetail maps an adapter SandboxDetail onto the inspect DTO.
func NewSandboxDetail(d msb.SandboxDetail) SandboxDetail {
	out := SandboxDetail{
		Name:      d.Name,
		Status:    d.Status,
		CreatedAt: d.CreatedAt,
		UpdatedAt: d.UpdatedAt,
		Image:     d.Image,
		CPUs:      d.CPUs,
		MemoryMiB: d.MemoryMiB,
		Workdir:   d.Workdir,
		Env:       d.Env,
	}
	if len(d.Mounts) > 0 {
		out.Mounts = make([]Mount, 0, len(d.Mounts))
		for _, m := range d.Mounts {
			out.Mounts = append(out.Mounts, Mount{
				Guest:    m.Guest,
				Type:     m.Type,
				ReadOnly: m.ReadOnly,
				SizeMiB:  m.SizeMiB,
				Name:     m.Name,
			})
		}
	}
	return out
}

// Snapshot is the list/inspect view of a stored snapshot artifact. ParentDigest
// stays a pointer so a parentless snapshot re-encodes as `null` (not absent),
// matching what msb emits.
type Snapshot struct {
	Name         string  `json:"name"`
	Digest       string  `json:"digest"`
	ImageRef     string  `json:"image_ref"`
	Format       string  `json:"format"`
	CreatedAt    string  `json:"created_at"`
	ArtifactPath string  `json:"artifact_path"`
	ParentDigest *string `json:"parent_digest"`
	SizeBytes    int64   `json:"size_bytes"`
}

// NewSnapshot maps an adapter Snapshot onto the DTO.
func NewSnapshot(s msb.Snapshot) Snapshot {
	return Snapshot{
		Name:         s.Name,
		Digest:       s.Digest,
		ImageRef:     s.ImageRef,
		Format:       s.Format,
		CreatedAt:    s.CreatedAt,
		ArtifactPath: s.ArtifactPath,
		ParentDigest: s.ParentDigest,
		SizeBytes:    s.SizeBytes,
	}
}

// NewSnapshots maps a slice of adapter Snapshots, always returning a non-nil
// slice so an empty list serialises as [].
func NewSnapshots(in []msb.Snapshot) []Snapshot {
	out := make([]Snapshot, 0, len(in))
	for _, s := range in {
		out = append(out, NewSnapshot(s))
	}
	return out
}

// Metrics is the point-in-time resource-usage view (GET /sandboxes/{name}/metrics).
type Metrics struct {
	Name             string  `json:"name"`
	CPUPercent       float64 `json:"cpu_percent"`
	MemoryBytes      int64   `json:"memory_bytes"`
	MemoryLimitBytes int64   `json:"memory_limit_bytes"`
	DiskReadBytes    int64   `json:"disk_read_bytes"`
	DiskWriteBytes   int64   `json:"disk_write_bytes"`
	NetRxBytes       int64   `json:"net_rx_bytes"`
	NetTxBytes       int64   `json:"net_tx_bytes"`
	UptimeSecs       float64 `json:"uptime_secs"`
	Timestamp        string  `json:"timestamp"`
}

// NewMetrics maps an adapter Metrics onto the DTO.
func NewMetrics(m msb.Metrics) Metrics {
	return Metrics{
		Name:             m.Name,
		CPUPercent:       m.CPUPercent,
		MemoryBytes:      m.MemoryBytes,
		MemoryLimitBytes: m.MemoryLimitBytes,
		DiskReadBytes:    m.DiskReadBytes,
		DiskWriteBytes:   m.DiskWriteBytes,
		NetRxBytes:       m.NetRxBytes,
		NetTxBytes:       m.NetTxBytes,
		UptimeSecs:       m.UptimeSecs,
		Timestamp:        m.Timestamp,
	}
}

// Volume is the list view of a named microsandbox volume (GET /volumes).
type Volume struct {
	Name      string `json:"name"`
	QuotaMiB  int    `json:"quota_mib"`
	UsedBytes int64  `json:"used_bytes"`
	CreatedAt string `json:"created_at"`
}

// NewVolume maps an adapter Volume onto the DTO.
func NewVolume(v msb.Volume) Volume {
	return Volume{
		Name:      v.Name,
		QuotaMiB:  v.QuotaMiB,
		UsedBytes: v.UsedBytes,
		CreatedAt: v.CreatedAt,
	}
}

// NewVolumes maps a slice of adapter Volumes, always returning a non-nil slice
// so an empty list serialises as [].
func NewVolumes(in []msb.Volume) []Volume {
	out := make([]Volume, 0, len(in))
	for _, v := range in {
		out = append(out, NewVolume(v))
	}
	return out
}

// Per-item statuses for a batch volume-create (POST /volumes with a
// {volumes:[...]} body). These string values are part of the wire contract, so
// they live here in the public DTO package (ADR-0006) rather than as private
// server constants.
const (
	VolumeStatusCreated = "created" // was absent; msb volume create ran
	VolumeStatusExists  = "exists"  // present already at the requested size (no-op)
	VolumeStatusError   = "error"   // size mismatch, or msb volume create failed
)

// VolumeResult is one entry in a batch volume-create response. Error is set
// only when Status == VolumeStatusError.
type VolumeResult struct {
	Name   string `json:"name"`
	Size   string `json:"size"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// VolumeBatchResponse is the body of a batch volume-create call: the full
// per-item result list, returned with 201 (all created/exists) or 207 (any
// error). A public DTO (ADR-0006) so the wire shape is owned here.
type VolumeBatchResponse struct {
	Results []VolumeResult `json:"results"`
}
