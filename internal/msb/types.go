package msb

// Sandbox is the summary view of a microsandbox microVM, as returned by
// `msb ls`. It carries only the identity/state fields the list endpoint needs.
type Sandbox struct {
	Name      string `json:"name"`
	Image     string `json:"image"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

// SandboxDetail is the full view from `msb inspect`, flattened from msb's
// nested config into the fields the control plane cares about.
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

// Mount is a guest mount point. Type distinguishes "Tmpfs" (auto, sized) from
// "Named" (a persistent microsandbox volume, which carries a source Name).
type Mount struct {
	Guest    string `json:"guest"`
	Type     string `json:"type"`
	ReadOnly bool   `json:"readonly"`
	SizeMiB  int    `json:"size_mib,omitempty"` // Tmpfs mounts only
	Name     string `json:"name,omitempty"`     // source volume name; set for Type=="Named"
}

// Volume is one row of `msb volume ls`: a named microsandbox volume.
type Volume struct {
	Name      string `json:"name"`
	QuotaMiB  int    `json:"quota_mib"`
	UsedBytes int64  `json:"used_bytes"`
	CreatedAt string `json:"created_at"`
}

// VolumeNames returns the source names of every named-volume mount. This is
// what the one-VM-per-volume lock keys on — derivable from msb state alone,
// so the lock stays stateless (CONTEXT open verification #1, resolved).
func (d SandboxDetail) VolumeNames() []string {
	var out []string
	for _, m := range d.Mounts {
		if m.Type == "Named" && m.Name != "" {
			out = append(out, m.Name)
		}
	}
	return out
}
