package msb

import (
	"context"
	"reflect"
	"testing"
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
