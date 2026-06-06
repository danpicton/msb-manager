package server

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthzReturns200(t *testing.T) {
	srv := New(Config{}, &fakeMsb{})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /healthz: got status %d, want %d", rec.Code, http.StatusOK)
	}
}

// /readyz is a deeper check than /healthz — it confirms msb itself is
// reachable. Returns 200 when msb ls succeeds; 503 when it errors. Also
// unauthenticated, so Caddy/systemd can probe it the same way as /healthz.
func TestReadyz_OKWhenMsbReachable(t *testing.T) {
	srv := New(Config{}, &fakeMsb{})

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

func TestReadyz_503WhenMsbUnreachable(t *testing.T) {
	srv := New(Config{}, &fakeMsb{listErr: errors.New("msb is down")})

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d, want 503", rec.Code)
	}
}

func TestReadyz_NeedsNoToken(t *testing.T) {
	srv := New(Config{Token: "s3cret"}, &fakeMsb{})

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("got %d, want 200 without auth", rec.Code)
	}
}
