package main

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// lookupFrom builds a lookup over a fixed map for interpolation tests.
func lookupFrom(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

func TestInterpolate_SubstitutesScalarValue(t *testing.T) {
	raw := []byte("name: demo\nvalue: ${TOKEN}\n")
	out, err := interpolate(raw, lookupFrom(map[string]string{"TOKEN": "abc123"}))
	if err != nil {
		t.Fatalf("interpolate: %v", err)
	}
	var got map[string]any
	if err := yaml.Unmarshal(out, &got); err != nil {
		t.Fatalf("result not valid YAML: %v\n%s", err, out)
	}
	if got["value"] != "abc123" {
		t.Errorf("value = %v, want abc123", got["value"])
	}
}

// The cardinal safety property (ADR-0008): a value containing a newline and a
// YAML key-shaped fragment must NOT be able to inject a new key. It stays a
// single scalar value.
func TestInterpolate_InjectionImpossible(t *testing.T) {
	raw := []byte("value: ${EVIL}\n")
	out, err := interpolate(raw, lookupFrom(map[string]string{"EVIL": "a\nb: c"}))
	if err != nil {
		t.Fatalf("interpolate: %v", err)
	}
	var got map[string]any
	if err := yaml.Unmarshal(out, &got); err != nil {
		t.Fatalf("result not valid YAML: %v\n%s", err, out)
	}
	if len(got) != 1 {
		t.Fatalf("document has %d keys, want 1 — value injected structure:\n%s", len(got), out)
	}
	if _, injected := got["b"]; injected {
		t.Errorf("injected key 'b' appeared; interpolation is not value-safe:\n%s", out)
	}
	if got["value"] != "a\nb: c" {
		t.Errorf("value = %q, want the literal multi-line string", got["value"])
	}
}

func TestInterpolate_UndefinedVariableErrorsByName(t *testing.T) {
	raw := []byte("value: ${MISSING}\n")
	_, err := interpolate(raw, lookupFrom(map[string]string{}))
	if err == nil {
		t.Fatal("undefined variable should error, not substitute empty")
	}
	if !strings.Contains(err.Error(), "MISSING") {
		t.Errorf("error %q should name the missing variable", err.Error())
	}
}

func TestInterpolate_EmbeddedPlaceholderStaysOneScalar(t *testing.T) {
	raw := []byte("host: pre-${X}-post\n")
	out, err := interpolate(raw, lookupFrom(map[string]string{"X": "mid"}))
	if err != nil {
		t.Fatalf("interpolate: %v", err)
	}
	var got map[string]any
	if err := yaml.Unmarshal(out, &got); err != nil {
		t.Fatalf("result not valid YAML: %v\n%s", err, out)
	}
	if got["host"] != "pre-mid-post" {
		t.Errorf("host = %v, want pre-mid-post", got["host"])
	}
}

func TestInterpolate_NoPlaceholdersIsPreserved(t *testing.T) {
	raw := []byte("name: demo\nimage: alpine\ncpus: 2\n")
	out, err := interpolate(raw, lookupFrom(map[string]string{}))
	if err != nil {
		t.Fatalf("interpolate: %v", err)
	}
	var got map[string]any
	if err := yaml.Unmarshal(out, &got); err != nil {
		t.Fatalf("result not valid YAML: %v\n%s", err, out)
	}
	if got["name"] != "demo" || got["image"] != "alpine" || got["cpus"] != 2 {
		t.Errorf("document changed unexpectedly: %v", got)
	}
}

func TestInterpolate_MultipleVariables(t *testing.T) {
	raw := []byte("a: ${ONE}\nb: ${TWO}\n")
	out, err := interpolate(raw, lookupFrom(map[string]string{"ONE": "1", "TWO": "2"}))
	if err != nil {
		t.Fatalf("interpolate: %v", err)
	}
	var got map[string]any
	if err := yaml.Unmarshal(out, &got); err != nil {
		t.Fatalf("result not valid YAML: %v", err)
	}
	// Substituted values are kept as strings (value-safe), so "1" not 1.
	if got["a"] != "1" || got["b"] != "2" {
		t.Errorf("got %v, want a=1 b=2 as strings", got)
	}
}

func TestMakeLookup_SetOverridesEnv(t *testing.T) {
	getenv := func(k string) string {
		if k == "TOKEN" {
			return "from-env"
		}
		return ""
	}
	lookup := makeLookup(map[string]string{"TOKEN": "from-set"}, getenv)
	if v, ok := lookup("TOKEN"); !ok || v != "from-set" {
		t.Errorf("lookup(TOKEN) = %q,%v; want from-set,true (--set overrides env)", v, ok)
	}
}

func TestMakeLookup_FallsBackToEnv(t *testing.T) {
	getenv := func(k string) string {
		if k == "HOST" {
			return "env-host"
		}
		return ""
	}
	lookup := makeLookup(nil, getenv)
	if v, ok := lookup("HOST"); !ok || v != "env-host" {
		t.Errorf("lookup(HOST) = %q,%v; want env-host,true", v, ok)
	}
}

func TestMakeLookup_UnsetOrEmptyIsUndefined(t *testing.T) {
	lookup := makeLookup(nil, func(string) string { return "" })
	if _, ok := lookup("NOPE"); ok {
		t.Error("an unset/empty env var should report ok=false")
	}
}
