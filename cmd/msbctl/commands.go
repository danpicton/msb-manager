package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// init registers every subcommand into the dispatch table. Keeping the command
// set here leaves main.go's dispatch skeleton independent of the command set.
func init() {
	registerCommand(&command{name: "ls", usage: "list sandboxes", run: cmdList})
	registerCommand(&command{name: "list", usage: "alias for ls", run: cmdList})
	registerCommand(&command{name: "inspect", usage: "show one sandbox (inspect <name>)", run: cmdInspect})
	registerCommand(&command{name: "create", usage: "create a sandbox from a spec (create -f <file|->)", run: cmdCreate})
	registerCommand(&command{name: "start", usage: "start a sandbox (start <name>)", run: cmdStart})
	registerCommand(&command{name: "stop", usage: "stop a sandbox (stop <name>)", run: cmdStop})
	registerCommand(&command{name: "rm", usage: "remove a sandbox (rm <name>)", run: cmdRm})
	registerCommand(&command{name: "logs", usage: "fetch sandbox logs (logs <name> [--tail --since --source])", run: cmdLogs})
	registerCommand(&command{name: "metrics", usage: "show sandbox metrics (metrics <name>)", run: cmdMetrics})
	registerCommand(&command{name: "volume", usage: "manage volumes (volume ls|create|rm)", run: cmdVolume})
	registerCommand(&command{name: "snapshot", usage: "manage snapshots (snapshot ls|create|rm)", run: cmdSnapshot})
}

// --- shared request helpers ---

// ensureToken short-circuits an authed call that has no credential, with a
// clear message rather than a bare 401 from the server.
func (env *runEnv) ensureToken() int {
	if err := requireToken(env.target); err != nil {
		fmt.Fprintf(env.stderr, "error: %v\n", err)
		return exitGeneric
	}
	return exitOK
}

// doRead performs a GET and renders the body in the requested format, or renders
// the server error. It is the one path every read command funnels through.
func (env *runEnv) doRead(ctx context.Context, path, format string, columns []string) int {
	if code := env.ensureToken(); code != exitOK {
		return code
	}
	resp, err := env.client.do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		fmt.Fprintf(env.stderr, "error: %v\n", err)
		return exitGeneric
	}
	if resp.status >= 400 {
		renderServerError(env.stderr, resp.status, resp.body)
		return exitCodeForStatus(resp.status)
	}
	if err := renderRead(env.stdout, format, resp.body, columns); err != nil {
		fmt.Fprintf(env.stderr, "error: %v\n", err)
		return exitGeneric
	}
	return exitOK
}

// doAction performs a mutating call and reports the outcome. On success it
// echoes the server's response body if there is one (e.g. create returns the
// new name), otherwise the supplied confirmation message.
func (env *runEnv) doAction(ctx context.Context, method, path string, body io.Reader, contentType, confirm string) int {
	if code := env.ensureToken(); code != exitOK {
		return code
	}
	resp, err := env.client.do(ctx, method, path, body, contentType)
	if err != nil {
		fmt.Fprintf(env.stderr, "error: %v\n", err)
		return exitGeneric
	}
	if resp.status >= 400 {
		renderServerError(env.stderr, resp.status, resp.body)
		return exitCodeForStatus(resp.status)
	}
	if trimmed := bytes.TrimSpace(resp.body); len(trimmed) > 0 {
		_ = writeJSONOut(env.stdout, trimmed)
	} else {
		fmt.Fprintln(env.stdout, confirm)
	}
	return exitOK
}

// doStatusAction performs a mutating call that always returns a body to render
// and derives the exit code from the HTTP status (via exitCodeForStatus) rather
// than collapsing every 2xx to success. That is what lets a 207 Multi-Status
// render its results body and still exit non-zero. The body is passed through
// opaquely — msbctl owns no response schema (ADR-0007).
func (env *runEnv) doStatusAction(ctx context.Context, method, path string, body io.Reader, contentType string) int {
	if code := env.ensureToken(); code != exitOK {
		return code
	}
	resp, err := env.client.do(ctx, method, path, body, contentType)
	if err != nil {
		fmt.Fprintf(env.stderr, "error: %v\n", err)
		return exitGeneric
	}
	if resp.status >= 400 {
		renderServerError(env.stderr, resp.status, resp.body)
		return exitCodeForStatus(resp.status)
	}
	if trimmed := bytes.TrimSpace(resp.body); len(trimmed) > 0 {
		_ = writeJSONOut(env.stdout, trimmed)
	}
	return exitCodeForStatus(resp.status)
}

// parseFlags parses a per-command flag set, returning the positional arguments.
// It allows flags and positionals to be interspersed (e.g. `logs web --tail 5`
// as well as `logs --tail 5 web`), which the stdlib flag package does not do on
// its own — it stops at the first non-flag token. Usage/errors go to stderr.
func (env *runEnv) parseFlags(fs *flag.FlagSet, args []string) ([]string, bool) {
	fs.SetOutput(env.stderr)
	var positionals []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, false
		}
		rest := fs.Args()
		if len(rest) == 0 {
			break
		}
		positionals = append(positionals, rest[0])
		args = rest[1:]
	}
	return positionals, true
}

// oneName extracts a single required positional <name>, validating only that it
// is present. The server does the authoritative identifier validation.
func oneName(env *runEnv, positionals []string, noun string) (string, bool) {
	if len(positionals) != 1 || positionals[0] == "" {
		fmt.Fprintf(env.stderr, "error: expected exactly one %s name\n", noun)
		return "", false
	}
	return positionals[0], true
}

// sandboxPath builds /sandboxes/{name}[/suffix] with the name URL-escaped.
func sandboxPath(name, suffix string) string {
	p := "/sandboxes/" + url.PathEscape(name)
	if suffix != "" {
		p += "/" + suffix
	}
	return p
}

// --- read commands ---

func cmdList(ctx context.Context, env *runEnv, args []string) int {
	fs := flag.NewFlagSet("ls", flag.ContinueOnError)
	format := fs.String("o", formatTable, "output format: table|json|yaml")
	if _, ok := env.parseFlags(fs, args); !ok {
		return exitGeneric
	}
	return env.doRead(ctx, "/sandboxes", *format, []string{"name", "status", "image", "created_at"})
}

func cmdInspect(ctx context.Context, env *runEnv, args []string) int {
	fs := flag.NewFlagSet("inspect", flag.ContinueOnError)
	format := fs.String("o", formatTable, "output format: table|json|yaml")
	pos, ok := env.parseFlags(fs, args)
	if !ok {
		return exitGeneric
	}
	name, ok := oneName(env, pos, "sandbox")
	if !ok {
		return exitGeneric
	}
	return env.doRead(ctx, sandboxPath(name, ""), *format, nil)
}

func cmdMetrics(ctx context.Context, env *runEnv, args []string) int {
	fs := flag.NewFlagSet("metrics", flag.ContinueOnError)
	format := fs.String("o", formatTable, "output format: table|json|yaml")
	pos, ok := env.parseFlags(fs, args)
	if !ok {
		return exitGeneric
	}
	name, ok := oneName(env, pos, "sandbox")
	if !ok {
		return exitGeneric
	}
	return env.doRead(ctx, sandboxPath(name, "metrics"), *format, nil)
}

func cmdLogs(ctx context.Context, env *runEnv, args []string) int {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	tail := fs.String("tail", "", "show only the last N lines")
	since := fs.String("since", "", "only entries since this time/duration")
	source := fs.String("source", "", "filter by source (e.g. stdout,stderr)")
	pos, ok := env.parseFlags(fs, args)
	if !ok {
		return exitGeneric
	}
	name, ok := oneName(env, pos, "sandbox")
	if !ok {
		return exitGeneric
	}
	q := url.Values{}
	if *tail != "" {
		q.Set("tail", *tail)
	}
	if *since != "" {
		q.Set("since", *since)
	}
	if *source != "" {
		q.Set("source", *source)
	}
	path := sandboxPath(name, "logs")
	if len(q) > 0 {
		path += "?" + q.Encode()
	}

	// Logs are an opaque NDJSON stream; pass the body straight through.
	if code := env.ensureToken(); code != exitOK {
		return code
	}
	resp, err := env.client.do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		fmt.Fprintf(env.stderr, "error: %v\n", err)
		return exitGeneric
	}
	if resp.status >= 400 {
		renderServerError(env.stderr, resp.status, resp.body)
		return exitCodeForStatus(resp.status)
	}
	_, _ = env.stdout.Write(resp.body)
	return exitOK
}

// --- sandbox action commands ---

func cmdStart(ctx context.Context, env *runEnv, args []string) int {
	return env.sandboxAction(ctx, args, "start", "started")
}

func cmdStop(ctx context.Context, env *runEnv, args []string) int {
	return env.sandboxAction(ctx, args, "stop", "stopped")
}

func (env *runEnv) sandboxAction(ctx context.Context, args []string, verb, confirm string) int {
	fs := flag.NewFlagSet(verb, flag.ContinueOnError)
	pos, ok := env.parseFlags(fs, args)
	if !ok {
		return exitGeneric
	}
	name, ok := oneName(env, pos, "sandbox")
	if !ok {
		return exitGeneric
	}
	return env.doAction(ctx, http.MethodPost, sandboxPath(name, verb), nil, "", confirm+" "+name)
}

func cmdRm(ctx context.Context, env *runEnv, args []string) int {
	fs := flag.NewFlagSet("rm", flag.ContinueOnError)
	pos, ok := env.parseFlags(fs, args)
	if !ok {
		return exitGeneric
	}
	name, ok := oneName(env, pos, "sandbox")
	if !ok {
		return exitGeneric
	}
	return env.doAction(ctx, http.MethodDelete, sandboxPath(name, ""), nil, "", "removed "+name)
}

// --- create (with secret interpolation) ---

// kvFlag collects repeatable KEY=VALUE flags into a map.
type kvFlag struct{ m map[string]string }

func (k *kvFlag) String() string { return "" }
func (k *kvFlag) Set(s string) error {
	key, val, ok := splitKeyValue(s)
	if !ok || key == "" {
		return fmt.Errorf("expected KEY=VALUE, got %q", s)
	}
	if k.m == nil {
		k.m = map[string]string{}
	}
	k.m[key] = val
	return nil
}

func cmdCreate(ctx context.Context, env *runEnv, args []string) int {
	fs := flag.NewFlagSet("create", flag.ContinueOnError)
	specFile := fs.String("f", "", "spec file to create from (- for stdin)")
	sets := &kvFlag{}
	fs.Var(sets, "set", "set an interpolation variable (repeatable): --set KEY=VALUE")
	if _, ok := env.parseFlags(fs, args); !ok {
		return exitGeneric
	}
	if *specFile == "" {
		fmt.Fprintln(env.stderr, "error: create requires -f <file|-> (a spec file or - for stdin)")
		return exitGeneric
	}

	raw, err := env.readSpec(*specFile)
	if err != nil {
		fmt.Fprintf(env.stderr, "error: %v\n", err)
		return exitGeneric
	}
	// Value-safe ${VAR} interpolation over the raw bytes before POSTing
	// (ADR-0008). Secret values come from the environment and --set overrides.
	interpolated, err := interpolate(raw, makeLookup(sets.m, env.getenv))
	if err != nil {
		fmt.Fprintf(env.stderr, "error: %v\n", err)
		return exitGeneric
	}
	return env.doAction(ctx, http.MethodPost, "/sandboxes",
		bytes.NewReader(interpolated), "application/yaml", "created")
}

// readSpec reads the spec from a file or, when path is "-", from stdin.
func (env *runEnv) readSpec(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(env.stdin)
	}
	return os.ReadFile(path)
}

// --- volume subcommands ---

func cmdVolume(ctx context.Context, env *runEnv, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(env.stderr, "error: volume needs a subcommand: ls|create|rm")
		return exitGeneric
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "ls", "list":
		fs := flag.NewFlagSet("volume ls", flag.ContinueOnError)
		format := fs.String("o", formatTable, "output format: table|json|yaml")
		if _, ok := env.parseFlags(fs, rest); !ok {
			return exitGeneric
		}
		return env.doRead(ctx, "/volumes", *format, []string{"name", "quota_mib", "used_bytes", "created_at"})
	case "create":
		fs := flag.NewFlagSet("volume create", flag.ContinueOnError)
		size := fs.String("size", "", "volume size, e.g. 10G (single-shot create)")
		manifest := fs.String("f", "", "manifest file of volumes to batch-create (- for stdin)")
		pos, ok := env.parseFlags(fs, rest)
		if !ok {
			return exitGeneric
		}
		// Declarative batch: POST the manifest as-is and render the per-item
		// results. Exits non-zero on 207 via exitCodeForStatus — the handling is
		// generic, so msbctl learns nothing volume-specific (ADR-0007).
		if *manifest != "" {
			raw, err := env.readSpec(*manifest)
			if err != nil {
				fmt.Fprintf(env.stderr, "error: %v\n", err)
				return exitGeneric
			}
			return env.doStatusAction(ctx, http.MethodPost, "/volumes",
				bytes.NewReader(raw), "application/yaml")
		}
		// Single-shot create (unchanged): name + --size.
		name, ok := oneName(env, pos, "volume")
		if !ok {
			return exitGeneric
		}
		if *size == "" {
			fmt.Fprintln(env.stderr, "error: volume create requires --size (or -f <manifest> for a batch)")
			return exitGeneric
		}
		body := volumeCreateBody(name, *size)
		return env.doAction(ctx, http.MethodPost, "/volumes", strings.NewReader(body), "application/json", "created volume "+name)
	case "rm":
		fs := flag.NewFlagSet("volume rm", flag.ContinueOnError)
		pos, ok := env.parseFlags(fs, rest)
		if !ok {
			return exitGeneric
		}
		name, ok := oneName(env, pos, "volume")
		if !ok {
			return exitGeneric
		}
		return env.doAction(ctx, http.MethodDelete, "/volumes/"+url.PathEscape(name), nil, "", "removed volume "+name)
	default:
		fmt.Fprintf(env.stderr, "error: unknown volume subcommand %q (want ls|create|rm)\n", sub)
		return exitGeneric
	}
}

// --- snapshot subcommands ---

func cmdSnapshot(ctx context.Context, env *runEnv, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(env.stderr, "error: snapshot needs a subcommand: ls|create|rm")
		return exitGeneric
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "ls", "list":
		fs := flag.NewFlagSet("snapshot ls", flag.ContinueOnError)
		format := fs.String("o", formatTable, "output format: table|json|yaml")
		if _, ok := env.parseFlags(fs, rest); !ok {
			return exitGeneric
		}
		return env.doRead(ctx, "/snapshots", *format,
			[]string{"name", "image_ref", "size_bytes", "created_at", "parent_digest"})
	case "create":
		fs := flag.NewFlagSet("snapshot create", flag.ContinueOnError)
		from := fs.String("from", "", "source stopped sandbox (required)")
		force := fs.Bool("force", false, "overwrite an existing snapshot of the same name")
		labels := &kvFlag{}
		fs.Var(labels, "label", "set a snapshot label (repeatable): --label KEY=VALUE")
		pos, ok := env.parseFlags(fs, rest)
		if !ok {
			return exitGeneric
		}
		name, ok := oneName(env, pos, "snapshot")
		if !ok {
			return exitGeneric
		}
		if *from == "" {
			fmt.Fprintln(env.stderr, "error: snapshot create requires --from <sandbox>")
			return exitGeneric
		}
		body := snapshotCreateBody(*from, name, *force, labels.m)
		return env.doAction(ctx, http.MethodPost, "/snapshots", strings.NewReader(body), "application/json", "created snapshot "+name)
	case "rm":
		fs := flag.NewFlagSet("snapshot rm", flag.ContinueOnError)
		pos, ok := env.parseFlags(fs, rest)
		if !ok {
			return exitGeneric
		}
		name, ok := oneName(env, pos, "snapshot")
		if !ok {
			return exitGeneric
		}
		return env.doAction(ctx, http.MethodDelete, "/snapshots/"+url.PathEscape(name), nil, "", "removed snapshot "+name)
	default:
		fmt.Fprintf(env.stderr, "error: unknown snapshot subcommand %q (want ls|create|rm)\n", sub)
		return exitGeneric
	}
}

// volumeCreateBody and snapshotCreateBody assemble the small JSON request bodies
// for the volume/snapshot create endpoints. These are request *inputs*, not the
// sandbox spec schema or a response DTO (ADR-0007's opacity rule is about those);
// the two-field bodies are part of the wire contract for these endpoints.
func volumeCreateBody(name, size string) string {
	return fmt.Sprintf(`{"name":%s,"size":%s}`, jsonString(name), jsonString(size))
}

// snapshotCreateBody marshals via an anonymous struct rather than a hand-built
// fmt.Sprintf: the nested `labels` object makes string assembly an ordering
// hazard (issue #16), and json.Marshal handles escaping and omitempty for us.
func snapshotCreateBody(from, name string, force bool, labels map[string]string) string {
	b, _ := json.Marshal(struct {
		From   string            `json:"from"`
		Name   string            `json:"name"`
		Force  bool              `json:"force"`
		Labels map[string]string `json:"labels,omitempty"`
	}{From: from, Name: name, Force: force, Labels: labels})
	return string(b)
}

// jsonString quotes a string as a JSON literal so a value with a quote or
// backslash cannot break the small hand-built request bodies above.
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
