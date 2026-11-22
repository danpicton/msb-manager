// Package msb is the single seam wrapping the microsandbox `msb` CLI.
//
// All msb interaction lives here (ADR-0002): a Runner abstracts the
// subprocess boundary so tests can stub it; a Client composes CLI args and
// will parse `--format json` output into domain structs. Keeping it in one
// place means a CLI-output change is a one-place fix.
package msb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
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
	Run(ctx context.Context, env []string, name string, args ...string) (stdout, stderr []byte, err error)
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

// runWithEnv is the single choke point for every msb invocation. It bounds the call
// with a per-invocation timeout so a wedged msb fails the one request (freeing
// any mutex the caller holds) instead of hanging forever (issue #4). On the
// timeout firing, exec.CommandContext kills the child; we surface ErrTimeout so
// the HTTP layer maps it to 504 rather than 500.
func (c *Client) runWithEnv(ctx context.Context, env []string, args ...string) (stdout, stderr []byte, err error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	stdout, stderr, err = c.runner.Run(ctx, env, c.bin, args...)
	if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return stdout, stderr, fmt.Errorf("%w after %s: %v", ErrTimeout, c.timeout, err)
	}
	return stdout, stderr, err
}

func (c *Client) run(ctx context.Context, args ...string) (stdout, stderr []byte, err error) {
	return c.runWithEnv(ctx, nil, args...)
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
	stdout, stderr, err := c.run(ctx, "inspect", "--format", "json", "--", name)
	if err != nil {
		return SandboxDetail{}, wrapRunErr(stderr, err)
	}
	detail, err := parseInspect(stdout)
	if err != nil {
		return SandboxDetail{}, err
	}
	if err := c.identifyDiskMounts(ctx, &detail); err != nil {
		return SandboxDetail{}, err
	}
	return detail, nil
}

// wrapRunErr classifies msb's stderr into a typed sentinel where possible,
// falling back to the raw exit error. Always returns non-nil when err is
// non-nil. The single place callers should funnel runner errors through.
func wrapRunErr(stderr []byte, err error) error {
	if err == nil {
		return nil
	}
	// Timeout and over-cap are already typed sentinels; don't let stderr
	// classification (empty on a kill, arbitrary on an overflow) shadow their
	// status mapping.
	if errors.Is(err, ErrTimeout) || errors.Is(err, ErrOutputTooLarge) {
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
	Snapshot  string            // mutually exclusive with Image
	CPUs      int               // 0 = unset, don't pass --cpus
	MemoryMiB int               // 0 = unset, don't pass --memory
	Volume    *VolumeMount      // nil = unset
	Env       map[string]string // nil/empty = no -e flags
	Ports     []PortMapping     // nil/empty = no -p flags
	Secrets   []Secret          // egress credentials for image-based creates
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

// Create boots a fresh image or restores a snapshot into a detached sandbox.
func (c *Client) Create(ctx context.Context, opts CreateOpts) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var snapshotScope, source string
	var args, env []string
	if opts.Snapshot != "" {
		if len(opts.Secrets) > 0 {
			return ErrRestoreSecretsUnsupported
		}
		var err error
		source, snapshotScope, err = c.resolveSnapshot(ctx, opts.Snapshot)
		if err != nil {
			return err
		}
		// The spec supplies all external bindings. A restore may intentionally
		// omit a source volume or mount a different one at a different path.
		args = []string{"restore", "--allow-missing-resources", "-n", opts.Name}
	} else {
		source = opts.Image
		args = appendEnvArgs([]string{"create", "-n", opts.Name}, opts.Env)
		if len(opts.Secrets) > 0 {
			file, childEnv, err := writeSecretConfig(opts.Secrets)
			if err != nil {
				return err
			}
			defer os.Remove(file)
			args = append(args, "--conf", file)
			env = childEnv
		}
	}

	args, err := c.appendCreateResourceArgs(ctx, args, opts)
	if err != nil {
		return err
	}
	args = append(args, "--", source)
	if opts.Snapshot != "" {
		if err := c.restore(ctx, args, snapshotScope); err != nil {
			return err
		}
	} else if _, stderr, err := c.runWithEnv(ctx, env, args...); err != nil {
		if len(opts.Secrets) > 0 {
			// CLI config diagnostics can quote secret values, including escaped
			// forms. Preserve known error categories without logging details.
			return wrapSecretConfigError(stderr, err)
		}
		return wrapRunErr(stderr, err)
	}

	if opts.Snapshot != "" && len(opts.Env) > 0 {
		// restore has no --env. These values apply to future guest execs.
		args := append(appendEnvArgs([]string{"modify"}, opts.Env), "--", opts.Name)
		if _, stderr, err := c.run(ctx, args...); err != nil {
			return c.rollbackCreate(ctx, opts.Name, "set environment after restore", wrapRunErr(stderr, err))
		}
	}
	if len(opts.SSHKeys) > 0 {
		_, stderr, err := c.run(ctx, "exec", "--no-stdin", opts.Name, "--", "/bin/sh", "-c", sshKeysScript(opts.SSHKeys))
		if err != nil {
			return c.rollbackCreate(ctx, opts.Name, "install ssh keys after create", wrapRunErr(stderr, err))
		}
	}
	return nil
}

func (c *Client) restore(ctx context.Context, args []string, scope string) error {
	if scope != "disk" {
		// Full checkpoints must cold-boot. For a selector without a known
		// scope, v0.7.16 rejects --disk-only on disk snapshots before creating
		// the destination. Only that specific rejection permits a plain restore.
		_, stderr, err := c.run(ctx, append([]string{"restore", "--disk-only"}, args[1:]...)...)
		if err == nil || scope == "full" || ctx.Err() != nil ||
			errors.Is(err, ErrTimeout) || errors.Is(err, ErrOutputTooLarge) || !diskSnapshotRejectsDiskOnly(stderr) {
			return wrapRestoreError(stderr, err)
		}
	}
	_, stderr, err := c.run(ctx, args...)
	return wrapRestoreError(stderr, err)
}

func (c *Client) rollbackCreate(ctx context.Context, name, operation string, cause error) error {
	// A disconnected caller must not cancel cleanup. c.run still bounds it.
	_, stderr, err := c.run(context.WithoutCancel(ctx), "rm", "-f", "--", name)
	if err = wrapRunErr(stderr, err); err != nil && !errors.Is(err, ErrSandboxNotFound) {
		slog.WarnContext(ctx, "create rollback failed; sandbox may be orphaned",
			"sandbox", name, "err", err.Error())
		cause = errors.Join(cause, ErrSandboxRollbackFailed)
	}
	return fmt.Errorf("%s: %w", operation, cause)
}

func (c *Client) appendCreateResourceArgs(ctx context.Context, args []string, opts CreateOpts) ([]string, error) {
	if opts.CPUs > 0 {
		args = append(args, "-c", strconv.Itoa(opts.CPUs))
	}
	if opts.MemoryMiB > 0 {
		args = append(args, "-m", strconv.Itoa(opts.MemoryMiB))
	}
	if opts.Volume != nil {
		mountArgs, err := c.volumeMountArgs(ctx, *opts.Volume, opts.Snapshot != "")
		if err != nil {
			return nil, err
		}
		args = append(args, mountArgs...)
	}
	for _, p := range opts.Ports {
		args = append(args, "-p", strconv.Itoa(p.Host)+":"+strconv.Itoa(p.Guest))
	}
	return args, nil
}

func appendEnvArgs(args []string, env map[string]string) []string {
	for _, key := range sortedKeys(env) {
		args = append(args, "-e", key+"="+env[key])
	}
	return args
}

// writeSecretConfig keeps caller values off argv. msb expands ${...} in its
// config, so escape each opening marker using a fixed child environment
// variable. Replacement text is not re-expanded by msb. This prevents caller
// values from referring to the manager's host environment.
func writeSecretConfig(secrets []Secret) (string, []string, error) {
	type secretEntry struct {
		Value string   `json:"value"`
		Allow []string `json:"allow"`
	}
	const dollarVar = "MSB_MANAGER_LITERAL_DOLLAR"
	escape := strings.NewReplacer("${", "${"+dollarVar+"}{")
	entries := make(map[string]secretEntry, len(secrets))
	for _, secret := range secrets {
		entries[secret.Key] = secretEntry{
			Value: escape.Replace(secret.Value),
			Allow: []string{escape.Replace(secret.Host)},
		}
	}
	file, err := os.CreateTemp("", "msb-manager-secrets-*.json")
	if err != nil {
		return "", nil, fmt.Errorf("create msb secret config: %w", err)
	}
	writeErr := json.NewEncoder(file).Encode(map[string]any{"secrets": entries})
	if err := errors.Join(writeErr, file.Close()); err != nil {
		_ = os.Remove(file.Name())
		return "", nil, fmt.Errorf("write msb secret config: %w", err)
	}
	return file.Name(), []string{dollarVar + "=$"}, nil
}

// sshKeysScript builds a POSIX shell snippet that overwrites
// /root/.ssh/authorized_keys with the given keys (one per line), at the
// canonical 700/600 modes. Each key is single-quoted so an embedded comment
// containing spaces, $vars, backticks, or single quotes can't escape the
// string. Executed in the guest via `msb exec` after create or restore.
func sshKeysScript(keys []string) string {
	var sb strings.Builder
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
// quote with the standard '\” close-escape-open sequence — the only way to
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
// handlers before any name reaches here. As a belt-and-braces second layer,
// every trailing positional identifier is passed after a `--` terminator
// (rm/start/stop/inspect/metrics/logs/volume/snapshot/create-image), so even an
// identifier that slipped validation cannot be parsed as a flag. Verified
// against msb v0.5.2: each subcommand accepts `--` provided all of its own
// flags appear before it (logs/metrics therefore emit their flags first, then
// `-- <name>`).
func (c *Client) Start(ctx context.Context, name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, stderr, err := c.run(ctx, "start", "--", name)
	return wrapRunErr(stderr, err)
}

func (c *Client) Stop(ctx context.Context, name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, stderr, err := c.run(ctx, "stop", "--", name)
	return wrapRunErr(stderr, err)
}

func (c *Client) Rm(ctx context.Context, name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, stderr, err := c.run(ctx, "rm", "--", name)
	return wrapRunErr(stderr, err)
}

// ExecResult is the outcome of a command that ran inside a sandbox: captured
// output plus the command's exit code. A non-zero ExitCode is a *result*, not
// an error — the command ran; it failed on its own terms.
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Exec shells out to `msb exec --no-stdin <name> -- <argv...>` and runs argv inside the
// sandbox, request/response (no TTY, no streaming), bounded by the
// per-invocation timeout. The `--` terminates msb's flag parsing so the guest
// command — which may legitimately start with a dash — can never be parsed as
// an msb flag (issue #3 defence in depth; same shape the ssh-key install
// already uses).
//
// Exec mutates guest state only, but msb's supervisor is suspect under
// concurrent invocation (CONTEXT verification #3) and the create-path exec
// already runs under the mutating mutex, so this serialises with the other
// mutating commands too.
func (c *Client) Exec(ctx context.Context, name string, argv []string) (ExecResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Foreground msb exec can temporarily start a stopped sandbox. Require
	// a running one so exec cannot bypass the server's volume claims.
	detail, err := c.Inspect(ctx, name)
	if err != nil {
		return ExecResult{}, err
	}
	if detail.Status != "Running" {
		return ExecResult{}, ErrSandboxNotRunning
	}
	args := append([]string{"exec", "--no-stdin", name, "--"}, argv...)
	stdout, stderr, err := c.run(ctx, args...)
	return execOutcome(stdout, stderr, err)
}

// execOutcome classifies a runner result into "the command ran" (an
// ExecResult, possibly with a non-zero exit) versus "msb itself failed" (a
// typed error for the HTTP layer). Order matters: timeout first (a killed
// child also reports an exit status), then msb's recognised stderr
// categories, then an exit code is attributed to the guest command.
func execOutcome(stdout, stderr []byte, err error) (ExecResult, error) {
	if err == nil {
		return ExecResult{Stdout: string(stdout), Stderr: string(stderr)}, nil
	}
	if errors.Is(err, ErrTimeout) || errors.Is(err, ErrOutputTooLarge) {
		return ExecResult{}, err
	}
	// Anchored, not the general classifier: exec's stderr is mostly the
	// guest command's own output, and a category substring mid-stream must
	// not be mistaken for an msb failure (PR #34 review, finding 1).
	if classified := classifyErrorAnchored(string(stderr)); classified != nil {
		return ExecResult{}, classified
	}
	// *exec.ExitError satisfies this; matched as an interface so tests can
	// drive the classification without spawning a real child.
	var codeErr interface{ ExitCode() int }
	if errors.As(err, &codeErr) && codeErr.ExitCode() > 0 {
		return ExecResult{Stdout: string(stdout), Stderr: string(stderr), ExitCode: codeErr.ExitCode()}, nil
	}
	// Signal-killed (-1) or never spawned: an adapter failure.
	return ExecResult{}, err
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

// Logs shells out to `msb logs [opts...] --json -- <name>` and returns the raw
// JSONL bytes — msb owns the per-line shape, and pass-through preserves
// streaming semantics (one JSON object per line, ndjson). Read-only. All flags
// precede the `--` terminator so the name can never be parsed as a flag (issue
// #3); msb v0.5.2 rejects options that appear after `--`.
func (c *Client) Logs(ctx context.Context, name string, opts LogsOpts) ([]byte, error) {
	args := []string{"logs"}
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
	args = append(args, "--json", "--", name)
	stdout, stderr, err := c.run(ctx, args...)
	if err != nil {
		return nil, wrapRunErr(stderr, err)
	}
	return stdout, nil
}

// Metrics shells out to `msb metrics <name> --format json` and returns the
// parsed point-in-time snapshot. Read-only.
func (c *Client) Metrics(ctx context.Context, name string) (Metrics, error) {
	stdout, stderr, err := c.run(ctx, "metrics", "--format", "json", "--", name)
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

// resolveSnapshot maps existing short names to the stable reference and scope
// returned by msb. Selectors resolved by msb itself have an unknown scope here.
func (c *Client) resolveSnapshot(ctx context.Context, name string) (string, string, error) {
	if strings.Contains(name, ":") {
		return name, "", nil
	}
	rows, err := c.SnapshotList(ctx)
	if err != nil {
		return "", "", err
	}
	var matching *Snapshot
	for i := range rows {
		if rows[i].Name != name {
			continue
		}
		if matching != nil {
			return "", "", ErrSnapshotAmbiguous
		}
		matching = &rows[i]
	}
	if matching != nil {
		if matching.Reference != "" {
			name = matching.Reference
		}
		return name, matching.Scope, nil
	}
	return name, "", nil
}

// SnapshotCreate puts each API-named snapshot in its own group. Installed
// group members are immutable in msb, even when the request supplies force.
func (c *Client) SnapshotCreate(ctx context.Context, from, dest string, labels map[string]string, _ bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	rows, err := c.SnapshotList(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.Name == dest {
			return ErrSnapshotAlreadyExists
		}
	}
	detail, err := c.Inspect(ctx, from)
	if err != nil {
		return err
	}
	if detail.Status != "Stopped" {
		return ErrSandboxStillRunning
	}
	args := []string{"snapshot", "create", "--sandbox", from, "--group", dest}
	for _, k := range sortedKeys(labels) {
		args = append(args, "--label", k+"="+labels[k])
	}
	args = append(args, "--", dest)
	_, stderr, err := c.run(ctx, args...)
	return wrapRunErr(stderr, err)
}

// SnapshotRm removes the referenced snapshot.
func (c *Client) SnapshotRm(ctx context.Context, name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	reference, _, err := c.resolveSnapshot(ctx, name)
	if err != nil {
		return err
	}
	_, stderr, err := c.run(ctx, "snapshot", "rm", "--", reference)
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

// VolumeCreate provisions a disk volume with a capacity in whole MiB.
func (c *Client) VolumeCreate(ctx context.Context, name, size string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	mib, err := ParseSizeMiB(size)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidVolumeSize, err)
	}
	_, stderr, err := c.run(ctx, "volume", "create", "--kind", "disk", "--size", strconv.Itoa(mib), "--", name)
	return wrapRunErr(stderr, err)
}

// VolumeRm shells out to `msb volume rm <name>`.
func (c *Client) VolumeRm(ctx context.Context, name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, stderr, err := c.run(ctx, "volume", "rm", "--", name)
	return wrapRunErr(stderr, err)
}
