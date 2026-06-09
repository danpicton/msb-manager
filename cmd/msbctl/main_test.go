package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRun_HelpMentionsTokenArgvCaveat(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{"help"}, &out, &errOut, noEnv)
	if code != exitOK {
		t.Fatalf("help exit = %d, want %d", code, exitOK)
	}
	help := out.String()
	if !strings.Contains(help, "--token") {
		t.Error("help should document the --token flag")
	}
	// The argv-leak caveat (issue #7) must be visible so operators prefer
	// env/config over --token.
	if !strings.Contains(strings.ToLower(help), "argv") && !strings.Contains(help, "world-readable") {
		t.Error("help should warn that --token leaks via argv")
	}
}

func TestRun_UnknownCommandIsGenericError(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{"frobnicate"}, &out, &errOut, noEnv)
	if code != exitGeneric {
		t.Fatalf("exit = %d, want %d", code, exitGeneric)
	}
	if !strings.Contains(errOut.String(), "unknown command") {
		t.Errorf("stderr = %q, want an unknown-command message", errOut.String())
	}
}

func TestRun_NoArgsShowsUsage(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run(nil, &out, &errOut, noEnv)
	if code != exitGeneric {
		t.Fatalf("exit = %d, want %d", code, exitGeneric)
	}
	if !strings.Contains(errOut.String(), "Usage:") {
		t.Errorf("stderr = %q, want usage text", errOut.String())
	}
}

func TestSplitKeyValue(t *testing.T) {
	k, v, ok := splitKeyValue("FOO=bar=baz")
	if !ok || k != "FOO" || v != "bar=baz" {
		t.Errorf("splitKeyValue = %q,%q,%v; want FOO,bar=baz,true", k, v, ok)
	}
	if _, _, ok := splitKeyValue("noequals"); ok {
		t.Error("splitKeyValue should report ok=false when there is no '='")
	}
}
