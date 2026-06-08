package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"msb-manager/internal/msb"
)

// A timed-out msb invocation (issue #4) surfaces as 504 Gateway Timeout, not a
// generic 500.
func TestStatusMapping_Timeout(t *testing.T) {
	client := &fakeMsb{inspectErr: msb.ErrTimeout}
	srv := New(Config{Token: testToken}, client)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authed(http.MethodGet, "/sandboxes/probe"))

	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504; body=%s", rec.Code, rec.Body.String())
	}
}

// Issue #3: a path segment beginning with '-' (or its percent-encoded form
// %2D%2Dforce) reaches a handler as "--force" and would be passed to msb as a
// flag. The handler must reject it with 400 *before* invoking the adapter.
func TestPathName_MalformedRejectedBeforeMsb(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		// check asserts the adapter method was NOT called.
		called func(*fakeMsb) bool
	}{
		{"inspect", http.MethodGet, "/sandboxes/--force", func(f *fakeMsb) bool { return f.gotInspectName != "" }},
		{"start", http.MethodPost, "/sandboxes/--force/start", func(f *fakeMsb) bool { return f.gotStartName != "" }},
		{"stop", http.MethodPost, "/sandboxes/--force/stop", func(f *fakeMsb) bool { return f.gotStopName != "" }},
		{"rm", http.MethodDelete, "/sandboxes/--force", func(f *fakeMsb) bool { return f.gotRmName != "" }},
		{"logs", http.MethodGet, "/sandboxes/--force/logs", func(f *fakeMsb) bool { return f.gotLogsName != "" }},
		{"metrics", http.MethodGet, "/sandboxes/--force/metrics", func(f *fakeMsb) bool { return f.gotMetricsName != "" }},
		{"volume rm", http.MethodDelete, "/volumes/--force", func(f *fakeMsb) bool { return f.gotVolumeRm != "" }},
		{"snapshot rm", http.MethodDelete, "/snapshots/--force", func(f *fakeMsb) bool { return f.gotSnapshotRm != "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeMsb{}
			srv := New(Config{Token: testToken}, client)

			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, authed(tc.method, tc.path))

			if rec.Code != http.StatusBadRequest {
				t.Errorf("%s %s: status = %d, want 400; body=%s", tc.method, tc.path, rec.Code, rec.Body.String())
			}
			if tc.called(client) {
				t.Errorf("%s %s: adapter invoked despite malformed name", tc.method, tc.path)
			}
		})
	}
}

// The percent-encoded form decodes to the same "--force" before routing; assert
// the path-decoded variant is rejected too.
func TestPathName_PercentEncodedDashRejected(t *testing.T) {
	client := &fakeMsb{}
	srv := New(Config{Token: testToken}, client)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authed(http.MethodDelete, "/sandboxes/%2D%2Dforce"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if client.gotRmName != "" {
		t.Error("Rm invoked despite percent-encoded --force")
	}
}

func TestPostVolumes_MalformedNameOrSizeReturns400(t *testing.T) {
	cases := []string{
		`{"name":"--force","size":"1G"}`,
		`{"name":"myvol","size":"-1G"}`,
		`{"name":"myvol","size":"--size"}`,
		`{"name":"bad name","size":"1G"}`,
		`{"name":"myvol","size":"1G;rm"}`,
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
			t.Errorf("body %s: VolumeCreate invoked despite malformed input", body)
		}
	}
}

func TestPostSnapshots_MalformedFromOrNameReturns400(t *testing.T) {
	cases := []string{
		`{"from":"--force","name":"snap"}`,
		`{"from":"probe","name":"--force"}`,
		`{"from":"a/b","name":"snap"}`,
		`{"from":"probe","name":"bad name"}`,
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
			t.Errorf("body %s: SnapshotCreate invoked despite malformed input", body)
		}
	}
}
