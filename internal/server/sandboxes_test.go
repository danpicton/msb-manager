package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"msb-manager/internal/msb"
)

// fakeMsb is the test double for the msb adapter. List/Inspect either return
// canned data or a canned error, and the recorded calls let tests assert
// arguments (e.g. the name a /sandboxes/{name} handler forwards).
type fakeMsb struct {
	listOut    []msb.Sandbox
	listErr    error
	inspectOut msb.SandboxDetail
	inspectErr error

	gotInspectName string
}

func (f *fakeMsb) List(_ context.Context) ([]msb.Sandbox, error) {
	return f.listOut, f.listErr
}
func (f *fakeMsb) Inspect(_ context.Context, name string) (msb.SandboxDetail, error) {
	f.gotInspectName = name
	return f.inspectOut, f.inspectErr
}

func authed(method, path string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	return req
}

func TestGetSandboxes_ReturnsJSONFromMsbLs(t *testing.T) {
	client := &fakeMsb{listOut: []msb.Sandbox{
		{Name: "jsontest", Image: "alpine", Status: "Running", CreatedAt: "2026-05-31 18:25:06"},
	}}
	srv := New(Config{Token: testToken}, client)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authed(http.MethodGet, "/sandboxes"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	var got []msb.Sandbox
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("response not JSON list: %v; body=%s", err, rec.Body.String())
	}
	if len(got) != 1 || got[0].Name != "jsontest" || got[0].Image != "alpine" {
		t.Errorf("body = %+v, want one jsontest/alpine entry", got)
	}
}

func TestGetSandboxes_AdapterErrorReturns500(t *testing.T) {
	client := &fakeMsb{listErr: errors.New("boom")}
	srv := New(Config{Token: testToken}, client)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authed(http.MethodGet, "/sandboxes"))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestGetSandboxByName_ReturnsJSONFromMsbInspect(t *testing.T) {
	client := &fakeMsb{inspectOut: msb.SandboxDetail{
		Name: "jsontest", Status: "Running", Image: "alpine", CPUs: 1, MemoryMiB: 256,
	}}
	srv := New(Config{Token: testToken}, client)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authed(http.MethodGet, "/sandboxes/jsontest"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if client.gotInspectName != "jsontest" {
		t.Errorf("Inspect called with %q, want %q", client.gotInspectName, "jsontest")
	}

	var got msb.SandboxDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("response not JSON: %v; body=%s", err, rec.Body.String())
	}
	if got.Name != "jsontest" || got.Image != "alpine" {
		t.Errorf("body = %+v, want jsontest/alpine", got)
	}
}
