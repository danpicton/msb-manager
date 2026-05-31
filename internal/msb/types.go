package msb

// Sandbox is the summary view of a microsandbox microVM, as returned by
// `msb ls`. It carries only the identity/state fields the list endpoint needs.
type Sandbox struct {
	Name      string
	Image     string // base image or snapshot reference
	Status    string
	CreatedAt string
}

// SandboxDetail is the full view from `msb inspect`, flattened from msb's
// nested config into the fields the control plane cares about.
type SandboxDetail struct {
	Name      string
	Status    string
	CreatedAt string
	UpdatedAt string

	Image     string            // config.image.<variant>.reference
	CPUs      int               // config.cpus
	MemoryMiB int               // config.memory_mib
	Workdir   string            // config.workdir
	Env       map[string]string // config.env, folded from [key,value] tuples
	Mounts    []Mount           // config.mounts
}

// Mount is a guest mount point. Type distinguishes "Tmpfs" (auto) from named
// volumes; the named-volume source field is TBD until we capture a fixture for
// one (see CONTEXT open verification #1).
type Mount struct {
	Guest    string
	Type     string
	ReadOnly bool
	SizeMiB  int
}
