package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"msb-manager/internal/lock"
	"msb-manager/internal/msb"
)

// newWithLock is shorthand for the test cases that need a non-zero VolumeLock.
// Pre-existing tests still go through New(cfg, client) and get a fresh lock.
func newWithLock(cfg Config, client MsbClient, vlock *lock.VolumeLock) http.Handler {
	return NewWithLock(cfg, client, vlock)
}

// fakeMsb is the test double for the msb adapter. Each method either returns
// canned data/error and records its arguments so tests can assert what the
// handler forwarded.
type fakeMsb struct {
	listOut          []msb.Sandbox
	listErr          error
	inspectOut       msb.SandboxDetail
	inspectErr       error
	createErr        error
	startErr         error
	stopErr          error
	rmErr            error
	volumeListOut    []msb.Volume
	volumeListErr    error
	volumeCreateErr  error
	volumeRmErr      error
	snapshotListOut   []msb.Snapshot
	snapshotListErr   error
	snapshotCreateErr error
	snapshotRmErr     error

	gotInspectName     string
	gotCreateOpts      msb.CreateOpts
	gotStartName       string
	gotStopName        string
	gotRmName          string
	gotVolumeCreate    [2]string // name, size
	gotVolumeRm        string
	gotSnapshotCreate  snapshotCall
	gotSnapshotRm      string
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
func (f *fakeMsb) VolumeList(_ context.Context) ([]msb.Volume, error) {
	return f.volumeListOut, f.volumeListErr
}
func (f *fakeMsb) VolumeCreate(_ context.Context, name, size string) error {
	f.gotVolumeCreate = [2]string{name, size}
	return f.volumeCreateErr
}
func (f *fakeMsb) VolumeRm(_ context.Context, name string) error {
	f.gotVolumeRm = name
	return f.volumeRmErr
}
func (f *fakeMsb) SnapshotList(_ context.Context) ([]msb.Snapshot, error) {
	return f.snapshotListOut, f.snapshotListErr
}
func (f *fakeMsb) SnapshotCreate(_ context.Context, from, dest string, labels map[string]string, force bool) error {
	f.gotSnapshotCreate = snapshotCall{From: from, Dest: dest, Labels: labels, Force: force}
	return f.snapshotCreateErr
}
func (f *fakeMsb) SnapshotRm(_ context.Context, name string) error {
	f.gotSnapshotRm = name
	return f.snapshotRmErr
}

type snapshotCall struct {
	From, Dest string
	Labels     map[string]string
	Force      bool
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

// Typed msb sentinels map to specific HTTP statuses: not-found → 404,
// already-exists / still-running → 409.
func TestStatusMapping_NotFound(t *testing.T) {
	cases := []struct {
		name string
		req  *http.Request
		set  func(*fakeMsb)
	}{
		{
			name: "GET inspect",
			req:  authed(http.MethodGet, "/sandboxes/nope"),
			set:  func(f *fakeMsb) { f.inspectErr = msb.ErrSandboxNotFound },
		},
		{
			name: "POST start",
			req:  authed(http.MethodPost, "/sandboxes/nope/start"),
			set:  func(f *fakeMsb) { f.startErr = msb.ErrSandboxNotFound },
		},
		{
			name: "POST stop",
			req:  authed(http.MethodPost, "/sandboxes/nope/stop"),
			set:  func(f *fakeMsb) { f.stopErr = msb.ErrSandboxNotFound },
		},
		{
			name: "DELETE",
			req:  authed(http.MethodDelete, "/sandboxes/nope"),
			set:  func(f *fakeMsb) { f.rmErr = msb.ErrSandboxNotFound },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeMsb{}
			tc.set(client)
			srv := New(Config{Token: testToken}, client)

			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, tc.req)

			if rec.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

// VolumeLock integration: a sandbox declaring a volume must not be created
// while another sandbox already claims that volume. The 409 surfaces the
// holder so the user knows what to stop/rm.
func TestPostSandboxes_VolumeBusyReturns409(t *testing.T) {
	client := &fakeMsb{}
	vlock := lock.New()
	_ = vlock.Acquire("myvol", "alice") // alice already holds it
	srv := newWithLock(Config{Token: testToken}, client, vlock)

	body := `name: bob
image: alpine
volume:
  name: myvol
  mount: /workspace
`
	req := httptest.NewRequest(http.MethodPost, "/sandboxes", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/yaml")

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	if client.gotCreateOpts.Name != "" {
		t.Error("Create called despite volume conflict; lock should short-circuit")
	}
}

func TestPostSandboxes_AdapterFailureRollsBackVolumeClaim(t *testing.T) {
	client := &fakeMsb{createErr: errors.New("boom")}
	vlock := lock.New()
	srv := newWithLock(Config{Token: testToken}, client, vlock)

	body := `name: alice
image: alpine
volume:
  name: myvol
  mount: /workspace
`
	req := httptest.NewRequest(http.MethodPost, "/sandboxes", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/yaml")

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	// Lock should NOT still be held — the failed Create must roll back.
	if err := vlock.Acquire("myvol", "carol"); err != nil {
		t.Errorf("myvol should be free after failed Create; got %v", err)
	}
}

func TestPostSandboxes_SuccessClaimsVolume(t *testing.T) {
	client := &fakeMsb{}
	vlock := lock.New()
	srv := newWithLock(Config{Token: testToken}, client, vlock)

	body := `name: alice
image: alpine
volume:
  name: myvol
  mount: /workspace
`
	req := httptest.NewRequest(http.MethodPost, "/sandboxes", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/yaml")

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	// myvol must now be held by alice.
	if err := vlock.Acquire("myvol", "bob"); !errors.Is(err, lock.ErrVolumeBusy) {
		t.Errorf("myvol should be alice's after successful Create; got %v", err)
	}
}

// Start needs to look up the sandbox's volumes (via Inspect) before claiming.
func TestPostSandboxStart_AcquiresVolumesFromInspect(t *testing.T) {
	client := &fakeMsb{
		inspectOut: msb.SandboxDetail{
			Name:   "alice",
			Mounts: []msb.Mount{{Type: "Named", Name: "myvol", Guest: "/workspace"}},
		},
	}
	vlock := lock.New()
	srv := newWithLock(Config{Token: testToken}, client, vlock)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authed(http.MethodPost, "/sandboxes/alice/start"))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if err := vlock.Acquire("myvol", "bob"); !errors.Is(err, lock.ErrVolumeBusy) {
		t.Errorf("myvol should be alice's after successful Start; got %v", err)
	}
}

func TestPostSandboxStart_VolumeBusyReturns409(t *testing.T) {
	client := &fakeMsb{
		inspectOut: msb.SandboxDetail{
			Name:   "bob",
			Mounts: []msb.Mount{{Type: "Named", Name: "myvol", Guest: "/workspace"}},
		},
	}
	vlock := lock.New()
	_ = vlock.Acquire("myvol", "alice") // alice already running with myvol
	srv := newWithLock(Config{Token: testToken}, client, vlock)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authed(http.MethodPost, "/sandboxes/bob/start"))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	if client.gotStartName != "" {
		t.Error("Start invoked despite volume conflict")
	}
}

// Stop and Delete release the sandbox's claims so other sandboxes can start.
func TestPostSandboxStop_ReleasesVolumes(t *testing.T) {
	client := &fakeMsb{}
	vlock := lock.New()
	_ = vlock.Acquire("myvol", "alice")
	srv := newWithLock(Config{Token: testToken}, client, vlock)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authed(http.MethodPost, "/sandboxes/alice/stop"))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if err := vlock.Acquire("myvol", "bob"); err != nil {
		t.Errorf("myvol should be free after Stop(alice); got %v", err)
	}
}

func TestDeleteSandbox_ReleasesVolumes(t *testing.T) {
	client := &fakeMsb{}
	vlock := lock.New()
	_ = vlock.Acquire("myvol", "alice")
	srv := newWithLock(Config{Token: testToken}, client, vlock)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authed(http.MethodDelete, "/sandboxes/alice"))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if err := vlock.Acquire("myvol", "bob"); err != nil {
		t.Errorf("myvol should be free after Delete(alice); got %v", err)
	}
}

func TestStatusMapping_Conflict(t *testing.T) {
	cases := []struct {
		name string
		req  func() *http.Request
		set  func(*fakeMsb)
	}{
		{
			name: "duplicate create",
			req: func() *http.Request {
				return authedJSON(http.MethodPost, "/sandboxes", `{"name":"probe","image":"alpine"}`)
			},
			set: func(f *fakeMsb) { f.createErr = msb.ErrSandboxAlreadyExists },
		},
		{
			name: "rm running",
			req: func() *http.Request {
				return authed(http.MethodDelete, "/sandboxes/probe")
			},
			set: func(f *fakeMsb) { f.rmErr = msb.ErrSandboxStillRunning },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeMsb{}
			tc.set(client)
			srv := New(Config{Token: testToken}, client)

			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, tc.req())

			if rec.Code != http.StatusConflict {
				t.Errorf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

// --- Volume endpoints ---

func TestPostVolumes_CreatesAndReturns201(t *testing.T) {
	client := &fakeMsb{}
	srv := New(Config{Token: testToken}, client)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authedJSON(http.MethodPost, "/volumes",
		`{"name":"myvol","size":"1G"}`))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if client.gotVolumeCreate != [2]string{"myvol", "1G"} {
		t.Errorf("VolumeCreate called with %v, want {myvol, 1G}", client.gotVolumeCreate)
	}
}

func TestPostVolumes_AcceptsYAML(t *testing.T) {
	client := &fakeMsb{}
	srv := New(Config{Token: testToken}, client)

	req := httptest.NewRequest(http.MethodPost, "/volumes",
		strings.NewReader("name: yamlvol\nsize: 2G\n"))
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/yaml")

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}
	if client.gotVolumeCreate != [2]string{"yamlvol", "2G"} {
		t.Errorf("VolumeCreate called with %v, want {yamlvol, 2G}", client.gotVolumeCreate)
	}
}

func TestPostVolumes_RequiresNameAndSize(t *testing.T) {
	cases := []string{
		`{}`,
		`{"name":"v"}`,
		`{"size":"1G"}`,
		`{"name":"v","size":""}`,
	}
	for _, body := range cases {
		client := &fakeMsb{}
		srv := New(Config{Token: testToken}, client)

		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, authedJSON(http.MethodPost, "/volumes", body))

		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %s: status = %d, want 400", body, rec.Code)
		}
		if client.gotVolumeCreate[0] != "" {
			t.Errorf("body %s: VolumeCreate invoked despite bad request", body)
		}
	}
}

func TestPostVolumes_RejectsUnknownFields(t *testing.T) {
	client := &fakeMsb{}
	srv := New(Config{Token: testToken}, client)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authedJSON(http.MethodPost, "/volumes",
		`{"name":"v","size":"1G","fnord":true}`))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestPostVolumes_AlreadyExistsReturns409(t *testing.T) {
	client := &fakeMsb{volumeCreateErr: msb.ErrVolumeAlreadyExists}
	srv := New(Config{Token: testToken}, client)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authedJSON(http.MethodPost, "/volumes",
		`{"name":"myvol","size":"1G"}`))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

func TestDeleteVolume_InvokesAndReturns204(t *testing.T) {
	client := &fakeMsb{}
	srv := New(Config{Token: testToken}, client)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authed(http.MethodDelete, "/volumes/myvol"))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if client.gotVolumeRm != "myvol" {
		t.Errorf("VolumeRm called with %q, want %q", client.gotVolumeRm, "myvol")
	}
}

func TestDeleteVolume_AdapterErrorReturns500(t *testing.T) {
	client := &fakeMsb{volumeRmErr: errors.New("boom")}
	srv := New(Config{Token: testToken}, client)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authed(http.MethodDelete, "/volumes/myvol"))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestGetVolumes_ReturnsJSONFromMsbVolumeLs(t *testing.T) {
	client := &fakeMsb{volumeListOut: []msb.Volume{
		{Name: "v1", QuotaMiB: 1024, UsedBytes: 0, CreatedAt: "2026-06-04 17:45:29"},
	}}
	srv := New(Config{Token: testToken}, client)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authed(http.MethodGet, "/volumes"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
	var got []msb.Volume
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("response not JSON list: %v; body=%s", err, rec.Body.String())
	}
	if len(got) != 1 || got[0].Name != "v1" || got[0].QuotaMiB != 1024 {
		t.Errorf("body = %+v, want one v1/1024 entry", got)
	}
}

func TestGetVolumes_EmptyReturnsJSONArray(t *testing.T) {
	client := &fakeMsb{}
	srv := New(Config{Token: testToken}, client)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authed(http.MethodGet, "/volumes"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != "[]" {
		t.Errorf("body = %q, want %q (empty array, not null)", body, "[]")
	}
}

func TestGetVolumes_AdapterErrorReturns500(t *testing.T) {
	client := &fakeMsb{volumeListErr: errors.New("boom")}
	srv := New(Config{Token: testToken}, client)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authed(http.MethodGet, "/volumes"))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

// msb itself lets you remove a volume that's mounted by a running sandbox
// (verified: msb v0.5.2 returns 0 even when the sandbox is using the volume).
// msb-manager keeps the safer invariant by consulting its VolumeLock first.
func TestDeleteVolume_ClaimedByRunningSandboxReturns409(t *testing.T) {
	client := &fakeMsb{}
	vlock := lock.New()
	_ = vlock.Acquire("inuse", "holder")
	srv := newWithLock(Config{Token: testToken}, client, vlock)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authed(http.MethodDelete, "/volumes/inuse"))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	if client.gotVolumeRm != "" {
		t.Error("VolumeRm invoked despite claim; lock should short-circuit")
	}
}

func TestDeleteVolume_NotFoundReturns404(t *testing.T) {
	client := &fakeMsb{volumeRmErr: msb.ErrVolumeNotFound}
	srv := New(Config{Token: testToken}, client)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authed(http.MethodDelete, "/volumes/nonexistent"))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

// --- Snapshot endpoints ---

func TestGetSnapshots_ReturnsJSONFromMsbSnapshotLs(t *testing.T) {
	parent := "sha256:parentdigest"
	client := &fakeMsb{snapshotListOut: []msb.Snapshot{
		{
			Name: "probe-snap", Digest: "sha256:digestx", ImageRef: "alpine",
			Format: "raw", CreatedAt: "2026-06-06 07:52:18",
			ArtifactPath: "/x/probe-snap", ParentDigest: &parent, SizeBytes: 4294967296,
		},
	}}
	srv := New(Config{Token: testToken}, client)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authed(http.MethodGet, "/snapshots"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got []msb.Snapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("response not JSON list: %v; body=%s", err, rec.Body.String())
	}
	if len(got) != 1 || got[0].Name != "probe-snap" || got[0].SizeBytes != 4294967296 {
		t.Errorf("body = %+v, want one probe-snap entry", got)
	}
}

func TestGetSnapshots_EmptyReturnsJSONArray(t *testing.T) {
	srv := New(Config{Token: testToken}, &fakeMsb{})

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authed(http.MethodGet, "/snapshots"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != "[]" {
		t.Errorf("body = %q, want %q", body, "[]")
	}
}

func TestPostSnapshots_CreatesAndReturns201(t *testing.T) {
	client := &fakeMsb{}
	srv := New(Config{Token: testToken}, client)

	body := `from: probe
name: probe-snap
labels:
  team: ops
  msb.parent: test-parent
force: true
`
	req := httptest.NewRequest(http.MethodPost, "/snapshots", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/yaml")

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	got := client.gotSnapshotCreate
	if got.From != "probe" || got.Dest != "probe-snap" || !got.Force {
		t.Errorf("Create call = %+v, want from=probe dest=probe-snap force=true", got)
	}
	if got.Labels["team"] != "ops" || got.Labels["msb.parent"] != "test-parent" {
		t.Errorf("Labels = %+v, want team+msb.parent", got.Labels)
	}
}

func TestPostSnapshots_RequiresFromAndName(t *testing.T) {
	cases := []string{
		`{}`,
		`{"from":"probe"}`,
		`{"name":"snap"}`,
	}
	for _, body := range cases {
		client := &fakeMsb{}
		srv := New(Config{Token: testToken}, client)

		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, authedJSON(http.MethodPost, "/snapshots", body))

		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %s: status = %d, want 400", body, rec.Code)
		}
		if client.gotSnapshotCreate.From != "" {
			t.Errorf("body %s: SnapshotCreate invoked despite bad request", body)
		}
	}
}

func TestPostSnapshots_AlreadyExistsReturns409(t *testing.T) {
	client := &fakeMsb{snapshotCreateErr: msb.ErrSnapshotAlreadyExists}
	srv := New(Config{Token: testToken}, client)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authedJSON(http.MethodPost, "/snapshots",
		`{"from":"probe","name":"probe-snap"}`))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

func TestDeleteSnapshot_InvokesAndReturns204(t *testing.T) {
	client := &fakeMsb{}
	srv := New(Config{Token: testToken}, client)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authed(http.MethodDelete, "/snapshots/probe-snap"))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if client.gotSnapshotRm != "probe-snap" {
		t.Errorf("SnapshotRm called with %q, want %q", client.gotSnapshotRm, "probe-snap")
	}
}

func TestDeleteSnapshot_NotFoundReturns404(t *testing.T) {
	client := &fakeMsb{snapshotRmErr: msb.ErrSnapshotNotFound}
	srv := New(Config{Token: testToken}, client)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authed(http.MethodDelete, "/snapshots/missing"))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}
