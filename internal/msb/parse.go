package msb

import (
	"encoding/json"
	"fmt"
)

// parseMetrics decodes `msb metrics <name> --format json` output (a single
// point-in-time snapshot of one sandbox's resource usage).
func parseMetrics(data []byte) (Metrics, error) {
	var m Metrics
	if err := json.Unmarshal(data, &m); err != nil {
		return Metrics{}, fmt.Errorf("parse msb metrics: %w", err)
	}
	return m, nil
}

// parseSnapshotList decodes `msb snapshot ls --format json` output. The CLI
// shape maps 1:1, so a direct struct decode suffices.
func parseSnapshotList(data []byte) ([]Snapshot, error) {
	var out []Snapshot
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse msb snapshot ls: %w", err)
	}
	return out, nil
}

// parseVolumeList decodes `msb volume ls --format json` output.
func parseVolumeList(data []byte) ([]Volume, error) {
	var rows []struct {
		Volume
		CapacityBytes int64 `json:"capacity_bytes"`
	}
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, fmt.Errorf("parse msb volume ls: %w", err)
	}
	out := make([]Volume, len(rows))
	for i, row := range rows {
		out[i] = row.Volume
		if row.CapacityBytes > 0 {
			out[i].QuotaMiB = int(row.CapacityBytes / (1 << 20))
		}
	}
	return out, nil
}

// parseList decodes `msb ls --format json` output into summary Sandboxes.
// The CLI shape maps 1:1, so a direct struct decode suffices.
func parseList(data []byte) ([]Sandbox, error) {
	var raw []struct {
		Name      string `json:"name"`
		Image     string `json:"image"`
		Status    string `json:"status"`
		CreatedAt string `json:"created_at"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse msb ls: %w", err)
	}

	out := make([]Sandbox, len(raw))
	for i, r := range raw {
		out[i] = Sandbox(r)
	}
	return out, nil
}

// inspectDTO mirrors the nested `msb inspect --format json` shape closely
// enough to extract what we need; unmapped fields are ignored.
//
// SECURITY NOTE: do NOT add a field for `config.network.secrets`. msb v0.5.2
// echoes the secret value in plaintext under that subtree, and our HTTP
// /sandboxes/{name} re-encodes whatever ends up in SandboxDetail. Keeping the
// secrets block unparsed makes the leak impossible at the type level. See
// TestParseInspect_DoesNotLeakSecretValue for the regression guard.
type inspectDTO struct {
	Name         string        `json:"name"`
	Status       string        `json:"status"`
	CreatedAt    string        `json:"created_at"`
	UpdatedAt    string        `json:"updated_at"`
	Config       inspectConfig `json:"config"`
	ActiveConfig *struct {
		Mounts []inspectMount `json:"mounts"`
	} `json:"active_config"`
}

type inspectResources struct {
	CPUs      int `json:"cpus"`
	MemoryMiB int `json:"memory_mib"`
}

type inspectConfig struct {
	inspectResources                   // older, flat config
	Resources        *inspectResources `json:"resources"`
	Workdir          string            `json:"workdir"`
	Runtime          *struct {
		Workdir string `json:"workdir"`
	} `json:"runtime"`
	Env []json.RawMessage `json:"env"`
	// image is a tagged enum, such as {"Oci": {"reference": "alpine"}}.
	Image map[string]struct {
		Reference string `json:"reference"`
	} `json:"image"`
	Mounts []inspectMount `json:"mounts"`
}

type inspectMount struct {
	Mount
	Host    string `json:"host"`
	Options *struct {
		ReadOnly bool `json:"readonly"`
	} `json:"options"`
}

func inspectMounts(rows []inspectMount) []Mount {
	var mounts []Mount
	for _, row := range rows {
		mount := row.Mount
		if row.Options != nil {
			mount.ReadOnly = row.Options.ReadOnly
		}
		if mount.Type == "DiskImage" {
			mount.DiskPath = row.Host
		}
		mounts = append(mounts, mount)
	}
	return mounts
}

// parseInspect decodes `msb inspect --format json` output, flattening msb's
// nested config into a SandboxDetail.
func parseInspect(data []byte) (SandboxDetail, error) {
	var dto inspectDTO
	if err := json.Unmarshal(data, &dto); err != nil {
		return SandboxDetail{}, fmt.Errorf("parse msb inspect: %w", err)
	}

	d := SandboxDetail{
		Name:      dto.Name,
		Status:    dto.Status,
		CreatedAt: dto.CreatedAt,
		UpdatedAt: dto.UpdatedAt,
		CPUs:      dto.Config.CPUs,
		MemoryMiB: dto.Config.MemoryMiB,
		Workdir:   dto.Config.Workdir,
		Image:     imageReference(dto.Config.Image),
		Mounts:    inspectMounts(dto.Config.Mounts),
	}
	if dto.Config.Resources != nil {
		d.CPUs = dto.Config.Resources.CPUs
		d.MemoryMiB = dto.Config.Resources.MemoryMiB
	}
	if dto.Config.Runtime != nil {
		d.Workdir = dto.Config.Runtime.Workdir
	}
	if dto.ActiveConfig != nil {
		mounts := inspectMounts(dto.ActiveConfig.Mounts)
		d.ActiveMounts = &mounts
	}

	if len(dto.Config.Env) > 0 {
		d.Env = make(map[string]string, len(dto.Config.Env))
		for _, raw := range dto.Config.Env {
			if raw[0] == '[' {
				var pair []string
				if err := json.Unmarshal(raw, &pair); err != nil {
					return SandboxDetail{}, fmt.Errorf("parse msb inspect env: %w", err)
				}
				if len(pair) == 2 {
					d.Env[pair[0]] = pair[1]
				}
				continue
			}
			var entry struct{ Key, Value string }
			if err := json.Unmarshal(raw, &entry); err != nil {
				return SandboxDetail{}, fmt.Errorf("parse msb inspect env: %w", err)
			}
			if entry.Key != "" {
				d.Env[entry.Key] = entry.Value
			}
		}
	}

	return d, nil
}

// imageReference pulls the reference out of msb's single-variant image enum,
// regardless of which variant key (Oci, …) it carries.
func imageReference(img map[string]struct {
	Reference string `json:"reference"`
}) string {
	for _, v := range img {
		if v.Reference != "" {
			return v.Reference
		}
	}
	return ""
}
