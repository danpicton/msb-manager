package msb

import (
	"errors"
	"testing"
)

// Fixtures captured from a live msb v0.5.2 against a real sandbox/host. If msb
// changes its error wording these tests fail loudly — and the substring
// matching in classifyError is the one place to update.
var classifyCases = []struct {
	name     string
	stderr   string
	wantErr  error
	wantText string // substring that must appear in the wrapped error message
}{
	{
		name:     "not found",
		stderr:   "error: sandbox not found: nope\n",
		wantErr:  ErrSandboxNotFound,
		wantText: "nope",
	},
	{
		name:     "already exists",
		stderr:   "error: sandbox already exists: sandbox 'probe' already exists; remove it, start the stopped sandbox, or recreate with .replace()\n",
		wantErr:  ErrSandboxAlreadyExists,
		wantText: "probe",
	},
	{
		name:     "still running",
		stderr:   "error: sandbox still running: cannot remove sandbox 'probe': still running\n",
		wantErr:  ErrSandboxStillRunning,
		wantText: "probe",
	},
	{
		name:     "volume already exists",
		stderr:   "error: volume already exists: myvol\n",
		wantErr:  ErrVolumeAlreadyExists,
		wantText: "myvol",
	},
	{
		name:    "unknown stays unknown",
		stderr:  "error: something we have not seen before\n",
		wantErr: nil, // classifyError returns nil for unrecognised → caller keeps the raw exit error
	},
	{
		name:    "empty stderr stays unknown",
		stderr:  "",
		wantErr: nil,
	},
}

func TestClassifyError(t *testing.T) {
	for _, tc := range classifyCases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyError(tc.stderr)
			if tc.wantErr == nil {
				if got != nil {
					t.Errorf("got %v, want nil (unrecognised)", got)
				}
				return
			}
			if !errors.Is(got, tc.wantErr) {
				t.Errorf("errors.Is mismatch: got %v, want wrap of %v", got, tc.wantErr)
			}
			if tc.wantText != "" && !containsSubstring(got.Error(), tc.wantText) {
				t.Errorf("error %q does not contain %q", got.Error(), tc.wantText)
			}
		})
	}
}

func containsSubstring(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
