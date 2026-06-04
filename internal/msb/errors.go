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
	ErrSandboxNotFound      = errors.New("msb: sandbox not found")
	ErrSandboxAlreadyExists = errors.New("msb: sandbox already exists")
	ErrSandboxStillRunning  = errors.New("msb: sandbox still running")
	ErrVolumeAlreadyExists  = errors.New("msb: volume already exists")
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

var classifierTable = []struct {
	prefix string
	err    error
}{
	{"sandbox not found:", ErrSandboxNotFound},
	{"sandbox already exists:", ErrSandboxAlreadyExists},
	{"sandbox still running:", ErrSandboxStillRunning},
	{"volume already exists:", ErrVolumeAlreadyExists},
}
