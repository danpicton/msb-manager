package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"msb-manager/internal/msb"
)

// These golden tests pin the exact response bytes of every read endpoint, so a
// future change to the DTO mapping (ADR-0006) that would reshape the public API
// fails loudly. They complement the internal/api unit tests (which prove the
// DTO JSON equals the adapter JSON) by locking the bytes a client actually
// receives end-to-end through the handler.

func assertBody(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got := strings.TrimSpace(rec.Body.String()); got != want {
		t.Errorf("wire body changed:\n  got:  %s\n  want: %s", got, want)
	}
}

func TestGolden_ListSandboxes(t *testing.T) {
	client := &fakeMsb{listOut: []msb.Sandbox{
		{Name: "jsontest", Image: "alpine", Status: "Running", CreatedAt: "2026-05-31 18:25:06"},
	}}
	srv := New(Config{Token: testToken}, client)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authed(http.MethodGet, "/sandboxes"))

	assertBody(t, rec, `[{"name":"jsontest","image":"alpine","status":"Running","created_at":"2026-05-31 18:25:06"}]`)
}

func TestGolden_InspectSandbox(t *testing.T) {
	client := &fakeMsb{inspectOut: msb.SandboxDetail{
		Name: "jsontest", Status: "Running", CreatedAt: "2026-05-31 18:25:06",
		UpdatedAt: "2026-05-31 18:30:00", Image: "alpine", CPUs: 2, MemoryMiB: 512,
		Workdir: "/workspace",
		Env:     map[string]string{"PATH": "/usr/bin"},
		Mounts: []msb.Mount{
			{Guest: "/workspace", Type: "Named", Name: "myvol"},
		},
	}}
	srv := New(Config{Token: testToken}, client)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authed(http.MethodGet, "/sandboxes/jsontest"))

	assertBody(t, rec, `{"name":"jsontest","status":"Running","created_at":"2026-05-31 18:25:06","updated_at":"2026-05-31 18:30:00","image":"alpine","cpus":2,"memory_mib":512,"workdir":"/workspace","env":{"PATH":"/usr/bin"},"mounts":[{"guest":"/workspace","type":"Named","readonly":false,"name":"myvol"}]}`)
}

func TestGolden_Metrics(t *testing.T) {
	client := &fakeMsb{metricsOut: msb.Metrics{
		Name: "probe", CPUPercent: 1.5, MemoryBytes: 80666624,
		UptimeSecs: 2.004, Timestamp: "2026-06-06T08:12:50.545+00:00",
	}}
	srv := New(Config{Token: testToken}, client)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authed(http.MethodGet, "/sandboxes/probe/metrics"))

	assertBody(t, rec, `{"name":"probe","cpu_percent":1.5,"memory_bytes":80666624,"memory_limit_bytes":0,"disk_read_bytes":0,"disk_write_bytes":0,"net_rx_bytes":0,"net_tx_bytes":0,"uptime_secs":2.004,"timestamp":"2026-06-06T08:12:50.545+00:00"}`)
}

func TestGolden_ListVolumes(t *testing.T) {
	client := &fakeMsb{volumeListOut: []msb.Volume{
		{Name: "v1", QuotaMiB: 1024, UsedBytes: 0, CreatedAt: "2026-06-04 17:45:29"},
	}}
	srv := New(Config{Token: testToken}, client)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authed(http.MethodGet, "/volumes"))

	assertBody(t, rec, `[{"name":"v1","quota_mib":1024,"used_bytes":0,"created_at":"2026-06-04 17:45:29"}]`)
}

func TestGolden_ListSnapshots(t *testing.T) {
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

	assertBody(t, rec, `[{"name":"probe-snap","digest":"sha256:digestx","image_ref":"alpine","format":"raw","created_at":"2026-06-06 07:52:18","artifact_path":"/x/probe-snap","parent_digest":"sha256:parentdigest","size_bytes":4294967296}]`)
}

// Parentless snapshots must keep parent_digest: null (not absent) on the wire.
func TestGolden_ListSnapshots_NullParent(t *testing.T) {
	client := &fakeMsb{snapshotListOut: []msb.Snapshot{
		{Name: "root", Digest: "sha256:x", ImageRef: "alpine", Format: "raw",
			CreatedAt: "2026-06-06 07:52:18", ArtifactPath: "/x/root", ParentDigest: nil, SizeBytes: 100},
	}}
	srv := New(Config{Token: testToken}, client)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authed(http.MethodGet, "/snapshots"))

	assertBody(t, rec, `[{"name":"root","digest":"sha256:x","image_ref":"alpine","format":"raw","created_at":"2026-06-06 07:52:18","artifact_path":"/x/root","parent_digest":null,"size_bytes":100}]`)
}
