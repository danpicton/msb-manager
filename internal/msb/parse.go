package msb

import (
	"encoding/json"
	"fmt"
)

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
type inspectDTO struct {
	Name      string `json:"name"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	Config    struct {
		CPUs      int        `json:"cpus"`
		MemoryMiB int        `json:"memory_mib"`
		Workdir   string     `json:"workdir"`
		Env       [][]string `json:"env"`
		// image is a Rust-style tagged enum: {"Oci": {"reference": ...}}.
		// One key (the variant); we read its reference.
		Image map[string]struct {
			Reference string `json:"reference"`
		} `json:"image"`
		Mounts []struct {
			Guest    string `json:"guest"`
			ReadOnly bool   `json:"readonly"`
			SizeMiB  int    `json:"size_mib"`
			Type     string `json:"type"`
			Name     string `json:"name"`
		} `json:"mounts"`
	} `json:"config"`
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
	}

	if len(dto.Config.Env) > 0 {
		d.Env = make(map[string]string, len(dto.Config.Env))
		for _, kv := range dto.Config.Env {
			if len(kv) == 2 {
				d.Env[kv[0]] = kv[1]
			}
		}
	}

	for _, m := range dto.Config.Mounts {
		d.Mounts = append(d.Mounts, Mount{
			Guest:    m.Guest,
			Type:     m.Type,
			ReadOnly: m.ReadOnly,
			SizeMiB:  m.SizeMiB,
			Name:     m.Name,
		})
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
