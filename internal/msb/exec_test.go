package msb

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

// exitCodeError stands in for *exec.ExitError: any error exposing ExitCode()
// (matched via errors.As on the interface) carries the child's exit status.
type exitCodeError struct{ code int }

func (e exitCodeError) Error() string { return fmt.Sprintf("exit status %d", e.code) }
func (e exitCodeError) ExitCode() int { return e.code }

func TestClientExec_InvokesMsbExecWithTerminator(t *testing.T) {
	r := &fakeRunner{stdout: []byte("hi\n")}
	c := NewClient("msb", r)

	res, err := c.Exec(context.Background(), "web", []string{"sh", "-c", "echo hi"})
	if err != nil {
		t.Fatalf("Exec: unexpected error: %v", err)
	}

	// `--` terminates msb's own flag parsing so the guest command (which may
	// legitimately start with a dash) can never be parsed as an msb flag.
	wantArgs := []string{"exec", "web", "--", "sh", "-c", "echo hi"}
	if !reflect.DeepEqual(r.gotArgs, wantArgs) {
		t.Errorf("invoked args = %v, want %v", r.gotArgs, wantArgs)
	}
	want := ExecResult{Stdout: "hi\n"}
	if res != want {
		t.Errorf("result = %+v, want %+v", res, want)
	}
}

// A non-zero exit that msb's stderr doesn't classify is the guest command's
// own outcome — the API's success case, not an adapter error.
func TestExecOutcome_NonZeroExitIsCommandResult(t *testing.T) {
	res, err := execOutcome([]byte(""), []byte("ls: /nope: No such file or directory\n"), exitCodeError{2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := ExecResult{Stderr: "ls: /nope: No such file or directory\n", ExitCode: 2}
	if res != want {
		t.Errorf("result = %+v, want %+v", res, want)
	}
}

// Recognised msb failures stay typed errors so the HTTP layer maps them
// (404 here), never masquerading as a command that ran and failed.
func TestExecOutcome_SandboxNotFoundStaysError(t *testing.T) {
	_, err := execOutcome(nil, []byte("error: sandbox not found: web\n"), exitCodeError{1})
	if !errors.Is(err, ErrSandboxNotFound) {
		t.Errorf("err = %v, want ErrSandboxNotFound", err)
	}
}

// A timed-out invocation must surface ErrTimeout (504), not be mistaken for
// the command exiting non-zero — the checks are ordered timeout-first.
func TestExecOutcome_TimeoutStaysError(t *testing.T) {
	wrapped := fmt.Errorf("%w after 60s: signal: killed", ErrTimeout)
	_, err := execOutcome(nil, nil, wrapped)
	if !errors.Is(err, ErrTimeout) {
		t.Errorf("err = %v, want ErrTimeout", err)
	}
}

// ExitCode -1 means the child was killed by a signal, not that the command
// completed; that is an adapter failure (500), not a command outcome.
func TestExecOutcome_SignalKillIsError(t *testing.T) {
	_, err := execOutcome(nil, nil, exitCodeError{-1})
	if err == nil {
		t.Error("want an error for a signal-killed child, got success")
	}
}

// A spawn failure (no exit code at all) is likewise an adapter error.
func TestExecOutcome_SpawnFailureIsError(t *testing.T) {
	_, err := execOutcome(nil, nil, errors.New("fork/exec: no such file"))
	if err == nil {
		t.Error("want an error for a spawn failure, got success")
	}
}

// --- guest-stderr spoof resistance (PR #34 review, finding 1) ---

// Exec's stderr is mostly guest-authored, so a category substring appearing
// mid-output (a grep result, a traceback, a log line) must NOT be mistaken
// for msb's own error. Only stderr that *starts with* msb's `error: ` line
// classifies — a genuine msb failure happens before the guest produces any
// output, so it always sits at position zero.
func TestExecOutcome_GuestStderrCannotSpoofClassification(t *testing.T) {
	for name, stderr := range map[string]string{
		"substring mid-line":  "grep: sandbox not found: no matches in app.log\n",
		"msb-shaped mid-body": "step 1 ok\nerror: sandbox not found: web\n",
		"volume category":     "warn: volume not found: cache (ignored)\n",
	} {
		t.Run(name, func(t *testing.T) {
			res, err := execOutcome(nil, []byte(stderr), exitCodeError{3})
			if err != nil {
				t.Fatalf("guest output was misclassified as an msb error: %v", err)
			}
			if res.ExitCode != 3 || res.Stderr != stderr {
				t.Errorf("result = %+v, want exit 3 with stderr passed through", res)
			}
		})
	}
}

// Leading whitespace must not defeat the anchor for a genuine msb error.
func TestExecOutcome_AnchoredMsbErrorStillClassifies(t *testing.T) {
	_, err := execOutcome(nil, []byte("\nerror: sandbox not found: web\n"), exitCodeError{1})
	if !errors.Is(err, ErrSandboxNotFound) {
		t.Errorf("err = %v, want ErrSandboxNotFound", err)
	}
}

// --- output cap (PR #34 review, finding 2) ---

// An over-cap capture is an adapter failure (500), never attributed to the
// guest command as a truncated-but-successful result.
func TestExecOutcome_OutputTooLargeStaysError(t *testing.T) {
	wrapped := fmt.Errorf("%w: stdout passed 1024 bytes", ErrOutputTooLarge)
	_, err := execOutcome([]byte("partial"), nil, wrapped)
	if !errors.Is(err, ErrOutputTooLarge) {
		t.Errorf("err = %v, want ErrOutputTooLarge", err)
	}
}
