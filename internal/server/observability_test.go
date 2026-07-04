package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Logs query params are forwarded verbatim as msb argv flag values. A
// flag-shaped value must 400 before any subprocess runs, matching the
// 400-before-subprocess contract of the other handlers (issues #3, #23).
func TestLogsFlagShapedParamsRejectedBeforeSubprocess(t *testing.T) {
	for _, query := range []string{"since=--follow", "until=-1h", "grep=-v", "source=--all"} {
		fake := &fakeMsb{logsOut: []byte("{}\n")}
		srv := New(Config{Token: testToken}, fake)

		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, authed(http.MethodGet, "/sandboxes/web/logs?"+query))

		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d, want %d", query, rec.Code, http.StatusBadRequest)
		}
		if fake.gotLogsName != "" {
			t.Errorf("%s: Logs was called for %q, want no subprocess", query, fake.gotLogsName)
		}
	}
}

func TestLogsUnknownSourceRejected(t *testing.T) {
	fake := &fakeMsb{logsOut: []byte("{}\n")}
	srv := New(Config{Token: testToken}, fake)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authed(http.MethodGet, "/sandboxes/web/logs?source=journal"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("source=journal: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if fake.gotLogsName != "" {
		t.Fatalf("source=journal: Logs was called, want no subprocess")
	}
}

func TestLogsValidParamsPassThrough(t *testing.T) {
	fake := &fakeMsb{logsOut: []byte("{}\n")}
	srv := New(Config{Token: testToken}, fake)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authed(http.MethodGet,
		"/sandboxes/web/logs?since=1h&source=stdout,stderr&grep=err"))

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	got := fake.gotLogsOpts
	if got.Since != "1h" || got.Source != "stdout,stderr" || got.Grep != "err" {
		t.Fatalf("forwarded opts = %+v, want since=1h source=stdout,stderr grep=err", got)
	}
}
