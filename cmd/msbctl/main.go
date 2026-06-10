// Command msbctl is the remote command-line client for msb-manager. It is a
// thin, opaque HTTP client: it owns no spec schema and no response DTOs, streams
// a spec to create (after value-safe interpolation), and renders JSON responses
// generically for reads (ADR-0007). It imports nothing under internal/ — that
// boundary is the test that the client stayed opaque.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv))
}

// runEnv bundles everything a subcommand needs: the HTTP client, the resolved
// target, an injected environment lookup (for create's interpolation), and the
// input/output streams. Passing it in keeps subcommands testable without globals.
type runEnv struct {
	client *client
	target target
	getenv func(string) string
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
}

// command is one subcommand. run returns the process exit code.
type command struct {
	name  string
	usage string // one-line summary for help
	run   func(ctx context.Context, env *runEnv, args []string) int
}

// commands is the subcommand registry, populated by init() in commands.go so
// the dispatch skeleton here stays independent of the command set.
var commands = map[string]*command{}

func registerCommand(c *command) { commands[c.name] = c }

// run is the testable entry point: it takes argv (without the program name),
// the output streams, and an environment lookup, and returns an exit code.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) int {
	var flags cliFlags
	gf := flag.NewFlagSet("msbctl", flag.ContinueOnError)
	gf.SetOutput(stderr)
	gf.StringVar(&flags.server, "server", "", "msb-manager base URL (overrides env and config)")
	gf.StringVar(&flags.profile, "profile", "", "config-file profile to use")
	gf.StringVar(&flags.token, "token", "", "bearer token (LAST RESORT: visible in process args; prefer MSB_MANAGER_TOKEN or the config file)")
	gf.Usage = func() { printUsage(stderr, gf) }

	if err := gf.Parse(args); err != nil {
		// flag already printed the error and usage.
		return exitGeneric
	}
	rest := gf.Args()
	if len(rest) == 0 {
		printUsage(stderr, gf)
		return exitGeneric
	}
	name, cmdArgs := rest[0], rest[1:]

	if name == "help" {
		printUsage(stdout, gf)
		return exitOK
	}

	cmd, ok := commands[name]
	if !ok {
		fmt.Fprintf(stderr, "unknown command %q\n\n", name)
		printUsage(stderr, gf)
		return exitGeneric
	}

	cfg, err := loadConfig(configPath(getenv), func(msg string) {
		fmt.Fprintln(stderr, "warning: "+msg)
	})
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return exitGeneric
	}
	tgt, err := resolveTarget(flags, getenv, cfg)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return exitGeneric
	}

	env := &runEnv{
		client: newClient(tgt),
		target: tgt,
		getenv: getenv,
		stdin:  stdin,
		stdout: stdout,
		stderr: stderr,
	}
	return cmd.run(context.Background(), env, cmdArgs)
}

// printUsage writes the top-level help, including the registered subcommands and
// the global flags. The --token caveat lives in the flag's own help string.
func printUsage(w io.Writer, gf *flag.FlagSet) {
	fmt.Fprintln(w, "msbctl — remote client for msb-manager")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage: msbctl [global flags] <command> [args]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	for _, c := range sortedCommands() {
		fmt.Fprintf(w, "  %-20s %s\n", c.name, c.usage)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Global flags:")
	gf.SetOutput(w)
	gf.PrintDefaults()
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Target/auth resolution (highest wins): flags > env "+
		"(MSB_MANAGER_URL/TOKEN/PROFILE) > config-file profile > defaults.")
	fmt.Fprintln(w, "Prefer the token via MSB_MANAGER_TOKEN or the 0600 config file; "+
		"--token is a last resort because argv is world-readable (issue #7).")
}

func sortedCommands() []*command {
	out := make([]*command, 0, len(commands))
	for _, c := range commands {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// splitKeyValue splits "KEY=VALUE" into its parts. Used by --set and any other
// k=v flag. The value may itself contain '='; only the first is the separator.
func splitKeyValue(s string) (key, value string, ok bool) {
	i := strings.IndexByte(s, '=')
	if i < 0 {
		return "", "", false
	}
	return s[:i], s[i+1:], true
}
