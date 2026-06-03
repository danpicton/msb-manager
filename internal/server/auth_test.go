package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

const testToken = "s3cret-token"

func doReq(h http.Handler, method, path, authHeader string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestProtectedPathWithoutTokenReturns401(t *testing.T) {
	srv := New(Config{Token: testToken}, &fakeMsb{})

	rec := doReq(srv, http.MethodGet, "/sandboxes", "")

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET /sandboxes without token: got %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestProtectedPathWithWrongTokenReturns401(t *testing.T) {
	srv := New(Config{Token: testToken}, &fakeMsb{})

	rec := doReq(srv, http.MethodGet, "/sandboxes", "Bearer wrong-token")

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET /sandboxes with wrong token: got %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

// A correct token must clear auth. With no /sandboxes handler registered yet,
// passing auth surfaces as a 404 from the protected mux — crucially not a 401.
func TestCorrectTokenClearsAuth(t *testing.T) {
	srv := New(Config{Token: testToken}, &fakeMsb{})

	rec := doReq(srv, http.MethodGet, "/sandboxes", "Bearer "+testToken)

	if rec.Code == http.StatusUnauthorized {
		t.Fatalf("GET /sandboxes with correct token: got 401, want it to clear auth")
	}
}

func TestHealthzNeedsNoToken(t *testing.T) {
	srv := New(Config{Token: testToken}, &fakeMsb{})

	rec := doReq(srv, http.MethodGet, "/healthz", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /healthz without token: got %d, want %d", rec.Code, http.StatusOK)
	}
}
