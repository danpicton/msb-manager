package msb

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeRunner records the last invocation and returns canned output. It's the
// test double for the subprocess boundary — every adapter test uses it so we
// never spawn a real msb during unit tests.
type fakeRunner struct {
	stdout, stderr []byte
	err            error

	gotName string
	gotArgs []string
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, []byte, error) {
	f.gotName = name
	f.gotArgs = args
	return f.stdout, f.stderr, f.err
}

func TestClientList_InvokesMsbLsJSON(t *testing.T) {
	r := &fakeRunner{stdout: []byte("[]")}
	c := NewClient("msb", r)

	if _, err := c.List(context.Background()); err != nil {
		t.Fatalf("List: unexpected error: %v", err)
	}

	if r.gotName != "msb" {
		t.Errorf("invoked binary = %q, want %q", r.gotName, "msb")
	}
	wantArgs := []string{"ls", "--format", "json"}
	if !reflect.DeepEqual(r.gotArgs, wantArgs) {
		t.Errorf("invoked args = %v, want %v", r.gotArgs, wantArgs)
	}
}

func TestClientInspect_InvokesMsbInspectJSON(t *testing.T) {
	r := &fakeRunner{stdout: []byte("{}")}
	c := NewClient("msb", r)

	if _, err := c.Inspect(context.Background(), "jsontest"); err != nil {
		t.Fatalf("Inspect: unexpected error: %v", err)
	}

	if r.gotName != "msb" {
		t.Errorf("invoked binary = %q, want %q", r.gotName, "msb")
	}
	wantArgs := []string{"inspect", "--format", "json", "jsontest"}
	if !reflect.DeepEqual(r.gotArgs, wantArgs) {
		t.Errorf("invoked args = %v, want %v", r.gotArgs, wantArgs)
	}
}

func TestClientCreate_InvokesMsbCreate(t *testing.T) {
	r := &fakeRunner{}
	c := NewClient("msb", r)

	if err := c.Create(context.Background(), CreateOpts{Name: "voltest", Image: "alpine"}); err != nil {
		t.Fatalf("Create: unexpected error: %v", err)
	}

	wantArgs := []string{"create", "-n", "voltest", "alpine"}
	if !reflect.DeepEqual(r.gotArgs, wantArgs) {
		t.Errorf("invoked args = %v, want %v", r.gotArgs, wantArgs)
	}
}

func TestClientCreate_WithSSHKeys(t *testing.T) {
	// Record every Run invocation so we can assert both create + exec happen.
	var calls [][]string
	r := &recordingRunner{calls: &calls}
	c := NewClient("msb", r)

	keys := []string{
		"ssh-ed25519 AAAAedkey dan@laptop",
		"ssh-rsa AAAArsakey 'with quotes'",
	}
	if err := c.Create(context.Background(), CreateOpts{
		Name: "x", Image: "alpine", SSHKeys: keys,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if len(calls) != 2 {
		t.Fatalf("got %d msb invocations, want 2 (create + exec install-ssh-keys); calls=%v",
			len(calls), calls)
	}

	// 1) create … --script-raw install-ssh-keys=<body>
	createArgs := calls[0]
	scriptIdx := -1
	for i, a := range createArgs {
		if a == "--script-raw" {
			scriptIdx = i
			break
		}
	}
	if scriptIdx < 0 {
		t.Fatalf("--script-raw not in create args: %v", createArgs)
	}
	flagVal := createArgs[scriptIdx+1]
	const namePrefix = "install-ssh-keys="
	if !strings.HasPrefix(flagVal, namePrefix) {
		t.Errorf("--script-raw value = %q, want prefix %q", flagVal, namePrefix)
	}
	body := strings.TrimPrefix(flagVal, namePrefix)
	// --script-raw skips shebang insertion; we must add our own.
	if !strings.HasPrefix(body, "#!/bin/sh\n") {
		t.Errorf("script body missing #!/bin/sh shebang: %s", body)
	}
	if !strings.Contains(body, "/root/.ssh/authorized_keys") {
		t.Errorf("script body does not target authorized_keys: %s", body)
	}
	for _, k := range keys {
		quoted := "'" + strings.ReplaceAll(k, "'", `'\''`) + "'"
		if !strings.Contains(body, quoted) {
			t.Errorf("body missing quoted key %q", k)
		}
	}

	// 2) exec x -- install-ssh-keys (the -- terminates msb's flag parsing)
	wantExec := []string{"exec", "x", "--", "install-ssh-keys"}
	if !reflect.DeepEqual(calls[1], wantExec) {
		t.Errorf("second invocation = %v, want %v", calls[1], wantExec)
	}
}

// If installing the ssh keys fails, the partial-state sandbox is removed so
// Create has atomic semantics from the caller's view.
func TestClientCreate_SSHKeyInstallFailureRollsBack(t *testing.T) {
	var calls [][]string
	r := &recordingRunner{
		calls: &calls,
		// First call (create) succeeds; second (exec install) fails; third (rm) succeeds.
		errs: []error{nil, errors.New("exit status 1"), nil},
	}
	c := NewClient("msb", r)

	err := c.Create(context.Background(), CreateOpts{
		Name: "x", Image: "alpine", SSHKeys: []string{"ssh-ed25519 AAAA"},
	})
	if err == nil {
		t.Fatal("Create: got nil, want install-failure error")
	}
	if len(calls) != 3 {
		t.Fatalf("got %d calls, want 3 (create, exec, rm); calls=%v", len(calls), calls)
	}
	// Final call must be a force-rm of the sandbox we just created.
	wantRm := []string{"rm", "-f", "x"}
	if !reflect.DeepEqual(calls[2], wantRm) {
		t.Errorf("rollback call = %v, want %v", calls[2], wantRm)
	}
}

type recordingRunner struct {
	calls *[][]string
	errs  []error // optional: per-call exit errors
	idx   int
}

func (rr *recordingRunner) Run(_ context.Context, _ string, args ...string) ([]byte, []byte, error) {
	*rr.calls = append(*rr.calls, append([]string(nil), args...))
	var err error
	if rr.idx < len(rr.errs) {
		err = rr.errs[rr.idx]
	}
	rr.idx++
	return nil, nil, err
}

// Spec→msb-args translation, the high-value pure-function seam called out in
// CLAUDE.md. Args order: -n NAME, optionals (-c, -m, -v, -e, -p), IMAGE last.
// Env is sorted by key for determinism (matters for testing and reproducible
// audit logs; msb itself is order-insensitive).
func TestClientCreate_FullOpts(t *testing.T) {
	r := &fakeRunner{}
	c := NewClient("msb", r)

	opts := CreateOpts{
		Name:      "voltest",
		Image:     "alpine",
		CPUs:      2,
		MemoryMiB: 512,
		Volume:    &VolumeMount{Name: "myvol", Mount: "/workspace"},
		Env:       map[string]string{"FOO": "bar", "PATH": "/usr/bin"},
		Ports:     []PortMapping{{Host: 8080, Guest: 80}, {Host: 9090, Guest: 90}},
		Secrets: []Secret{
			{Key: "GITHUB_TOKEN", Value: "ghp_x", Host: "github.com"},
			{Key: "OPENAI_KEY", Value: "sk-y", Host: "api.openai.com"},
		},
	}
	if err := c.Create(context.Background(), opts); err != nil {
		t.Fatalf("Create: unexpected error: %v", err)
	}

	wantArgs := []string{
		"create",
		"-n", "voltest",
		"-c", "2",
		"-m", "512",
		"-v", "myvol:/workspace",
		"-e", "FOO=bar",
		"-e", "PATH=/usr/bin",
		"-p", "8080:80",
		"-p", "9090:90",
		"--secret", "GITHUB_TOKEN=ghp_x@github.com",
		"--secret", "OPENAI_KEY=sk-y@api.openai.com",
		"alpine",
	}
	if !reflect.DeepEqual(r.gotArgs, wantArgs) {
		t.Errorf("invoked args =\n  %v\nwant\n  %v", r.gotArgs, wantArgs)
	}
}

func TestClientStart_InvokesMsbStart(t *testing.T) {
	r := &fakeRunner{}
	c := NewClient("msb", r)

	if err := c.Start(context.Background(), "voltest"); err != nil {
		t.Fatalf("Start: unexpected error: %v", err)
	}
	wantArgs := []string{"start", "voltest"}
	if !reflect.DeepEqual(r.gotArgs, wantArgs) {
		t.Errorf("invoked args = %v, want %v", r.gotArgs, wantArgs)
	}
}

func TestClientStop_InvokesMsbStop(t *testing.T) {
	r := &fakeRunner{}
	c := NewClient("msb", r)

	if err := c.Stop(context.Background(), "voltest"); err != nil {
		t.Fatalf("Stop: unexpected error: %v", err)
	}
	wantArgs := []string{"stop", "voltest"}
	if !reflect.DeepEqual(r.gotArgs, wantArgs) {
		t.Errorf("invoked args = %v, want %v", r.gotArgs, wantArgs)
	}
}

func TestClientRm_InvokesMsbRm(t *testing.T) {
	r := &fakeRunner{}
	c := NewClient("msb", r)

	if err := c.Rm(context.Background(), "voltest"); err != nil {
		t.Fatalf("Rm: unexpected error: %v", err)
	}
	wantArgs := []string{"rm", "voltest"}
	if !reflect.DeepEqual(r.gotArgs, wantArgs) {
		t.Errorf("invoked args = %v, want %v", r.gotArgs, wantArgs)
	}
}

func TestClientVolumeCreate_InvokesMsbVolumeCreate(t *testing.T) {
	r := &fakeRunner{}
	c := NewClient("msb", r)

	if err := c.VolumeCreate(context.Background(), "myvol", "1G"); err != nil {
		t.Fatalf("VolumeCreate: unexpected error: %v", err)
	}
	wantArgs := []string{"volume", "create", "--size", "1G", "myvol"}
	if !reflect.DeepEqual(r.gotArgs, wantArgs) {
		t.Errorf("invoked args = %v, want %v", r.gotArgs, wantArgs)
	}
}

func TestClientVolumeList_InvokesMsbVolumeLsJSON(t *testing.T) {
	r := &fakeRunner{stdout: []byte("[]")}
	c := NewClient("msb", r)

	if _, err := c.VolumeList(context.Background()); err != nil {
		t.Fatalf("VolumeList: unexpected error: %v", err)
	}
	wantArgs := []string{"volume", "ls", "--format", "json"}
	if !reflect.DeepEqual(r.gotArgs, wantArgs) {
		t.Errorf("invoked args = %v, want %v", r.gotArgs, wantArgs)
	}
}

func TestClientSnapshotList_InvokesMsbSnapshotLsJSON(t *testing.T) {
	r := &fakeRunner{stdout: []byte("[]")}
	c := NewClient("msb", r)
	if _, err := c.SnapshotList(context.Background()); err != nil {
		t.Fatalf("SnapshotList: %v", err)
	}
	wantArgs := []string{"snapshot", "ls", "--format", "json"}
	if !reflect.DeepEqual(r.gotArgs, wantArgs) {
		t.Errorf("invoked args = %v, want %v", r.gotArgs, wantArgs)
	}
}

func TestClientSnapshotCreate_InvokesWithSortedLabels(t *testing.T) {
	r := &fakeRunner{}
	c := NewClient("msb", r)
	if err := c.SnapshotCreate(context.Background(), "probe", "probe-snap",
		map[string]string{"team": "ops", "msb.parent": "test"}, false); err != nil {
		t.Fatalf("SnapshotCreate: %v", err)
	}
	// Labels emitted in sorted key order so args are deterministic; destination
	// is the trailing positional (msb's grammar, not a flag).
	wantArgs := []string{
		"snapshot", "create",
		"--from", "probe",
		"--label", "msb.parent=test",
		"--label", "team=ops",
		"probe-snap",
	}
	if !reflect.DeepEqual(r.gotArgs, wantArgs) {
		t.Errorf("invoked args =\n  %v\nwant\n  %v", r.gotArgs, wantArgs)
	}
}

func TestClientSnapshotCreate_ForceAddsFlag(t *testing.T) {
	r := &fakeRunner{}
	c := NewClient("msb", r)
	if err := c.SnapshotCreate(context.Background(), "probe", "probe-snap", nil, true); err != nil {
		t.Fatalf("SnapshotCreate: %v", err)
	}
	wantArgs := []string{"snapshot", "create", "--from", "probe", "--force", "probe-snap"}
	if !reflect.DeepEqual(r.gotArgs, wantArgs) {
		t.Errorf("invoked args = %v, want %v", r.gotArgs, wantArgs)
	}
}

func TestClientSnapshotRm_InvokesMsbSnapshotRm(t *testing.T) {
	r := &fakeRunner{}
	c := NewClient("msb", r)
	if err := c.SnapshotRm(context.Background(), "probe-snap"); err != nil {
		t.Fatalf("SnapshotRm: %v", err)
	}
	wantArgs := []string{"snapshot", "rm", "probe-snap"}
	if !reflect.DeepEqual(r.gotArgs, wantArgs) {
		t.Errorf("invoked args = %v, want %v", r.gotArgs, wantArgs)
	}
}

func TestClientLogs_NoOptsJustNameAndJSON(t *testing.T) {
	r := &fakeRunner{stdout: []byte(`{"line":"x"}` + "\n")}
	c := NewClient("msb", r)

	body, err := c.Logs(context.Background(), "probe", LogsOpts{})
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	if string(body) != `{"line":"x"}`+"\n" {
		t.Errorf("returned body = %q, want pass-through", body)
	}

	wantArgs := []string{"logs", "probe", "--json"}
	if !reflect.DeepEqual(r.gotArgs, wantArgs) {
		t.Errorf("invoked args = %v, want %v", r.gotArgs, wantArgs)
	}
}

func TestClientLogs_FullOpts(t *testing.T) {
	r := &fakeRunner{}
	c := NewClient("msb", r)

	if _, err := c.Logs(context.Background(), "probe", LogsOpts{
		Tail:   200,
		Since:  "5m",
		Until:  "2026-06-06T08:00:00Z",
		Source: "stdout,stderr",
		Grep:   "ERROR",
	}); err != nil {
		t.Fatalf("Logs: %v", err)
	}
	wantArgs := []string{
		"logs", "probe",
		"--tail", "200",
		"--since", "5m",
		"--until", "2026-06-06T08:00:00Z",
		"--source", "stdout,stderr",
		"--grep", "ERROR",
		"--json",
	}
	if !reflect.DeepEqual(r.gotArgs, wantArgs) {
		t.Errorf("invoked args =\n  %v\nwant\n  %v", r.gotArgs, wantArgs)
	}
}

func TestClientMetrics_InvokesMsbMetricsJSON(t *testing.T) {
	r := &fakeRunner{stdout: []byte(`{"name":"probe","cpu_percent":1.5}`)}
	c := NewClient("msb", r)

	got, err := c.Metrics(context.Background(), "probe")
	if err != nil {
		t.Fatalf("Metrics: %v", err)
	}
	if got.Name != "probe" || got.CPUPercent != 1.5 {
		t.Errorf("got = %+v, want name=probe cpu=1.5", got)
	}
	wantArgs := []string{"metrics", "probe", "--format", "json"}
	if !reflect.DeepEqual(r.gotArgs, wantArgs) {
		t.Errorf("invoked args = %v, want %v", r.gotArgs, wantArgs)
	}
}

func TestClientVolumeRm_InvokesMsbVolumeRm(t *testing.T) {
	r := &fakeRunner{}
	c := NewClient("msb", r)

	if err := c.VolumeRm(context.Background(), "myvol"); err != nil {
		t.Fatalf("VolumeRm: unexpected error: %v", err)
	}
	wantArgs := []string{"volume", "rm", "myvol"}
	if !reflect.DeepEqual(r.gotArgs, wantArgs) {
		t.Errorf("invoked args = %v, want %v", r.gotArgs, wantArgs)
	}
}

func TestClientList_HonoursCustomBinaryPath(t *testing.T) {
	r := &fakeRunner{stdout: []byte("[]")}
	c := NewClient("/opt/microsandbox/bin/msb", r)

	if _, err := c.List(context.Background()); err != nil {
		t.Fatalf("List: unexpected error: %v", err)
	}

	if r.gotName != "/opt/microsandbox/bin/msb" {
		t.Errorf("invoked binary = %q, want override", r.gotName)
	}
}

// Mutating commands must not overlap: msb v0.5.2 is not concurrent-safe
// (CONTEXT verification #3). Read calls are unaffected — they don't take the
// mutex — but Create/Start/Stop/Rm are serialised. This test fires N
// concurrent goroutines through Create and asserts the runner never sees
// more than one in flight at once.
func TestClient_MutatingCommandsSerialise(t *testing.T) {
	var (
		inflight atomic.Int32
		maxSeen  atomic.Int32
	)
	r := &concurrencyRunner{
		onCall: func() {
			cur := inflight.Add(1)
			for {
				old := maxSeen.Load()
				if cur <= old || maxSeen.CompareAndSwap(old, cur) {
					break
				}
			}
			// Hold the call open long enough that an unserialised second call
			// would race in.
			time.Sleep(15 * time.Millisecond)
			inflight.Add(-1)
		},
	}
	c := NewClient("msb", r)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = c.Create(context.Background(), CreateOpts{Name: "x", Image: "alpine"})
		}()
	}
	wg.Wait()

	if got := maxSeen.Load(); got > 1 {
		t.Errorf("max concurrent Create invocations = %d, want 1", got)
	}
}

type concurrencyRunner struct {
	onCall func()
}

func (cr *concurrencyRunner) Run(_ context.Context, _ string, _ ...string) ([]byte, []byte, error) {
	cr.onCall()
	return nil, nil, nil
}

// When the runner returns a non-zero exit, Client methods classify the stderr
// and wrap the recognised sentinel. The HTTP layer errors.Is()-es to pick a
// status. Inspect is the easiest to exercise — its real-world failure mode is
// "not found" on a typo.
func TestClientInspect_NotFound_WrapsSentinel(t *testing.T) {
	r := &fakeRunner{
		stderr: []byte("error: sandbox not found: nope\n"),
		err:    errors.New("exit status 1"),
	}
	c := NewClient("msb", r)

	_, err := c.Inspect(context.Background(), "nope")
	if !errors.Is(err, ErrSandboxNotFound) {
		t.Fatalf("Inspect on missing: got %v, want wrap of ErrSandboxNotFound", err)
	}
}

func TestClientCreate_AlreadyExists_WrapsSentinel(t *testing.T) {
	r := &fakeRunner{
		stderr: []byte("error: sandbox already exists: sandbox 'probe' already exists\n"),
		err:    errors.New("exit status 1"),
	}
	c := NewClient("msb", r)

	err := c.Create(context.Background(), CreateOpts{Name: "probe", Image: "alpine"})
	if !errors.Is(err, ErrSandboxAlreadyExists) {
		t.Fatalf("Create on duplicate: got %v, want wrap of ErrSandboxAlreadyExists", err)
	}
}

func TestClientRm_StillRunning_WrapsSentinel(t *testing.T) {
	r := &fakeRunner{
		stderr: []byte("error: sandbox still running: cannot remove sandbox 'probe': still running\n"),
		err:    errors.New("exit status 1"),
	}
	c := NewClient("msb", r)

	err := c.Rm(context.Background(), "probe")
	if !errors.Is(err, ErrSandboxStillRunning) {
		t.Fatalf("Rm of running: got %v, want wrap of ErrSandboxStillRunning", err)
	}
}

// Unrecognised stderr surfaces the raw exit error untouched — the HTTP layer
// keeps mapping that to 500.
func TestClientStop_UnknownError_Untouched(t *testing.T) {
	rawErr := errors.New("exit status 137")
	r := &fakeRunner{
		stderr: []byte("error: kernel said no\n"),
		err:    rawErr,
	}
	c := NewClient("msb", r)

	err := c.Stop(context.Background(), "anything")
	if err == nil {
		t.Fatal("Stop: got nil, want error")
	}
	if errors.Is(err, ErrSandboxNotFound) || errors.Is(err, ErrSandboxAlreadyExists) || errors.Is(err, ErrSandboxStillRunning) {
		t.Errorf("unrecognised stderr was classified: %v", err)
	}
}
