package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"msb-manager/internal/msb"
)

func TestExecSandbox_RunsCommandAndReturnsResult(t *testing.T) {
	client := &fakeMsb{execOut: msb.ExecResult{Stdout: "hi\n"}}
	srv := New(Config{Token: testToken}, client)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authedJSON(http.MethodPost, "/sandboxes/web/exec",
		`{"command":["sh","-c","echo hi"]}`))

	assertBody(t, rec, `{"stdout":"hi\n","stderr":"","exit_code":0}`)
	if client.gotExecName != "web" {
		t.Errorf("exec name = %q, want web", client.gotExecName)
	}
	if got, want := strings.Join(client.gotExecArgv, " "), "sh -c echo hi"; got != want {
		t.Errorf("exec argv = %q, want %q", got, want)
	}
}

// The command running and exiting non-zero is a successful control-plane
// operation: HTTP 200 with the exit code in the body, not an HTTP error.
func TestExecSandbox_NonZeroExitIs200(t *testing.T) {
	client := &fakeMsb{execOut: msb.ExecResult{Stderr: "no such file\n", ExitCode: 2}}
	srv := New(Config{Token: testToken}, client)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authedJSON(http.MethodPost, "/sandboxes/web/exec", `{"command":["ls","/nope"]}`))

	assertBody(t, rec, `{"stdout":"","stderr":"no such file\n","exit_code":2}`)
}

// The body is YAML-or-JSON like every other endpoint; a YAML command list
// must work the same way.
func TestExecSandbox_AcceptsYAMLBody(t *testing.T) {
	client := &fakeMsb{}
	srv := New(Config{Token: testToken}, client)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authedJSON(http.MethodPost, "/sandboxes/web/exec", "command: [uptime]"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if len(client.gotExecArgv) != 1 || client.gotExecArgv[0] != "uptime" {
		t.Errorf("exec argv = %v, want [uptime]", client.gotExecArgv)
	}
}

func TestExecSandbox_EmptyCommandIs400(t *testing.T) {
	for name, body := range map[string]string{
		"missing":       `{}`,
		"empty list":    `{"command":[]}`,
		"empty argv[0]": `{"command":[""]}`,
	} {
		t.Run(name, func(t *testing.T) {
			client := &fakeMsb{}
			srv := New(Config{Token: testToken}, client)

			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, authedJSON(http.MethodPost, "/sandboxes/web/exec", body))

			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			if client.gotExecName != "" {
				t.Error("adapter Exec was invoked for an invalid body")
			}
		})
	}
}

// A typo'd field must be an error, not a silent drop (matching spec.Parse and
// parseVolumeManifest).
func TestExecSandbox_UnknownFieldIs400(t *testing.T) {
	srv := New(Config{Token: testToken}, &fakeMsb{})

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authedJSON(http.MethodPost, "/sandboxes/web/exec",
		`{"command":["ls"],"comand_env":{"A":"b"}}`))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// A path name msb could parse as a flag must be rejected before any
// subprocess runs (issue #3).
func TestExecSandbox_InvalidNameIs400(t *testing.T) {
	client := &fakeMsb{}
	srv := New(Config{Token: testToken}, client)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authedJSON(http.MethodPost, "/sandboxes/%2D%2Dforce/exec", `{"command":["ls"]}`))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	if client.gotExecName != "" {
		t.Error("adapter Exec was invoked for an invalid name")
	}
}

func TestExecSandbox_NotFoundMapsTo404(t *testing.T) {
	srv := New(Config{Token: testToken}, &fakeMsb{execErr: msb.ErrSandboxNotFound})

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authedJSON(http.MethodPost, "/sandboxes/ghost/exec", `{"command":["ls"]}`))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

func TestExecSandbox_OversizeBodyIs413(t *testing.T) {
	srv := New(Config{Token: testToken}, &fakeMsb{})

	big := `{"command":["sh","-c","` + strings.Repeat("x", maxSpecBytes) + `"]}`
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authedJSON(http.MethodPost, "/sandboxes/web/exec", big))

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", rec.Code)
	}
}

// An over-cap capture surfaces as a clear 500 telling the caller what
// happened, not a generic "exec failed" (PR #34 review, finding 2).
func TestExecSandbox_OutputTooLargeIs500WithClearMessage(t *testing.T) {
	srv := New(Config{Token: testToken}, &fakeMsb{execErr: msb.ErrOutputTooLarge})

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authedJSON(http.MethodPost, "/sandboxes/web/exec", `{"command":["yes"]}`))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "output exceeded") {
		t.Errorf("body = %s, want a message naming the output cap", rec.Body.String())
	}
}
