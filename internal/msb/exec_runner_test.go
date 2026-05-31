package msb

import (
	"context"
	"strings"
	"testing"
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
