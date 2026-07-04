package msb

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"
)

// killGraceDelay is how long Wait will wait, after ctx cancellation kills the
// process, before force-closing the I/O pipes and returning. It guards against
// a grandchild process inheriting and holding stdout/stderr open, which would
// otherwise make Wait (and therefore the bounded invocation) block past its
// timeout. Set generously small — the child is already being killed.
const killGraceDelay = 5 * time.Second

// DefaultMaxCaptureBytes caps each captured output stream when
// ExecRunner.MaxCaptureBytes is unset. Every msb subcommand's own output is
// tiny; the two consumers that can legitimately grow are `logs` (bounded by
// tail/log volume) and `exec` (arbitrary guest commands — PR #34 review,
// finding 2). 32 MiB is far above any sane control-plane payload while
// keeping the worst case per invocation (two streams) at 64 MiB on a host
// that also runs the VMs.
const DefaultMaxCaptureBytes = 32 << 20

// ExecRunner is the production Runner: it spawns the binary via os/exec and
// captures stdout/stderr separately. It is the single place in the codebase
// where msb-manager forks a subprocess.
type ExecRunner struct {
	// MaxCaptureBytes bounds how much of EACH output stream is held in
	// memory; <= 0 means DefaultMaxCaptureBytes. Past the cap the runner
	// keeps draining (so the child never blocks on a full pipe) but stops
	// storing, and Run reports ErrOutputTooLarge.
	MaxCaptureBytes int
}

// Run executes name with args under ctx. stdout and stderr are returned
// separately so the caller can surface stderr text in error responses while
// parsing JSON from stdout. A non-zero exit becomes a non-nil error
// (*exec.ExitError); the caller can inspect ExitCode to map to HTTP status.
// If either stream exceeds the capture cap, the captured prefixes are
// returned with an error wrapping ErrOutputTooLarge — an over-cap invocation
// is never reported as success, even if the child exited 0.
func (r ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	// exec.CommandContext's default Cancel sends os.Kill (SIGKILL) when ctx is
	// done; WaitDelay bounds how long Wait then blocks on pending I/O before
	// giving up, so a wedged child can't keep the bounded invocation alive past
	// its timeout (issue #4).
	cmd.WaitDelay = killGraceDelay
	limit := r.MaxCaptureBytes
	if limit <= 0 {
		limit = DefaultMaxCaptureBytes
	}
	stdout := &capWriter{limit: limit}
	stderr := &capWriter{limit: limit}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	if stdout.overflow || stderr.overflow {
		if err != nil {
			// Keep the original failure visible, but the typed sentinel wins
			// the classification (a truncated stderr can't be trusted anyway).
			err = fmt.Errorf("%w (cap %d bytes per stream): %v", ErrOutputTooLarge, limit, err)
		} else {
			err = fmt.Errorf("%w (cap %d bytes per stream)", ErrOutputTooLarge, limit)
		}
	}
	return stdout.buf.Bytes(), stderr.buf.Bytes(), err
}

// capWriter stores at most limit bytes and silently discards the rest,
// flagging the overflow. Writes always report full success so os/exec's copy
// goroutine keeps draining the pipe — a child producing unbounded output runs
// on (until it exits or the ctx timeout kills it) without ever blocking, while
// the control plane's memory stays bounded.
type capWriter struct {
	buf      bytes.Buffer
	limit    int
	overflow bool
}

func (w *capWriter) Write(p []byte) (int, error) {
	if room := w.limit - w.buf.Len(); len(p) > room {
		w.overflow = true
		if room > 0 {
			w.buf.Write(p[:room])
		}
	} else {
		w.buf.Write(p)
	}
	return len(p), nil
}
