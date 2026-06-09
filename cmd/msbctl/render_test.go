package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRenderRead_JSONIsVerbatimPassthrough(t *testing.T) {
	body := []byte(`[{"name":"a","status":"Running"}]`)
	var buf bytes.Buffer
	if err := renderRead(&buf, formatJSON, body, nil); err != nil {
		t.Fatalf("renderRead: %v", err)
	}
	if strings.TrimRight(buf.String(), "\n") != string(body) {
		t.Errorf("json output = %q, want verbatim passthrough", buf.String())
	}
}

func TestRenderRead_YAMLReencodesJSON(t *testing.T) {
	body := []byte(`{"name":"demo","cpus":2}`)
	var buf bytes.Buffer
	if err := renderRead(&buf, formatYAML, body, nil); err != nil {
		t.Fatalf("renderRead: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "name: demo") || !strings.Contains(out, "cpus: 2") {
		t.Errorf("yaml output missing expected keys:\n%s", out)
	}
}

func TestRenderRead_TableListHasHeadersAndRows(t *testing.T) {
	body := []byte(`[{"name":"web","status":"Running","image":"alpine","created_at":"2026-06-01"}]`)
	var buf bytes.Buffer
	if err := renderRead(&buf, formatTable, body, []string{"name", "status", "image", "created_at"}); err != nil {
		t.Fatalf("renderRead: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"NAME", "STATUS", "IMAGE", "web", "Running", "alpine"} {
		if !strings.Contains(out, want) {
			t.Errorf("table output missing %q:\n%s", want, out)
		}
	}
}

func TestRenderRead_TableEmptyListIsFriendly(t *testing.T) {
	var buf bytes.Buffer
	if err := renderRead(&buf, formatTable, []byte(`[]`), []string{"name"}); err != nil {
		t.Fatalf("renderRead: %v", err)
	}
	if !strings.Contains(buf.String(), "(none)") {
		t.Errorf("empty table = %q, want a (none) marker", buf.String())
	}
}

func TestRenderRead_TableObjectIsKeyValue(t *testing.T) {
	body := []byte(`{"name":"web","cpus":2,"memory_mib":512}`)
	var buf bytes.Buffer
	if err := renderRead(&buf, formatTable, body, nil); err != nil {
		t.Fatalf("renderRead: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "name") || !strings.Contains(out, "web") || !strings.Contains(out, "512") {
		t.Errorf("object table missing expected pairs:\n%s", out)
	}
}

func TestRenderRead_UnknownFormatErrors(t *testing.T) {
	if err := renderRead(&bytes.Buffer{}, "xml", []byte(`{}`), nil); err == nil {
		t.Fatal("unknown format should error")
	}
}

func TestCellString_LargeIntegerNotScientific(t *testing.T) {
	// size_bytes etc. decode to float64; a table must not show 4.294967296e+09.
	if got := cellString(float64(4294967296)); got != "4294967296" {
		t.Errorf("cellString(4294967296) = %q, want plain integer", got)
	}
}

func TestCellString_NilIsDash(t *testing.T) {
	if got := cellString(nil); got != "-" {
		t.Errorf("cellString(nil) = %q, want -", got)
	}
}
