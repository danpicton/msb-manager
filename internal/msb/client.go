// Package msb is the single seam wrapping the microsandbox `msb` CLI.
//
// All msb interaction lives here (ADR-0002): a Runner abstracts the
// subprocess boundary so tests can stub it; a Client composes CLI args and
// will parse `--format json` output into domain structs. Keeping it in one
// place means a CLI-output change is a one-place fix.
package msb

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Runner is the subprocess boundary. Tests inject a fake; production wires up
// an os/exec-backed implementation. Returning stdout and stderr separately
// lets the caller map msb's stderr text + non-zero exit into an HTTP status.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (stdout, stderr []byte, err error)
}

// Client wraps the msb CLI. Safe for concurrent use.
//
// msb v0.5.2 is not concurrent-safe under mutating commands (CONTEXT
// verification #3 — parallel `msb create` left the supervisor unable to
// service `msb ls`). Mutating methods (Create/Start/Stop/Rm) therefore take a
// per-process mutex, serialising every msb invocation that changes state. Read
// methods (List/Inspect) don't take the mutex — they're cheap and don't race
// the supervisor against itself.
type Client struct {
	bin    string
	runner Runner
	mu     sync.Mutex
}

// NewClient binds the configured msb binary path to a Runner.
func NewClient(bin string, runner Runner) *Client {
	return &Client{bin: bin, runner: runner}
}

// List shells out to `msb ls --format json` and returns the summary view of
// every sandbox msb knows about.
func (c *Client) List(ctx context.Context) ([]Sandbox, error) {
	stdout, stderr, err := c.runner.Run(ctx, c.bin, "ls", "--format", "json")
	if err != nil {
		return nil, wrapRunErr(stderr, err)
	}
	return parseList(stdout)
}

// Inspect shells out to `msb inspect --format json <name>` and returns the
// full detail view of a single sandbox.
func (c *Client) Inspect(ctx context.Context, name string) (SandboxDetail, error) {
	stdout, stderr, err := c.runner.Run(ctx, c.bin, "inspect", "--format", "json", name)
	if err != nil {
		return SandboxDetail{}, wrapRunErr(stderr, err)
	}
	return parseInspect(stdout)
}

// wrapRunErr classifies msb's stderr into a typed sentinel where possible,
// falling back to the raw exit error. Always returns non-nil when err is
// non-nil. The single place callers should funnel runner errors through.
func wrapRunErr(stderr []byte, err error) error {
	if err == nil {
		return nil
	}
	if classified := classifyError(string(stderr)); classified != nil {
		return classified
	}
	return err
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
	Secrets   []Secret          // nil/empty = no --secret flags
	SSHKeys   []string          // OpenSSH-format pubkey lines; installed via --script
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

// Secret is an egress credential injected into the guest as an env var, but
// only released to outbound traffic destined for Host. msb enforces the
// allow-list at the network policy layer.
type Secret struct {
	Key   string
	Value string
	Host  string
}

// Create shells out to `msb create -n <name> [opts...] <image>`. msb creates
// the sandbox and boots it in the background; a non-nil error here means the
// create itself was rejected. Boot success is observable via Inspect.
// sshKeyScriptName is the registered script name on PATH inside the guest
// (msb's --script-raw places it at /.msb/scripts/<name>).
const sshKeyScriptName = "install-ssh-keys"

func (c *Client) Create(ctx context.Context, opts CreateOpts) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	args := buildCreateArgs(opts)
	if _, stderr, err := c.runner.Run(ctx, c.bin, args...); err != nil {
		return wrapRunErr(stderr, err)
	}
	// msb's --script REGISTERS scripts on PATH but doesn't auto-run them. The
	// keys install body was registered by `create`; now run it via `exec` so
	// authorized_keys actually lands. On failure roll back with `rm -f` so the
	// caller sees atomic "either fully created or nothing" semantics.
	if len(opts.SSHKeys) > 0 {
		_, stderr, err := c.runner.Run(ctx, c.bin, "exec", opts.Name, sshKeyScriptName)
		if err != nil {
			_, _, _ = c.runner.Run(ctx, c.bin, "rm", "-f", opts.Name)
			return fmt.Errorf("install ssh keys after create: %w", wrapRunErr(stderr, err))
		}
	}
	return nil
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
	for _, s := range opts.Secrets {
		args = append(args, "--secret", s.Key+"="+s.Value+"@"+s.Host)
	}
	if len(opts.SSHKeys) > 0 {
		// --script-raw (not --script) so msb does NOT decode \n/\t escapes;
		// our printf format strings rely on staying literal. The trade-off is
		// we must include our own shebang.
		args = append(args, "--script-raw", sshKeyScriptName+"="+sshKeysScript(opts.SSHKeys))
	}
	args = append(args, opts.Image)
	return args
}

// sshKeysScript builds a POSIX shell snippet that overwrites
// /root/.ssh/authorized_keys with the given keys (one per line), at the
// canonical 700/600 modes. Each key is single-quoted so an embedded comment
// containing spaces, $vars, backticks, or single quotes can't escape the
// string. Registered via --script-raw and executed post-create via
// `msb exec <name> install-ssh-keys`.
func sshKeysScript(keys []string) string {
	var sb strings.Builder
	sb.WriteString("#!/bin/sh\n") // --script-raw doesn't insert one for us
	sb.WriteString("set -eu\n")
	sb.WriteString("mkdir -p /root/.ssh\n")
	sb.WriteString("chmod 700 /root/.ssh\n")
	sb.WriteString("{\n")
	for _, k := range keys {
		sb.WriteString("printf '%s\\n' ")
		sb.WriteString(shellSingleQuote(k))
		sb.WriteByte('\n')
	}
	sb.WriteString("} > /root/.ssh/authorized_keys\n")
	sb.WriteString("chmod 600 /root/.ssh/authorized_keys\n")
	return sb.String()
}

// shellSingleQuote wraps s in single quotes, replacing any embedded single
// quote with the standard '\'' close-escape-open sequence — the only way to
// embed a literal ' inside a POSIX single-quoted string.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
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
	c.mu.Lock()
	defer c.mu.Unlock()
	_, stderr, err := c.runner.Run(ctx, c.bin, "start", name)
	return wrapRunErr(stderr, err)
}

func (c *Client) Stop(ctx context.Context, name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, stderr, err := c.runner.Run(ctx, c.bin, "stop", name)
	return wrapRunErr(stderr, err)
}

func (c *Client) Rm(ctx context.Context, name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, stderr, err := c.runner.Run(ctx, c.bin, "rm", name)
	return wrapRunErr(stderr, err)
}

// VolumeList shells out to `msb volume ls --format json`. Read-only, so it
// doesn't take the mutating-commands mutex.
func (c *Client) VolumeList(ctx context.Context) ([]Volume, error) {
	stdout, stderr, err := c.runner.Run(ctx, c.bin, "volume", "ls", "--format", "json")
	if err != nil {
		return nil, wrapRunErr(stderr, err)
	}
	return parseVolumeList(stdout)
}

// VolumeCreate shells out to `msb volume create --size <size> <name>`. size is
// passed through verbatim (e.g. "1G", "512M") — msb owns the unit grammar.
func (c *Client) VolumeCreate(ctx context.Context, name, size string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, stderr, err := c.runner.Run(ctx, c.bin, "volume", "create", "--size", size, name)
	return wrapRunErr(stderr, err)
}

// VolumeRm shells out to `msb volume rm <name>`.
func (c *Client) VolumeRm(ctx context.Context, name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, stderr, err := c.runner.Run(ctx, c.bin, "volume", "rm", name)
	return wrapRunErr(stderr, err)
}
