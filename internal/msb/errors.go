package msb

import (
	"errors"
	"fmt"
	"strings"
)

// Sentinel errors for the discriminable failures msb surfaces on stderr. The
// adapter wraps recognised categories so callers can errors.Is() instead of
// substring-matching error text themselves.
//
// Captured patterns (msb v0.5.2, format `error: <category>: <details>`):
//   - "sandbox not found: <name>"
//   - "sandbox already exists: <details>"
//   - "sandbox still running: <details>"
var (
	ErrSandboxNotFound       = errors.New("msb: sandbox not found")
	ErrSandboxAlreadyExists  = errors.New("msb: sandbox already exists")
	ErrSandboxStillRunning   = errors.New("msb: sandbox still running")
	ErrVolumeAlreadyExists   = errors.New("msb: volume already exists")
	ErrVolumeNotFound        = errors.New("msb: volume not found")
	ErrSnapshotAlreadyExists = errors.New("msb: snapshot already exists")
	ErrSnapshotNotFound      = errors.New("msb: snapshot not found")

	// ErrTimeout is returned when an msb invocation exceeds its bounded
	// lifetime (issue #4). It's distinct from a non-zero exit so the HTTP layer
	// can map it to 504 Gateway Timeout rather than 500.
	ErrTimeout = errors.New("msb: command timed out")

	// ErrOutputTooLarge is returned when a child's stdout or stderr exceeded
	// the runner's capture cap (PR #34 review, finding 2). The captured prefix
	// is still returned alongside it for logging, but the invocation is an
	// adapter failure — never a truncated-but-successful result.
	ErrOutputTooLarge = errors.New("msb: command output exceeded capture limit")
)

// classifyError inspects msb's stderr text and, if a known category is found,
// returns a sentinel wrapped with the original detail. Returns nil if nothing
// recognised — the caller keeps the raw exit error in that case (mapped to 500
// by the HTTP layer).
//
// Substring matching is intentional: msb's category prefix is stable enough
// for v1, and a wording drift breaks tests loudly rather than silently
// mis-routing statuses.
func classifyError(stderr string) error {
	s := strings.TrimSpace(stderr)
	for _, c := range classifierTable {
		if i := strings.Index(s, c.prefix); i >= 0 {
			detail := strings.TrimSpace(s[i+len(c.prefix):])
			return fmt.Errorf("%w: %s", c.err, detail)
		}
	}
	return nil
}

// classifyErrorAnchored is classifyError for streams that are NOT purely
// msb-authored — today, exec's stderr, which is mostly the guest command's
// output (PR #34 review, finding 1). It only honours a category that appears
// at the very start of stderr as msb's own `error: <category>: <details>`
// line. That anchor holds because a genuine msb failure (sandbox not found)
// happens *before* the guest produces any output; a category substring in the
// middle of guest output (a grep hit, a traceback) can no longer collide.
// A guest that deliberately prints the exact prefix first can still spoof —
// but only the caller's own request, which the single trust domain already
// lets it do more directly.
func classifyErrorAnchored(stderr string) error {
	s := strings.TrimSpace(stderr)
	const errPrefix = "error: "
	if !strings.HasPrefix(s, errPrefix) {
		return nil
	}
	rest := s[len(errPrefix):]
	for _, c := range classifierTable {
		if strings.HasPrefix(rest, c.prefix) {
			detail := strings.TrimSpace(rest[len(c.prefix):])
			return fmt.Errorf("%w: %s", c.err, detail)
		}
	}
	return nil
}

var classifierTable = []struct {
	prefix string
	err    error
}{
	{"sandbox not found:", ErrSandboxNotFound},
	{"sandbox already exists:", ErrSandboxAlreadyExists},
	{"sandbox still running:", ErrSandboxStillRunning},
	{"volume already exists:", ErrVolumeAlreadyExists},
	{"volume not found:", ErrVolumeNotFound},
	{"snapshot already exists:", ErrSnapshotAlreadyExists},
	{"snapshot not found:", ErrSnapshotNotFound},
	// `msb logs` uses non-standard wording when the sandbox doesn't exist;
	// fold it into the same sentinel so HTTP still maps to 404.
	{"no logs directory for sandbox", ErrSandboxNotFound},
}
