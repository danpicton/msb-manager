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

// List shells out to `msb ls --format json` and returns the raw bytes.
// Typed parsing will land once we have captured fixtures to snapshot-test
// against (step-0 verification 1).
func (c *Client) List(ctx context.Context) ([]byte, error) {
	stdout, _, err := c.runner.Run(ctx, c.bin, "ls", "--format", "json")
	if err != nil {
		return nil, err
	}
	return stdout, nil
}
