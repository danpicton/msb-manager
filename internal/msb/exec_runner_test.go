package msb

import (
	"context"
	"strings"
	"testing"
	"time"
)

// These tests use real subprocesses (true/false/printf — POSIX coreutils that
// are present anywhere Go's test suite runs). They guard the contract of
// ExecRunner: stdout/stderr captured separately, non-zero exit surfaced as an
// error.

func TestExecRunner_CapturesStdout(t *testing.T) {
	r := ExecRunner{}
	stdout, stderr, err := r.Run(context.Background(), "printf", "%s", "hello")
	if err != nil {
		t.Fatalf("Run printf: unexpected error: %v", err)
	}
	if string(stdout) != "hello" {
		t.Errorf("stdout = %q, want %q", stdout, "hello")
	}
	if len(stderr) != 0 {
		t.Errorf("stderr = %q, want empty", stderr)
	}
}

func TestExecRunner_NonZeroExitReturnsError(t *testing.T) {
	r := ExecRunner{}
	_, _, err := r.Run(context.Background(), "false")
	if err == nil {
		t.Fatal("Run false: got nil error, want non-zero exit error")
	}
}

// A cancelled context must kill the child and let Run return promptly, rather
// than waiting out the full sleep. Uses `sleep` (a coreutil), not msb — this
// guards exec.CommandContext's kill semantics that issue #4 relies on.
func TestExecRunner_ContextCancellationKillsProcess(t *testing.T) {
	r := ExecRunner{}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, _, err := r.Run(ctx, "sleep", "30")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Run sleep under cancelled ctx: got nil error, want kill error")
	}
	// Must return far sooner than the 30s sleep — the kill, plus at most the
	// WaitDelay grace, not the full duration.
	if elapsed > 10*time.Second {
		t.Errorf("Run took %v, want prompt return after context cancellation", elapsed)
	}
}

func TestExecRunner_CapturesStderr(t *testing.T) {
	r := ExecRunner{}
	// `sh -c` is portable; route a known string to stderr.
	_, stderr, err := r.Run(context.Background(), "sh", "-c", "printf boom 1>&2")
	if err != nil {
		t.Fatalf("Run sh: unexpected error: %v", err)
	}
	if !strings.Contains(string(stderr), "boom") {
		t.Errorf("stderr = %q, want to contain %q", stderr, "boom")
	}
}
