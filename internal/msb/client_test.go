package msb

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"slices"
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

func (f *fakeRunner) Run(_ context.Context, _ []string, name string, args ...string) ([]byte, []byte, error) {
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
	wantArgs := []string{"inspect", "--format", "json", "--", "jsontest"}
	if !reflect.DeepEqual(r.gotArgs, wantArgs) {
		t.Errorf("invoked args = %v, want %v", r.gotArgs, wantArgs)
	}
}

func TestClientInspect_RecognizesRestoredManagedDisk(t *testing.T) {
	var calls [][]string
	r := &recordingRunner{calls: &calls, outputs: [][]byte{
		[]byte(`{
			"name":"derived","status":"Running",
			"config":{"mounts":[
				{"type":"DiskImage","host":"/custom/volumes/data/disk.raw","guest":"/workspace"},
				{"type":"DiskImage","host":"/other/data/disk.raw","guest":"/external"}
			]},
			"active_config":{"mounts":[{"type":"DiskImage","host":"/custom/volumes/data/disk.raw","guest":"/workspace"}]}
		}`),
		[]byte(`{"volumes":{"items":[{"name":"data","path":"/custom/volumes/data"}]}}`),
	}}
	detail, err := NewClient("msb", r).Inspect(context.Background(), "derived")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(detail.VolumeNames(), []string{"data"}) || !slices.Equal(detail.ActiveVolumeNames(), []string{"data"}) {
		t.Errorf("volume claims = %v / %v, want just [data]", detail.VolumeNames(), detail.ActiveVolumeNames())
	}
	if detail.Mounts[1].Type != "DiskImage" {
		t.Errorf("external disk was treated as a managed volume: %+v", detail.Mounts[1])
	}
}

func TestClientCreate_InvokesMsbCreate(t *testing.T) {
	r := &fakeRunner{}
	c := NewClient("msb", r)

	if err := c.Create(context.Background(), CreateOpts{Name: "voltest", Image: "alpine"}); err != nil {
		t.Fatalf("Create: unexpected error: %v", err)
	}

	wantArgs := []string{"create", "-n", "voltest", "--", "alpine"}
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

	wantCreate := []string{"create", "-n", "x", "--", "alpine"}
	if !reflect.DeepEqual(calls[0], wantCreate) {
		t.Errorf("first invocation = %v, want %v", calls[0], wantCreate)
	}
	wantExec := []string{"exec", "--no-stdin", "x", "--", "/bin/sh", "-c"}
	if len(calls[1]) != len(wantExec)+1 || !slices.Equal(calls[1][:len(wantExec)], wantExec) {
		t.Fatalf("second invocation = %v, want %v followed by the SSH script", calls[1], wantExec)
	}
	body := calls[1][len(wantExec)]
	if !strings.Contains(body, "/root/.ssh/authorized_keys") {
		t.Errorf("script body does not target authorized_keys: %s", body)
	}
	for _, key := range keys {
		quoted := "'" + strings.ReplaceAll(key, "'", `'\''`) + "'"
		if !strings.Contains(body, quoted) {
			t.Errorf("body missing quoted key %q", key)
		}
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
	wantRm := []string{"rm", "-f", "--", "x"}
	if !reflect.DeepEqual(calls[2], wantRm) {
		t.Errorf("rollback call = %v, want %v", calls[2], wantRm)
	}
}

// The SSH-key rollback must run even when the caller's context is already
// cancelled (client disconnected mid-exec), or the "atomic create" promise
// breaks and a sandbox is orphaned (review #3). This runner records, per call,
// the ctx error it observed, so we can prove the rollback ran with a detached
// (non-cancelled) context.
func TestClientCreate_RollbackDetachesFromCancelledContext(t *testing.T) {
	r := &ctxRecordingRunner{
		// create succeeds; exec install fails; rm rollback succeeds.
		errs: []error{nil, errors.New("exit status 1"), nil},
	}
	c := NewClient("msb", r)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // caller already gone before Create returns

	err := c.Create(ctx, CreateOpts{Name: "x", Image: "alpine", SSHKeys: []string{"ssh-ed25519 AAAA"}})
	if err == nil {
		t.Fatal("Create: got nil, want install-failure error")
	}
	if len(r.calls) != 3 {
		t.Fatalf("got %d calls, want 3 (create, exec, rm); calls=%v", len(r.calls), r.calls)
	}
	if want := []string{"rm", "-f", "--", "x"}; !reflect.DeepEqual(r.calls[2], want) {
		t.Errorf("rollback call = %v, want %v", r.calls[2], want)
	}
	// The rollback (3rd call) must see a live context, not the cancelled parent.
	if r.ctxErrs[2] != nil {
		t.Errorf("rollback ran with ctx.Err()=%v, want nil (detached from cancelled parent)", r.ctxErrs[2])
	}
}

// ctxRecordingRunner records each call's args and the ctx error observed when
// it was invoked.
type ctxRecordingRunner struct {
	calls   [][]string
	ctxErrs []error
	errs    []error
	idx     int
}

func (r *ctxRecordingRunner) Run(ctx context.Context, _ []string, _ string, args ...string) ([]byte, []byte, error) {
	r.calls = append(r.calls, append([]string(nil), args...))
	r.ctxErrs = append(r.ctxErrs, ctx.Err())
	var err error
	if r.idx < len(r.errs) {
		err = r.errs[r.idx]
	}
	r.idx++
	return nil, nil, err
}

type recordingRunner struct {
	calls   *[][]string
	errs    []error // optional: per-call exit errors
	outputs [][]byte
	idx     int
}

func (rr *recordingRunner) Run(_ context.Context, _ []string, _ string, args ...string) ([]byte, []byte, error) {
	*rr.calls = append(*rr.calls, append([]string(nil), args...))
	var err error
	if rr.idx < len(rr.errs) {
		err = rr.errs[rr.idx]
	}
	var stdout []byte
	if rr.idx < len(rr.outputs) {
		stdout = rr.outputs[rr.idx]
	}
	rr.idx++
	return stdout, nil, err
}

// Spec→msb-args translation, the high-value pure-function seam called out in
// CLAUDE.md. Args order: -n NAME, optionals (-c, -m, -v, -e, -p), IMAGE last.
// Env is sorted by key for determinism (matters for testing and reproducible
// audit logs; msb itself is order-insensitive).
func TestClientCreate_FromSnapshotRestoresAndSetsEnvironment(t *testing.T) {
	var calls [][]string
	r := &recordingRunner{
		calls: &calls,
		outputs: [][]byte{
			[]byte(`[{"name":"probe-snap","scope":"disk","reference":"/var/lib/msb/snapshots/probe-snap/snap_example"}]`),
			[]byte(`[{"name":"myvol","kind":"disk","capacity_bytes":1073741824}]`),
			[]byte(`{"volumes":{"items":[{"name":"myvol","path":"/var/lib/msb/volumes/myvol"}]}}`),
		},
	}
	c := NewClient("msb", r)
	err := c.Create(context.Background(), CreateOpts{
		Name: "derived", Snapshot: "probe-snap",
		CPUs: 2, MemoryMiB: 512,
		Volume: &VolumeMount{Name: "myvol", Mount: "/workspace"},
		Env:    map[string]string{"FOO": "bar"},
		Ports:  []PortMapping{{Host: 8080, Guest: 80}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	want := [][]string{
		{"snapshot", "ls", "--format", "json"},
		{"volume", "ls", "--format", "json"},
		{"df", "--format", "json"},
		{"restore", "--allow-missing-resources", "-n", "derived",
			"-c", "2", "-m", "512", "--mount-disk", "/var/lib/msb/volumes/myvol/disk.raw:/workspace:format=raw,fstype=ext4",
			"-p", "8080:80", "--", "/var/lib/msb/snapshots/probe-snap/snap_example"},
		{"modify", "-e", "FOO=bar", "--", "derived"},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("invocations = %v, want %v", calls, want)
	}
}

func TestClientCreate_UsesRestoreModeForSnapshotScope(t *testing.T) {
	for _, tc := range []struct {
		name      string
		scope     string
		stderr    string
		want      []bool
		wantError string
	}{
		{name: "checkpoint cold boot", scope: "full", want: []bool{true}},
		{name: "disk snapshot with qualified selector", stderr: diskSnapshotRestoreRejection, want: []bool{true, false}},
		{name: "other restore error", stderr: "error: failed to start\n  → disk image is locked", want: []bool{true}, wantError: "disk image is locked"},
		{name: "full checkpoint rejection stays cold", scope: "full", stderr: diskSnapshotRestoreRejection,
			want: []bool{true}, wantError: "disk_only requires a full snapshot"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var diskOnly []bool
			selector := "web:baseline"
			wantReference := selector
			if tc.scope != "" {
				selector, wantReference = "baseline", "/snapshots/web/snap_example"
			}
			c := NewClient("msb", runnerFunc(func(_ context.Context, _ []string, _ string, args ...string) ([]byte, []byte, error) {
				if slices.Equal(args, []string{"snapshot", "ls", "--format", "json"}) {
					listing := `[{"name":"baseline","scope":"` + tc.scope + `","reference":"` + wantReference + `"}]`
					return []byte(listing), nil, nil
				}
				if args[0] != "restore" || args[len(args)-1] != wantReference {
					t.Fatalf("unexpected command %v", args)
				}
				diskOnly = append(diskOnly, slices.Contains(args, "--disk-only"))
				if len(diskOnly) == 1 && tc.stderr != "" {
					return nil, []byte(tc.stderr + "\n"), exitCodeError{1}
				}
				return nil, nil, nil
			}))
			err := c.Create(t.Context(), CreateOpts{Name: "derived", Snapshot: selector})
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Errorf("Create: got %v, want error containing %q", err, tc.wantError)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(diskOnly, tc.want) {
				t.Errorf("--disk-only on each call = %v, want %v", diskOnly, tc.want)
			}
		})
	}
}

type runnerFunc func(context.Context, []string, string, ...string) ([]byte, []byte, error)

func (run runnerFunc) Run(ctx context.Context, env []string, name string, args ...string) ([]byte, []byte, error) {
	return run(ctx, env, name, args...)
}

func TestClientCreate_FullOpts(t *testing.T) {
	var configPath string
	c := NewClient("msb", runnerFunc(func(_ context.Context, env []string, _ string, args ...string) ([]byte, []byte, error) {
		if slices.Equal(args, []string{"volume", "ls", "--format", "json"}) {
			return []byte(`[{"name":"myvol","kind":"disk","capacity_bytes":1073741824}]`), nil, nil
		}
		i := slices.Index(args, "--conf")
		if i < 0 || i+1 >= len(args) {
			t.Fatalf("missing secret config in %v", args)
		}
		configPath = args[i+1]
		wantArgs := []string{
			"create", "-n", "voltest", "-e", "FOO=bar", "-e", "PATH=/usr/bin",
			"--conf", configPath, "-c", "2", "-m", "512", "--mount-named", "myvol:/workspace:kind=disk,size=1024",
			"-p", "8080:80", "-p", "9090:90", "--", "alpine",
		}
		if !slices.Equal(args, wantArgs) {
			t.Errorf("invoked args = %v, want %v", args, wantArgs)
		}
		info, err := os.Stat(configPath)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Errorf("secret config mode = %o, want 0600", info.Mode().Perm())
		}
		data, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatal(err)
		}
		var config struct {
			Secrets map[string]struct {
				Value string
				Allow []string
			}
		}
		if err := json.Unmarshal(data, &config); err != nil {
			t.Fatal(err)
		}
		token := config.Secrets["GITHUB_TOKEN"]
		if token.Value != "ghp_@${MSB_MANAGER_LITERAL_DOLLAR}{PATH}" ||
			!slices.Equal(token.Allow, []string{"github.com"}) {
			t.Errorf("unexpected secret config entry: %+v", token)
		}
		wantEnv := []string{"MSB_MANAGER_LITERAL_DOLLAR=$"}
		if !slices.Equal(env, wantEnv) {
			t.Errorf("unexpected child environment: %v", env)
		}
		return nil, nil, nil
	}))
	opts := CreateOpts{
		Name: "voltest", Image: "alpine", CPUs: 2, MemoryMiB: 512,
		Volume:  &VolumeMount{Name: "myvol", Mount: "/workspace"},
		Env:     map[string]string{"FOO": "bar", "PATH": "/usr/bin"},
		Ports:   []PortMapping{{Host: 8080, Guest: 80}, {Host: 9090, Guest: 90}},
		Secrets: []Secret{{Key: "GITHUB_TOKEN", Value: "ghp_@${PATH}", Host: "github.com"}},
	}
	if err := c.Create(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(configPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("secret config still exists after create: %v", err)
	}
}

func TestClientStart_InvokesMsbStart(t *testing.T) {
	r := &fakeRunner{}
	c := NewClient("msb", r)

	if err := c.Start(context.Background(), "voltest"); err != nil {
		t.Fatalf("Start: unexpected error: %v", err)
	}
	wantArgs := []string{"start", "--", "voltest"}
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
	wantArgs := []string{"stop", "--", "voltest"}
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
	wantArgs := []string{"rm", "--", "voltest"}
	if !reflect.DeepEqual(r.gotArgs, wantArgs) {
		t.Errorf("invoked args = %v, want %v", r.gotArgs, wantArgs)
	}
}

func TestClientVolumeCreate_InvokesMsbVolumeCreate(t *testing.T) {
	r := &fakeRunner{}
	c := NewClient("msb", r)

	if err := c.VolumeCreate(context.Background(), "myvol", "1GiB"); err != nil {
		t.Fatalf("VolumeCreate: unexpected error: %v", err)
	}
	wantArgs := []string{"volume", "create", "--kind", "disk", "--size", "1024", "--", "myvol"}
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
	var calls [][]string
	r := &recordingRunner{
		calls:   &calls,
		outputs: [][]byte{[]byte(`[]`), []byte(`{"status":"Stopped"}`)},
	}
	c := NewClient("msb", r)
	if err := c.SnapshotCreate(context.Background(), "probe", "probe-snap",
		map[string]string{"team": "ops", "msb.parent": "test"}, false); err != nil {
		t.Fatalf("SnapshotCreate: %v", err)
	}
	// Labels emitted in sorted key order so args are deterministic; destination
	// is the trailing positional (msb's grammar, not a flag).
	wantArgs := []string{
		"snapshot", "create",
		"--sandbox", "probe", "--group", "probe-snap",
		"--label", "msb.parent=test",
		"--label", "team=ops",
		"--", "probe-snap",
	}
	if len(calls) != 3 || !slices.Equal(calls[2], wantArgs) {
		t.Errorf("invocations = %v, want list, inspect, and %v", calls, wantArgs)
	}
}

func TestClientSnapshotCreate_ForceCannotOverwriteInstalledSnapshot(t *testing.T) {
	r := &fakeRunner{stdout: []byte(`[{"name":"probe-snap","reference":"snap-example"}]`)}
	c := NewClient("msb", r)
	err := c.SnapshotCreate(context.Background(), "probe", "probe-snap", nil, true)
	if !errors.Is(err, ErrSnapshotAlreadyExists) {
		t.Fatalf("SnapshotCreate: got %v, want ErrSnapshotAlreadyExists", err)
	}
	if want := []string{"snapshot", "ls", "--format", "json"}; !slices.Equal(r.gotArgs, want) {
		t.Errorf("last invocation = %v, want %v", r.gotArgs, want)
	}
}

func TestClientSnapshotRm_InvokesMsbSnapshotRm(t *testing.T) {
	r := &fakeRunner{stdout: []byte(`[]`)}
	c := NewClient("msb", r)
	if err := c.SnapshotRm(context.Background(), "probe-snap"); err != nil {
		t.Fatalf("SnapshotRm: %v", err)
	}
	wantArgs := []string{"snapshot", "rm", "--", "probe-snap"}
	if !reflect.DeepEqual(r.gotArgs, wantArgs) {
		t.Errorf("invoked args = %v, want %v", r.gotArgs, wantArgs)
	}
}

func TestClientSnapshotRm_AmbiguousShortNameRequiresQualifiedReference(t *testing.T) {
	r := &fakeRunner{stdout: []byte(`[{"name":"base","group":"one"},{"name":"base","group":"two"}]`)}
	c := NewClient("msb", r)
	if err := c.SnapshotRm(context.Background(), "base"); !errors.Is(err, ErrSnapshotAmbiguous) {
		t.Fatalf("unqualified remove error = %v, want ErrSnapshotAmbiguous", err)
	}
	if !slices.Equal(r.gotArgs, []string{"snapshot", "ls", "--format", "json"}) {
		t.Fatalf("unqualified delete reached msb: %v", r.gotArgs)
	}
	if err := c.SnapshotRm(context.Background(), "two:base"); err != nil {
		t.Fatal(err)
	}
	if want := []string{"snapshot", "rm", "--", "two:base"}; !slices.Equal(r.gotArgs, want) {
		t.Errorf("invoked args = %v, want %v", r.gotArgs, want)
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

	wantArgs := []string{"logs", "--json", "--", "probe"}
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
		"logs",
		"--tail", "200",
		"--since", "5m",
		"--until", "2026-06-06T08:00:00Z",
		"--source", "stdout,stderr",
		"--grep", "ERROR",
		"--json",
		"--", "probe",
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
	wantArgs := []string{"metrics", "--format", "json", "--", "probe"}
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
	wantArgs := []string{"volume", "rm", "--", "myvol"}
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

func (cr *concurrencyRunner) Run(_ context.Context, _ []string, _ string, _ ...string) ([]byte, []byte, error) {
	cr.onCall()
	return nil, nil, nil
}

// A failed create whose stderr echoes the offending --secret argument must not
// leak the secret value into the returned (and therefore logged) error (issue
// #7). Redaction must not break sentinel classification — errors.Is still works.
// Mirrors TestParseInspect_DoesNotLeakSecretValue on the create/log side.
func TestClientCreate_DoesNotLeakSecretInError(t *testing.T) {
	const secretVal = "ghp_SUPER_SECRET_do_not_log"
	r := &fakeRunner{
		stderr: []byte("error: sandbox already exists: rejected --secret GITHUB_TOKEN=" +
			secretVal + "@github.com\n"),
		err: errors.New("exit status 1"),
	}
	c := NewClient("msb", r)

	err := c.Create(context.Background(), CreateOpts{
		Name: "x", Image: "alpine",
		Secrets: []Secret{{Key: "GITHUB_TOKEN", Value: secretVal, Host: "github.com"}},
	})
	if err == nil {
		t.Fatal("Create: got nil, want error")
	}
	if strings.Contains(err.Error(), secretVal) {
		t.Fatalf("secret value leaked through create error: %v", err)
	}
	// The sentinel must still be reachable for HTTP status mapping.
	if !errors.Is(err, ErrSandboxAlreadyExists) {
		t.Errorf("redaction broke sentinel classification: %v", err)
	}
}

// blockingRunner simulates a hung msb: it blocks until ctx is cancelled (the
// timeout firing, then exec killing the child) and then returns ctx.Err(),
// mirroring how exec.CommandContext surfaces a killed process.
type blockingRunner struct {
	started chan struct{} // closed-ish signal: receives once per call entry
}

func (b *blockingRunner) Run(ctx context.Context, _ []string, _ string, _ ...string) ([]byte, []byte, error) {
	if b.started != nil {
		select {
		case b.started <- struct{}{}:
		default:
		}
	}
	<-ctx.Done()
	return nil, nil, ctx.Err()
}

// A wedged msb must fail the one request with a timeout error rather than hang
// forever (issue #4). The per-invocation context.WithTimeout cancels the call.
func TestClient_TimeoutReturnsErrTimeout(t *testing.T) {
	r := &blockingRunner{}
	c := NewClientWithTimeout("msb", r, 20*time.Millisecond)

	err := c.Create(context.Background(), CreateOpts{Name: "x", Image: "alpine"})
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("Create on hung msb: got %v, want wrap of ErrTimeout", err)
	}
}

// The mutex the mutating commands take must be released after a timeout, or one
// wedged call would deadlock the whole control plane — the exact failure the
// timeout exists to prevent (issue #4).
func TestClient_TimeoutFreesMutex(t *testing.T) {
	r := &blockingRunner{started: make(chan struct{}, 2)}
	c := NewClientWithTimeout("msb", r, 20*time.Millisecond)

	// First mutating call times out (and must release c.mu on the way out).
	if err := c.Create(context.Background(), CreateOpts{Name: "a", Image: "alpine"}); !errors.Is(err, ErrTimeout) {
		t.Fatalf("first Create: got %v, want ErrTimeout", err)
	}

	// Second mutating call: if the mutex were still held this blocks forever.
	done := make(chan error, 1)
	go func() {
		done <- c.Stop(context.Background(), "b")
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrTimeout) {
			t.Fatalf("second call: got %v, want ErrTimeout", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second mutating call blocked — mutex not released after timeout")
	}
}

// A timeout must be distinguishable from an ordinary non-zero exit: ErrTimeout,
// not a classified sentinel and not a bare exit error (the HTTP layer maps it
// to 504, others to their own statuses / 500).
func TestClient_ReadTimeout_AlsoBounded(t *testing.T) {
	r := &blockingRunner{}
	c := NewClientWithTimeout("msb", r, 20*time.Millisecond)

	_, err := c.List(context.Background())
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("List on hung msb: got %v, want ErrTimeout", err)
	}
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
