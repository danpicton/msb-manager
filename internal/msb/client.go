// Package msb is the single seam wrapping the microsandbox `msb` CLI.
//
// All msb interaction lives here (ADR-0002): a Runner abstracts the
// subprocess boundary so tests can stub it; a Client composes CLI args and
// will parse `--format json` output into domain structs. Keeping it in one
// place means a CLI-output change is a one-place fix.
package msb

import "context"

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

// CreateOpts is the image-only create surface. Volume/env/secrets/ports/
// network policy land with steps 4–6 (spec parsing, volumes+lock, credentials).
type CreateOpts struct {
	Name  string
	Image string
}

// Create shells out to `msb create -n <name> <image>`. msb creates the
// sandbox and boots it in the background; a non-nil error here means the
// create itself was rejected (the sandbox may or may not have come up — the
// caller can poll Inspect to find out).
func (c *Client) Create(ctx context.Context, opts CreateOpts) error {
	_, _, err := c.runner.Run(ctx, c.bin, "create", "-n", opts.Name, opts.Image)
	return err
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
