package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// recorder is a stub msb-manager: it records the last request and replies with
// a canned status and body. Command tests assert the client issued the right
// method/path/body without a real server or msb.
type recorder struct {
	method, path, rawQuery string
	body, contentType      string
	authHeader             string

	status   int
	respBody string
}

func (rec *recorder) server() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		rec.method = r.Method
		rec.path = r.URL.Path
		rec.rawQuery = r.URL.RawQuery
		rec.body = string(b)
		rec.contentType = r.Header.Get("Content-Type")
		rec.authHeader = r.Header.Get("Authorization")
		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, rec.respBody)
	}))
}

// runWith drives the real run() entry point against srvURL with a test token.
func runWith(t *testing.T, srvURL string, stdin io.Reader, args ...string) (string, string, int) {
	t.Helper()
	if stdin == nil {
		stdin = strings.NewReader("")
	}
	var out, errb bytes.Buffer
	full := append([]string{"--server", srvURL, "--token", "test-token"}, args...)
	code := run(full, stdin, &out, &errb, noEnv)
	return out.String(), errb.String(), code
}

func TestCmd_List_GetsSandboxesAndTabulates(t *testing.T) {
	rec := &recorder{respBody: `[{"name":"web","status":"Running","image":"alpine","created_at":"2026-06-01"}]`}
	srv := rec.server()
	defer srv.Close()

	out, _, code := runWith(t, srv.URL, nil, "ls")
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if rec.method != http.MethodGet || rec.path != "/sandboxes" {
		t.Errorf("request = %s %s, want GET /sandboxes", rec.method, rec.path)
	}
	if rec.authHeader != "Bearer test-token" {
		t.Errorf("auth = %q, want Bearer test-token", rec.authHeader)
	}
	if !strings.Contains(out, "NAME") || !strings.Contains(out, "web") {
		t.Errorf("table output missing header/row:\n%s", out)
	}
}

func TestCmd_List_OutputJSONPassthrough(t *testing.T) {
	rec := &recorder{respBody: `[{"name":"web"}]`}
	srv := rec.server()
	defer srv.Close()

	out, _, code := runWith(t, srv.URL, nil, "ls", "-o", "json")
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if strings.TrimSpace(out) != `[{"name":"web"}]` {
		t.Errorf("json output = %q, want verbatim server body", out)
	}
}

func TestCmd_List_OutputYAML(t *testing.T) {
	rec := &recorder{respBody: `[{"name":"web","status":"Running"}]`}
	srv := rec.server()
	defer srv.Close()

	out, _, code := runWith(t, srv.URL, nil, "ls", "-o", "yaml")
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(out, "name: web") {
		t.Errorf("yaml output missing name: web:\n%s", out)
	}
}

func TestCmd_Inspect_GetsByName(t *testing.T) {
	rec := &recorder{respBody: `{"name":"web","status":"Running"}`}
	srv := rec.server()
	defer srv.Close()

	_, _, code := runWith(t, srv.URL, nil, "inspect", "web")
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if rec.method != http.MethodGet || rec.path != "/sandboxes/web" {
		t.Errorf("request = %s %s, want GET /sandboxes/web", rec.method, rec.path)
	}
}

func TestCmd_Inspect_RequiresName(t *testing.T) {
	rec := &recorder{}
	srv := rec.server()
	defer srv.Close()

	_, errOut, code := runWith(t, srv.URL, nil, "inspect")
	if code == exitOK {
		t.Fatal("inspect with no name should fail")
	}
	if rec.method != "" {
		t.Error("no request should be issued when the name is missing")
	}
	if !strings.Contains(errOut, "sandbox name") {
		t.Errorf("stderr = %q, want a missing-name message", errOut)
	}
}

func TestCmd_Start_PostsStart(t *testing.T) {
	rec := &recorder{status: http.StatusNoContent}
	srv := rec.server()
	defer srv.Close()

	out, _, code := runWith(t, srv.URL, nil, "start", "web")
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if rec.method != http.MethodPost || rec.path != "/sandboxes/web/start" {
		t.Errorf("request = %s %s, want POST /sandboxes/web/start", rec.method, rec.path)
	}
	if !strings.Contains(out, "started web") {
		t.Errorf("stdout = %q, want confirmation", out)
	}
}

func TestCmd_Stop_PostsStop(t *testing.T) {
	rec := &recorder{status: http.StatusNoContent}
	srv := rec.server()
	defer srv.Close()

	_, _, code := runWith(t, srv.URL, nil, "stop", "web")
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if rec.method != http.MethodPost || rec.path != "/sandboxes/web/stop" {
		t.Errorf("request = %s %s, want POST /sandboxes/web/stop", rec.method, rec.path)
	}
}

func TestCmd_Rm_DeletesSandbox(t *testing.T) {
	rec := &recorder{status: http.StatusNoContent}
	srv := rec.server()
	defer srv.Close()

	_, _, code := runWith(t, srv.URL, nil, "rm", "web")
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if rec.method != http.MethodDelete || rec.path != "/sandboxes/web" {
		t.Errorf("request = %s %s, want DELETE /sandboxes/web", rec.method, rec.path)
	}
}

func TestCmd_Logs_PassesQueryAndStreamsBody(t *testing.T) {
	rec := &recorder{respBody: `{"line":"hello"}` + "\n"}
	srv := rec.server()
	defer srv.Close()

	out, _, code := runWith(t, srv.URL, nil, "logs", "web", "--tail", "50", "--since", "5m", "--source", "stdout")
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if rec.path != "/sandboxes/web/logs" {
		t.Errorf("path = %s, want /sandboxes/web/logs", rec.path)
	}
	for _, want := range []string{"tail=50", "since=5m", "source=stdout"} {
		if !strings.Contains(rec.rawQuery, want) {
			t.Errorf("query %q missing %q", rec.rawQuery, want)
		}
	}
	if out != `{"line":"hello"}`+"\n" {
		t.Errorf("logs body not passed through verbatim: %q", out)
	}
}

func TestCmd_Metrics_GetsMetrics(t *testing.T) {
	rec := &recorder{respBody: `{"name":"web","cpu_percent":1.5}`}
	srv := rec.server()
	defer srv.Close()

	_, _, code := runWith(t, srv.URL, nil, "metrics", "web")
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if rec.path != "/sandboxes/web/metrics" {
		t.Errorf("path = %s, want /sandboxes/web/metrics", rec.path)
	}
}

func TestCmd_Create_InterpolatesAndPostsSpec(t *testing.T) {
	rec := &recorder{status: http.StatusCreated, respBody: `{"name":"demo","image":"alpine"}`}
	srv := rec.server()
	defer srv.Close()

	spec := "name: demo\nimage: alpine\nsecrets:\n  - key: TOKEN\n    value: ${GH_TOKEN}\n    host: github.com\n"
	out, _, code := runWith(t, srv.URL, strings.NewReader(spec),
		"create", "-f", "-", "--set", "GH_TOKEN=ghp_secretvalue")
	if code != exitOK {
		t.Fatalf("exit = %d, want 0; stdin spec maybe rejected", code)
	}
	if rec.method != http.MethodPost || rec.path != "/sandboxes" {
		t.Errorf("request = %s %s, want POST /sandboxes", rec.method, rec.path)
	}
	if rec.contentType != "application/yaml" {
		t.Errorf("content-type = %q, want application/yaml", rec.contentType)
	}
	if strings.Contains(rec.body, "${GH_TOKEN}") {
		t.Errorf("placeholder not substituted; body still has ${GH_TOKEN}:\n%s", rec.body)
	}
	if !strings.Contains(rec.body, "ghp_secretvalue") {
		t.Errorf("interpolated value missing from body:\n%s", rec.body)
	}
	if !strings.Contains(out, "demo") {
		t.Errorf("stdout = %q, want the created-name echo", out)
	}
}

func TestCmd_Create_UndefinedVariableFailsWithoutPosting(t *testing.T) {
	rec := &recorder{status: http.StatusCreated}
	srv := rec.server()
	defer srv.Close()

	spec := "name: demo\nvalue: ${MISSING}\n"
	_, errOut, code := runWith(t, srv.URL, strings.NewReader(spec), "create", "-f", "-")
	if code == exitOK {
		t.Fatal("undefined variable should fail the command")
	}
	if rec.method != "" {
		t.Error("nothing should be POSTed when interpolation fails")
	}
	if !strings.Contains(errOut, "MISSING") {
		t.Errorf("stderr = %q, want the offending variable named", errOut)
	}
}

func TestCmd_Create_RequiresFileFlag(t *testing.T) {
	rec := &recorder{}
	srv := rec.server()
	defer srv.Close()

	_, errOut, code := runWith(t, srv.URL, nil, "create")
	if code == exitOK {
		t.Fatal("create without -f should fail")
	}
	if !strings.Contains(errOut, "-f") {
		t.Errorf("stderr = %q, want a -f requirement message", errOut)
	}
}

func TestCmd_Volume_LsCreateRm(t *testing.T) {
	t.Run("ls", func(t *testing.T) {
		rec := &recorder{respBody: `[{"name":"v1","quota_mib":1024,"used_bytes":0,"created_at":"2026-06-04"}]`}
		srv := rec.server()
		defer srv.Close()
		out, _, code := runWith(t, srv.URL, nil, "volume", "ls")
		if code != exitOK {
			t.Fatalf("exit = %d", code)
		}
		if rec.method != http.MethodGet || rec.path != "/volumes" {
			t.Errorf("request = %s %s, want GET /volumes", rec.method, rec.path)
		}
		if !strings.Contains(out, "v1") {
			t.Errorf("output missing volume row:\n%s", out)
		}
	})
	t.Run("create", func(t *testing.T) {
		rec := &recorder{status: http.StatusCreated, respBody: `{"name":"v1","size":"10G"}`}
		srv := rec.server()
		defer srv.Close()
		_, _, code := runWith(t, srv.URL, nil, "volume", "create", "v1", "--size", "10G")
		if code != exitOK {
			t.Fatalf("exit = %d", code)
		}
		if rec.method != http.MethodPost || rec.path != "/volumes" {
			t.Errorf("request = %s %s, want POST /volumes", rec.method, rec.path)
		}
		if !strings.Contains(rec.body, `"name":"v1"`) || !strings.Contains(rec.body, `"size":"10G"`) {
			t.Errorf("body = %s, want name/size", rec.body)
		}
	})
	t.Run("rm", func(t *testing.T) {
		rec := &recorder{status: http.StatusNoContent}
		srv := rec.server()
		defer srv.Close()
		_, _, code := runWith(t, srv.URL, nil, "volume", "rm", "v1")
		if code != exitOK {
			t.Fatalf("exit = %d", code)
		}
		if rec.method != http.MethodDelete || rec.path != "/volumes/v1" {
			t.Errorf("request = %s %s, want DELETE /volumes/v1", rec.method, rec.path)
		}
	})
}

func TestCmd_Volume_CreateFromManifest(t *testing.T) {
	t.Run("posts manifest from stdin and renders results on 201", func(t *testing.T) {
		rec := &recorder{
			status:   http.StatusCreated,
			respBody: `{"results":[{"name":"alpine-data","size":"1G","status":"created"}]}`,
		}
		srv := rec.server()
		defer srv.Close()

		manifest := "volumes:\n  - name: alpine-data\n    size: 1G\n"
		out, _, code := runWith(t, srv.URL, strings.NewReader(manifest), "volume", "create", "-f", "-")
		if code != exitOK {
			t.Fatalf("exit = %d, want 0", code)
		}
		if rec.method != http.MethodPost || rec.path != "/volumes" {
			t.Errorf("request = %s %s, want POST /volumes", rec.method, rec.path)
		}
		if !strings.Contains(rec.body, "alpine-data") {
			t.Errorf("posted body missing manifest contents:\n%s", rec.body)
		}
		if !strings.Contains(out, "created") || !strings.Contains(out, "alpine-data") {
			t.Errorf("output missing rendered results:\n%s", out)
		}
	})

	t.Run("exits non-zero on 207 partial failure", func(t *testing.T) {
		rec := &recorder{
			status: http.StatusMultiStatus,
			respBody: `{"results":[` +
				`{"name":"alpine-data","size":"1G","status":"created"},` +
				`{"name":"pg-data","size":"10G","status":"error","error":"exists at 5120MiB, cannot resize to 10240MiB"}]}`,
		}
		srv := rec.server()
		defer srv.Close()

		manifest := "volumes:\n  - name: alpine-data\n    size: 1G\n  - name: pg-data\n    size: 10G\n"
		out, _, code := runWith(t, srv.URL, strings.NewReader(manifest), "volume", "create", "-f", "-")
		if code == exitOK {
			t.Fatal("exit = 0, want non-zero on 207")
		}
		if code != exitPartial {
			t.Errorf("exit = %d, want exitPartial (%d)", code, exitPartial)
		}
		// The full result list is still rendered on a partial failure.
		if !strings.Contains(out, "error") || !strings.Contains(out, "pg-data") {
			t.Errorf("output missing the partial-failure results:\n%s", out)
		}
	})
}

func TestCmd_Snapshot_LsCreateRm(t *testing.T) {
	t.Run("ls", func(t *testing.T) {
		rec := &recorder{respBody: `[{"name":"snap","image_ref":"alpine","size_bytes":100,"created_at":"2026-06-06","parent_digest":null}]`}
		srv := rec.server()
		defer srv.Close()
		out, _, code := runWith(t, srv.URL, nil, "snapshot", "ls")
		if code != exitOK {
			t.Fatalf("exit = %d", code)
		}
		if rec.path != "/snapshots" {
			t.Errorf("path = %s, want /snapshots", rec.path)
		}
		if !strings.Contains(out, "snap") {
			t.Errorf("output missing snapshot row:\n%s", out)
		}
	})
	t.Run("create", func(t *testing.T) {
		rec := &recorder{status: http.StatusCreated, respBody: `{"name":"snap","from":"web"}`}
		srv := rec.server()
		defer srv.Close()
		_, _, code := runWith(t, srv.URL, nil, "snapshot", "create", "snap", "--from", "web", "--force")
		if code != exitOK {
			t.Fatalf("exit = %d", code)
		}
		if rec.method != http.MethodPost || rec.path != "/snapshots" {
			t.Errorf("request = %s %s, want POST /snapshots", rec.method, rec.path)
		}
		if !strings.Contains(rec.body, `"from":"web"`) || !strings.Contains(rec.body, `"force":true`) {
			t.Errorf("body = %s, want from/force", rec.body)
		}
	})
	t.Run("rm", func(t *testing.T) {
		rec := &recorder{status: http.StatusNoContent}
		srv := rec.server()
		defer srv.Close()
		_, _, code := runWith(t, srv.URL, nil, "snapshot", "rm", "snap")
		if code != exitOK {
			t.Fatalf("exit = %d", code)
		}
		if rec.method != http.MethodDelete || rec.path != "/snapshots/snap" {
			t.Errorf("request = %s %s, want DELETE /snapshots/snap", rec.method, rec.path)
		}
	})
}

// Server errors are rendered to stderr and mapped to a non-zero exit code.
func TestCmd_ServerErrorRendersAndMapsExitCode(t *testing.T) {
	rec := &recorder{status: http.StatusNotFound, respBody: `{"error":"sandbox not found"}`}
	srv := rec.server()
	defer srv.Close()

	out, errOut, code := runWith(t, srv.URL, nil, "inspect", "ghost")
	if code != exitClientError {
		t.Fatalf("exit = %d, want %d for a 4xx", code, exitClientError)
	}
	if out != "" {
		t.Errorf("stdout = %q, want nothing on error", out)
	}
	if !strings.Contains(errOut, "sandbox not found") {
		t.Errorf("stderr = %q, want the server message", errOut)
	}
}

// A 5xx maps to the distinct server-error exit code.
func TestCmd_ServerError5xxExitCode(t *testing.T) {
	rec := &recorder{status: http.StatusInternalServerError, respBody: `{"error":"boom"}`}
	srv := rec.server()
	defer srv.Close()

	_, _, code := runWith(t, srv.URL, nil, "ls")
	if code != exitServerError {
		t.Fatalf("exit = %d, want %d for a 5xx", code, exitServerError)
	}
}

// A command requiring auth with no token configured fails clearly and never
// issues the request.
func TestCmd_MissingTokenFailsClearly(t *testing.T) {
	rec := &recorder{}
	srv := rec.server()
	defer srv.Close()

	var out, errb bytes.Buffer
	// No --token and an empty environment: the token is unresolved.
	code := run([]string{"--server", srv.URL, "ls"}, strings.NewReader(""), &out, &errb, noEnv)
	if code != exitGeneric {
		t.Fatalf("exit = %d, want %d", code, exitGeneric)
	}
	if rec.method != "" {
		t.Error("no request should go out without a token")
	}
	if !strings.Contains(errb.String(), "token") {
		t.Errorf("stderr = %q, want a clear missing-token message", errb.String())
	}
}
