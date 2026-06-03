// Package msb is the single seam wrapping the microsandbox `msb` CLI.
//
// All msb interaction lives here (ADR-0002): a Runner abstracts the
// subprocess boundary so tests can stub it; a Client composes CLI args and
// will parse `--format json` output into domain structs. Keeping it in one
// place means a CLI-output change is a one-place fix.
package msb

import (
	"context"
	"sort"
	"strconv"
)

// Runner is the subprocess boundary. Tests inject a fake; production wires up
// an os/exec-backed implementation. Returning stdout and stderr separately
// lets the caller map msb's stderr text + non-zero exit into an HTTP status.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (stdout, stderr []byte, err error)
}

// Client wraps the msb CLI. It is safe for concurrent use; serialisation of
// mutating commands (if needed — see step-0 verification 3) is a layer above.
type Client struct {
	bin    string
	runner Runner
}

// NewClient binds the configured msb binary path to a Runner.
func NewClient(bin string, runner Runner) *Client {
	return &Client{bin: bin, runner: runner}
}

// List shells out to `msb ls --format json` and returns the summary view of
// every sandbox msb knows about.
func (c *Client) List(ctx context.Context) ([]Sandbox, error) {
	stdout, _, err := c.runner.Run(ctx, c.bin, "ls", "--format", "json")
	if err != nil {
		return nil, err
	}
	return parseList(stdout)
}

// Inspect shells out to `msb inspect --format json <name>` and returns the
// full detail view of a single sandbox.
func (c *Client) Inspect(ctx context.Context, name string) (SandboxDetail, error) {
	stdout, _, err := c.runner.Run(ctx, c.bin, "inspect", "--format", "json", name)
	if err != nil {
		return SandboxDetail{}, err
	}
	return parseInspect(stdout)
}

// CreateOpts is the parameter object for Client.Create. It carries the step-4
// spec fields; secrets/ssh-pubkeys/setup-script/network-policy land at step 6,
// snapshot-source at step 7.
type CreateOpts struct {
	Name      string
	Image     string
	CPUs      int               // 0 = unset, don't pass --cpus
	MemoryMiB int               // 0 = unset, don't pass --memory
	Volume    *VolumeMount      // nil = unset
	Env       map[string]string // nil/empty = no -e flags
	Ports     []PortMapping     // nil/empty = no -p flags
}

// VolumeMount is a single named-volume mount: a microsandbox volume by Name,
// surfaced at the absolute guest path Mount.
type VolumeMount struct {
	Name  string
	Mount string
}

// PortMapping is a host→guest port forward.
type PortMapping struct {
	Host  int
	Guest int
}

// Create shells out to `msb create -n <name> [opts...] <image>`. msb creates
// the sandbox and boots it in the background; a non-nil error here means the
// create itself was rejected. Boot success is observable via Inspect.
func (c *Client) Create(ctx context.Context, opts CreateOpts) error {
	args := buildCreateArgs(opts)
	_, _, err := c.runner.Run(ctx, c.bin, args...)
	return err
}

// buildCreateArgs is the pure spec→msb-args translation (CLAUDE.md's
// highest-value test seam). Env entries are emitted in sorted key order so the
// arg list is deterministic — handy for tests, audit logs, and reasoning.
func buildCreateArgs(opts CreateOpts) []string {
	args := []string{"create", "-n", opts.Name}
	if opts.CPUs > 0 {
		args = append(args, "-c", strconv.Itoa(opts.CPUs))
	}
	if opts.MemoryMiB > 0 {
		args = append(args, "-m", strconv.Itoa(opts.MemoryMiB))
	}
	if opts.Volume != nil {
		args = append(args, "-v", opts.Volume.Name+":"+opts.Volume.Mount)
	}
	for _, k := range sortedKeys(opts.Env) {
		args = append(args, "-e", k+"="+opts.Env[k])
	}
	for _, p := range opts.Ports {
		args = append(args, "-p", strconv.Itoa(p.Host)+":"+strconv.Itoa(p.Guest))
	}
	args = append(args, opts.Image)
	return args
}

func sortedKeys(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// Start, Stop, Rm wrap the corresponding `msb` verbs by name. They're
// trivially uniform; if msb ever grows per-verb flags we care about, they
// become per-method args structs the way Create did.
func (c *Client) Start(ctx context.Context, name string) error {
	_, _, err := c.runner.Run(ctx, c.bin, "start", name)
	return err
}

func (c *Client) Stop(ctx context.Context, name string) error {
	_, _, err := c.runner.Run(ctx, c.bin, "stop", name)
	return err
}

func (c *Client) Rm(ctx context.Context, name string) error {
	_, _, err := c.runner.Run(ctx, c.bin, "rm", name)
	return err
}
