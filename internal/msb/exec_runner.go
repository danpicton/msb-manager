package msb

import (
	"bytes"
	"context"
	"os/exec"
)

// ExecRunner is the production Runner: it spawns the binary via os/exec and
// captures stdout/stderr separately. It is the single place in the codebase
// where msb-manager forks a subprocess.
type ExecRunner struct{}

// Run executes name with args under ctx. stdout and stderr are returned
// separately so the caller can surface stderr text in error responses while
// parsing JSON from stdout. A non-zero exit becomes a non-nil error
// (*exec.ExitError); the caller can inspect ExitCode to map to HTTP status.
func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}
