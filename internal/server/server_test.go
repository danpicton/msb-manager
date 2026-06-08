package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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

// /readyz is unauthenticated so probes can hit it; a short-TTL cache collapses
// a burst of calls into a single `msb ls`, removing the DoS-amplification
// vector (issue #6). N rapid calls must produce exactly one List.
func TestReadyz_CachesAcrossBurst(t *testing.T) {
	client := &fakeMsb{}
	srv := New(Config{Token: testToken}, client)

	for i := 0; i < 10; i++ {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("call %d: got %d, want 200", i, rec.Code)
		}
	}

	if client.listCalls != 1 {
		t.Errorf("List invoked %d times across a burst, want 1 (cached)", client.listCalls)
	}
}

// A canceled request context (client disconnect) must NOT poison the cache:
// otherwise every /readyz caller would see 503 for the whole TTL even though
// msb is healthy (review #2). A genuine error is still cached.
func TestReadinessCache_DoesNotCacheCanceledContext(t *testing.T) {
	rc := &readinessCache{ttl: time.Minute}
	client := &fakeMsb{listErr: context.Canceled}

	if err := rc.ready(context.Background(), client); !errors.Is(err, context.Canceled) {
		t.Fatalf("ready: got %v, want context.Canceled", err)
	}
	// Second call must re-run List (the canceled error was not cached).
	_ = rc.ready(context.Background(), client)
	if client.listCalls != 2 {
		t.Errorf("List called %d times, want 2 (canceled error must not be cached)", client.listCalls)
	}
}

func TestReadinessCache_CachesGenuineError(t *testing.T) {
	rc := &readinessCache{ttl: time.Minute}
	client := &fakeMsb{listErr: errors.New("msb is down")}

	_ = rc.ready(context.Background(), client)
	_ = rc.ready(context.Background(), client)
	if client.listCalls != 1 {
		t.Errorf("List called %d times, want 1 (genuine error should cache)", client.listCalls)
	}
}
