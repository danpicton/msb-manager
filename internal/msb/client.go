// Package msb is the single seam wrapping the microsandbox `msb` CLI.
//
// All msb interaction lives here (ADR-0002): a Runner abstracts the
// subprocess boundary so tests can stub it; a Client composes CLI args and
// will parse `--format json` output into domain structs. Keeping it in one
// place means a CLI-output change is a one-place fix.
package msb

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultCmdTimeout bounds a single msb invocation when no override is
// configured. msb v0.5.2 can hang under contention (CONTEXT verification #3),
// and the mutating mutex means one hung call would otherwise block every other
// mutating request; the timeout caps the blast radius to a single request.
const DefaultCmdTimeout = 60 * time.Second

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
	bin     string
	runner  Runner
	timeout time.Duration
	mu      sync.Mutex
}

// NewClient binds the configured msb binary path to a Runner, using
// DefaultCmdTimeout for each invocation.
func NewClient(bin string, runner Runner) *Client {
	return NewClientWithTimeout(bin, runner, DefaultCmdTimeout)
}

// NewClientWithTimeout is NewClient with an explicit per-invocation timeout. A
// non-positive timeout falls back to DefaultCmdTimeout so a misconfiguration
// can't disable the bound entirely.
func NewClientWithTimeout(bin string, runner Runner, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = DefaultCmdTimeout
	}
	return &Client{bin: bin, runner: runner, timeout: timeout}
}

// run is the single choke point for every msb invocation. It bounds the call
// with a per-invocation timeout so a wedged msb fails the one request (freeing
// any mutex the caller holds) instead of hanging forever (issue #4). On the
// timeout firing, exec.CommandContext kills the child; we surface ErrTimeout so
// the HTTP layer maps it to 504 rather than 500.
func (c *Client) run(ctx context.Context, args ...string) (stdout, stderr []byte, err error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	stdout, stderr, err = c.runner.Run(ctx, c.bin, args...)
	if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return stdout, stderr, fmt.Errorf("%w after %s: %v", ErrTimeout, c.timeout, err)
	}
	return stdout, stderr, err
}

// List shells out to `msb ls --format json` and returns the summary view of
// every sandbox msb knows about.
func (c *Client) List(ctx context.Context) ([]Sandbox, error) {
	stdout, stderr, err := c.run(ctx, "ls", "--format", "json")
	if err != nil {
		return nil, wrapRunErr(stderr, err)
	}
	return parseList(stdout)
}

// Inspect shells out to `msb inspect --format json <name>` and returns the
// full detail view of a single sandbox.
func (c *Client) Inspect(ctx context.Context, name string) (SandboxDetail, error) {
	stdout, stderr, err := c.run(ctx, "inspect", "--format", "json", name)
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
	// A timeout is already a typed sentinel; don't let stderr classification
	// (usually empty on a kill) shadow the 504 mapping.
	if errors.Is(err, ErrTimeout) {
		return err
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
	Image     string            // mutually exclusive with Snapshot
	Snapshot  string            // mutually exclusive with Image; dispatches to `msb run -d --snapshot`
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
	if _, stderr, err := c.run(ctx, args...); err != nil {
		return wrapRunErr(stderr, err)
	}
	// msb's --script REGISTERS scripts on PATH but doesn't auto-run them. The
	// keys install body was registered by `create`; now run it via `exec` so
	// authorized_keys actually lands. On failure roll back with `rm -f` so the
	// caller sees atomic "either fully created or nothing" semantics.
	if len(opts.SSHKeys) > 0 {
		// `--` terminates msb's own flag parsing so the script name is taken
		// as the command rather than as another flag.
		_, stderr, err := c.run(ctx, "exec", opts.Name, "--", sshKeyScriptName)
		if err != nil {
			_, _, _ = c.run(ctx, "rm", "-f", opts.Name)
			return fmt.Errorf("install ssh keys after create: %w", wrapRunErr(stderr, err))
		}
	}
	return nil
}

// buildCreateArgs is the pure spec→msb-args translation (CLAUDE.md's
// highest-value test seam). Env entries are emitted in sorted key order so the
// arg list is deterministic — handy for tests, audit logs, and reasoning.
//
// Two dispatch paths:
//   - Image set     → `msb create -n <name> [opts...] <image>`
//   - Snapshot set  → `msb run -d --snapshot <name> -n <name> [opts...]`
//
// msb v0.5.2 only accepts --snapshot on `run`, not `create`; -d (--detach)
// gives `run` the same "boot in background, print name" semantics as create.
// Validation (mutual exclusion of Image/Snapshot) lives in the spec layer.
func buildCreateArgs(opts CreateOpts) []string {
	var args []string
	if opts.Snapshot != "" {
		args = []string{"run", "-d", "--snapshot", opts.Snapshot, "-n", opts.Name}
	} else {
		args = []string{"create", "-n", opts.Name}
	}
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
	// For the image path, IMAGE is the trailing positional. For the snapshot
	// path, msb run takes --snapshot in place of the image — nothing trails.
	if opts.Snapshot == "" {
		args = append(args, opts.Image)
	}
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
//
// Defence-in-depth note (issue #3): the primary guard against flag injection
// is ValidName/ValidImage/ValidSize, applied in spec.Validate() and the path
// handlers before any name reaches here. As a belt-and-braces second layer we
// would also pass positional identifiers after a `--` terminator (as Create
// already does for `exec … -- install-ssh-keys`), so even an identifier that
// slipped validation couldn't be parsed as a flag. Adding `--` to the read and
// lifecycle verbs (rm/start/stop/inspect/metrics/logs/volume/snapshot) needs a
// real msb to confirm each subcommand accepts the terminator without changing
// behaviour — TODO once an msb binary is available. Not blocking: validation
// already closes the vector for well-behaved inputs.
func (c *Client) Start(ctx context.Context, name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, stderr, err := c.run(ctx, "start", name)
	return wrapRunErr(stderr, err)
}

func (c *Client) Stop(ctx context.Context, name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, stderr, err := c.run(ctx, "stop", name)
	return wrapRunErr(stderr, err)
}

func (c *Client) Rm(ctx context.Context, name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, stderr, err := c.run(ctx, "rm", name)
	return wrapRunErr(stderr, err)
}

// LogsOpts mirrors the v1 subset of `msb logs` flags we expose through the
// HTTP API. Zero/empty fields don't appear in the args. --follow is
// deliberately absent (build plan: fetch-only, no streaming).
type LogsOpts struct {
	Tail   int    // 0 = no --tail
	Since  string // "" = no --since (msb accepts RFC3339 or "5m"/"1h"/etc.)
	Until  string // "" = no --until
	Source string // "" = no --source ("stdout,stderr,output,system,all")
	Grep   string // "" = no --grep (regex over body)
}

// Logs shells out to `msb logs <name> [opts...] --json` and returns the raw
// JSONL bytes — msb owns the per-line shape, and pass-through preserves
// streaming semantics (one JSON object per line, ndjson). Read-only.
func (c *Client) Logs(ctx context.Context, name string, opts LogsOpts) ([]byte, error) {
	args := []string{"logs", name}
	if opts.Tail > 0 {
		args = append(args, "--tail", strconv.Itoa(opts.Tail))
	}
	if opts.Since != "" {
		args = append(args, "--since", opts.Since)
	}
	if opts.Until != "" {
		args = append(args, "--until", opts.Until)
	}
	if opts.Source != "" {
		args = append(args, "--source", opts.Source)
	}
	if opts.Grep != "" {
		args = append(args, "--grep", opts.Grep)
	}
	args = append(args, "--json")
	stdout, stderr, err := c.run(ctx, args...)
	if err != nil {
		return nil, wrapRunErr(stderr, err)
	}
	return stdout, nil
}

// Metrics shells out to `msb metrics <name> --format json` and returns the
// parsed point-in-time snapshot. Read-only.
func (c *Client) Metrics(ctx context.Context, name string) (Metrics, error) {
	stdout, stderr, err := c.run(ctx, "metrics", name, "--format", "json")
	if err != nil {
		return Metrics{}, wrapRunErr(stderr, err)
	}
	return parseMetrics(stdout)
}

// SnapshotList shells out to `msb snapshot ls --format json`. Read-only.
func (c *Client) SnapshotList(ctx context.Context) ([]Snapshot, error) {
	stdout, stderr, err := c.run(ctx, "snapshot", "ls", "--format", "json")
	if err != nil {
		return nil, wrapRunErr(stderr, err)
	}
	return parseSnapshotList(stdout)
}

// SnapshotCreate shells out to:
//
//	msb snapshot create --from <from> [--label k=v ...] [--force] <dest>
//
// from must be a stopped sandbox; dest is the snapshot name. Labels are
// emitted in sorted key order for deterministic args.
func (c *Client) SnapshotCreate(ctx context.Context, from, dest string, labels map[string]string, force bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	args := []string{"snapshot", "create", "--from", from}
	for _, k := range sortedKeys(labels) {
		args = append(args, "--label", k+"="+labels[k])
	}
	if force {
		args = append(args, "--force")
	}
	args = append(args, dest)
	_, stderr, err := c.run(ctx, args...)
	return wrapRunErr(stderr, err)
}

// SnapshotRm shells out to `msb snapshot rm <name>`.
func (c *Client) SnapshotRm(ctx context.Context, name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, stderr, err := c.run(ctx, "snapshot", "rm", name)
	return wrapRunErr(stderr, err)
}

// VolumeList shells out to `msb volume ls --format json`. Read-only, so it
// doesn't take the mutating-commands mutex.
func (c *Client) VolumeList(ctx context.Context) ([]Volume, error) {
	stdout, stderr, err := c.run(ctx, "volume", "ls", "--format", "json")
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
	_, stderr, err := c.run(ctx, "volume", "create", "--size", size, name)
	return wrapRunErr(stderr, err)
}

// VolumeRm shells out to `msb volume rm <name>`.
func (c *Client) VolumeRm(ctx context.Context, name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, stderr, err := c.run(ctx, "volume", "rm", name)
	return wrapRunErr(stderr, err)
}
