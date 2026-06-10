package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDo_SetsBearerAndContentType(t *testing.T) {
	var gotAuth, gotCT, gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		gotMethod = r.Method
		gotPath = r.URL.Path
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()

	c := newClient(target{URL: srv.URL, Token: "s3cr3t"})
	resp, err := c.do(context.Background(), http.MethodPost, "/sandboxes",
		strings.NewReader("body"), "application/yaml")
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	if gotAuth != "Bearer s3cr3t" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer s3cr3t")
	}
	if gotCT != "application/yaml" {
		t.Errorf("Content-Type = %q, want application/yaml", gotCT)
	}
	if gotMethod != http.MethodPost || gotPath != "/sandboxes" {
		t.Errorf("request = %s %s, want POST /sandboxes", gotMethod, gotPath)
	}
	if resp.status != http.StatusOK || string(resp.body) != `{"ok":true}` {
		t.Errorf("resp = %d %q, want 200 and the body echoed", resp.status, resp.body)
	}
}

func TestDo_NoAuthHeaderWhenTokenEmpty(t *testing.T) {
	var hadAuth bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hadAuth = r.Header["Authorization"]
	}))
	defer srv.Close()

	c := newClient(target{URL: srv.URL})
	if _, err := c.do(context.Background(), http.MethodGet, "/sandboxes", nil, ""); err != nil {
		t.Fatalf("do: %v", err)
	}
	if hadAuth {
		t.Error("Authorization header sent despite empty token")
	}
}

func TestExitCodeForStatus(t *testing.T) {
	cases := map[int]int{
		200: exitOK,
		201: exitOK,
		204: exitOK,
		207: exitPartial,
		400: exitClientError,
		404: exitClientError,
		409: exitClientError,
		500: exitServerError,
		503: exitServerError,
		302: exitGeneric,
	}
	for status, want := range cases {
		if got := exitCodeForStatus(status); got != want {
			t.Errorf("exitCodeForStatus(%d) = %d, want %d", status, got, want)
		}
	}
}

func TestServerErrorMessage_ParsesErrorField(t *testing.T) {
	got := serverErrorMessage([]byte(`{"error":"sandbox not found"}`))
	if got != "sandbox not found" {
		t.Errorf("message = %q, want %q", got, "sandbox not found")
	}
}

func TestServerErrorMessage_FallsBackToRawBody(t *testing.T) {
	got := serverErrorMessage([]byte("plain text failure\n"))
	if got != "plain text failure" {
		t.Errorf("message = %q, want trimmed raw body", got)
	}
}

func TestServerErrorMessage_EmptyBody(t *testing.T) {
	if got := serverErrorMessage(nil); got != "" {
		t.Errorf("message = %q, want empty for empty body", got)
	}
}

// The token must never reach any output stream. Render a server error and
// assert the secret does not appear in what we'd print to stderr.
func TestRenderError_NeverLeaksToken(t *testing.T) {
	const token = "super-secret-token-value"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"unauthorized"}`)
	}))
	defer srv.Close()

	c := newClient(target{URL: srv.URL, Token: token})
	resp, err := c.do(context.Background(), http.MethodGet, "/sandboxes", nil, "")
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	var stderr bytes.Buffer
	renderServerError(&stderr, resp.status, resp.body)
	if strings.Contains(stderr.String(), token) {
		t.Fatal("token leaked into rendered error output")
	}
	if !strings.Contains(stderr.String(), "unauthorized") {
		t.Errorf("rendered error %q should carry the server message", stderr.String())
	}
}

func TestRequireToken_ClearErrorWhenMissing(t *testing.T) {
	err := requireToken(target{URL: defaultURL})
	if err == nil {
		t.Fatal("requireToken with no token should return an error, not nil")
	}
	if !strings.Contains(err.Error(), "token") {
		t.Errorf("error %q should mention the missing token", err.Error())
	}
}

func TestRequireToken_OKWhenPresent(t *testing.T) {
	if err := requireToken(target{URL: defaultURL, Token: "t"}); err != nil {
		t.Errorf("requireToken with a token should succeed, got %v", err)
	}
}
