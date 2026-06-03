package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"msb-manager/internal/msb"
)

// fakeMsb is the test double for the msb adapter. Each method either returns
// canned data/error and records its arguments so tests can assert what the
// handler forwarded.
type fakeMsb struct {
	listOut    []msb.Sandbox
	listErr    error
	inspectOut msb.SandboxDetail
	inspectErr error
	createErr  error
	startErr   error
	stopErr    error
	rmErr      error

	gotInspectName string
	gotCreateOpts  msb.CreateOpts
	gotStartName   string
	gotStopName    string
	gotRmName      string
}

func (f *fakeMsb) List(_ context.Context) ([]msb.Sandbox, error) {
	return f.listOut, f.listErr
}
func (f *fakeMsb) Inspect(_ context.Context, name string) (msb.SandboxDetail, error) {
	f.gotInspectName = name
	return f.inspectOut, f.inspectErr
}
func (f *fakeMsb) Create(_ context.Context, opts msb.CreateOpts) error {
	f.gotCreateOpts = opts
	return f.createErr
}
func (f *fakeMsb) Start(_ context.Context, name string) error {
	f.gotStartName = name
	return f.startErr
}
func (f *fakeMsb) Stop(_ context.Context, name string) error {
	f.gotStopName = name
	return f.stopErr
}
func (f *fakeMsb) Rm(_ context.Context, name string) error {
	f.gotRmName = name
	return f.rmErr
}

func authed(method, path string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	return req
}

func authedJSON(method, path, body string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/json")
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

func TestPostSandboxes_CreatesAndReturns201(t *testing.T) {
	client := &fakeMsb{}
	srv := New(Config{Token: testToken}, client)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authedJSON(http.MethodPost, "/sandboxes",
		`{"name":"voltest","image":"alpine"}`))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if client.gotCreateOpts.Name != "voltest" || client.gotCreateOpts.Image != "alpine" {
		t.Errorf("Create called with %+v, want {voltest, alpine}", client.gotCreateOpts)
	}
}

func TestPostSandboxes_AcceptsYAML(t *testing.T) {
	client := &fakeMsb{}
	srv := New(Config{Token: testToken}, client)

	body := `name: voltest
image: alpine
cpus: 2
memory: 512
volume:
  name: myvol
  mount: /workspace
env:
  PATH: /usr/bin
ports:
  - host: 8080
    guest: 80
`
	req := httptest.NewRequest(http.MethodPost, "/sandboxes", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/yaml")

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}

	got := client.gotCreateOpts
	if got.Name != "voltest" || got.Image != "alpine" {
		t.Errorf("Name/Image = %q/%q, want voltest/alpine", got.Name, got.Image)
	}
	if got.CPUs != 2 || got.MemoryMiB != 512 {
		t.Errorf("CPUs/MemoryMiB = %d/%d, want 2/512", got.CPUs, got.MemoryMiB)
	}
	if got.Volume == nil || got.Volume.Name != "myvol" || got.Volume.Mount != "/workspace" {
		t.Errorf("Volume = %+v, want {myvol, /workspace}", got.Volume)
	}
	if got.Env["PATH"] != "/usr/bin" {
		t.Errorf("Env = %+v, want PATH=/usr/bin", got.Env)
	}
	if len(got.Ports) != 1 || got.Ports[0].Host != 8080 || got.Ports[0].Guest != 80 {
		t.Errorf("Ports = %+v, want [8080:80]", got.Ports)
	}
}

// Spec validation failures (e.g. negative cpus, unknown field) must surface as
// 400 from the handler, not 500 — they're client errors.
func TestPostSandboxes_SpecValidationReturns400(t *testing.T) {
	client := &fakeMsb{}
	srv := New(Config{Token: testToken}, client)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authedJSON(http.MethodPost, "/sandboxes",
		`{"name":"voltest","image":"alpine","cpus":-1}`))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if client.gotCreateOpts.Name != "" {
		t.Error("Create called on invalid spec — should have stopped at Validate")
	}
}

func TestPostSandboxes_RejectsBadJSON(t *testing.T) {
	client := &fakeMsb{}
	srv := New(Config{Token: testToken}, client)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authedJSON(http.MethodPost, "/sandboxes", `not json`))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if client.gotCreateOpts.Name != "" {
		t.Error("Create called on bad input — should have short-circuited at parse")
	}
}

func TestPostSandboxes_RequiresNameAndImage(t *testing.T) {
	client := &fakeMsb{}
	srv := New(Config{Token: testToken}, client)

	cases := []string{
		`{}`,
		`{"name":"voltest"}`,
		`{"image":"alpine"}`,
	}
	for _, body := range cases {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, authedJSON(http.MethodPost, "/sandboxes", body))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %s: status = %d, want 400", body, rec.Code)
		}
	}
}

func TestPostSandboxStart_InvokesAndReturns204(t *testing.T) {
	client := &fakeMsb{}
	srv := New(Config{Token: testToken}, client)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authed(http.MethodPost, "/sandboxes/voltest/start"))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if client.gotStartName != "voltest" {
		t.Errorf("Start called with %q, want %q", client.gotStartName, "voltest")
	}
}

func TestPostSandboxStop_InvokesAndReturns204(t *testing.T) {
	client := &fakeMsb{}
	srv := New(Config{Token: testToken}, client)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authed(http.MethodPost, "/sandboxes/voltest/stop"))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if client.gotStopName != "voltest" {
		t.Errorf("Stop called with %q, want %q", client.gotStopName, "voltest")
	}
}

func TestDeleteSandbox_InvokesAndReturns204(t *testing.T) {
	client := &fakeMsb{}
	srv := New(Config{Token: testToken}, client)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authed(http.MethodDelete, "/sandboxes/voltest"))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if client.gotRmName != "voltest" {
		t.Errorf("Rm called with %q, want %q", client.gotRmName, "voltest")
	}
}

func TestPostSandboxStart_AdapterErrorReturns500(t *testing.T) {
	client := &fakeMsb{startErr: errors.New("boom")}
	srv := New(Config{Token: testToken}, client)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authed(http.MethodPost, "/sandboxes/voltest/start"))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}
