package msb

import (
	"bytes"
	"context"
	"os/exec"
	"time"
)

// killGraceDelay is how long Wait will wait, after ctx cancellation kills the
// process, before force-closing the I/O pipes and returning. It guards against
// a grandchild process inheriting and holding stdout/stderr open, which would
// otherwise make Wait (and therefore the bounded invocation) block past its
// timeout. Set generously small — the child is already being killed.
const killGraceDelay = 5 * time.Second

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
	// exec.CommandContext's default Cancel sends os.Kill (SIGKILL) when ctx is
	// done; WaitDelay bounds how long Wait then blocks on pending I/O before
	// giving up, so a wedged child can't keep the bounded invocation alive past
	// its timeout (issue #4).
	cmd.WaitDelay = killGraceDelay
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}
