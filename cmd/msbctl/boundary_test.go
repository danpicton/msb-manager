package main

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// TestBoundary_NoInternalImports enforces the ADR-0007 invariant that msbctl is
// an opaque, HTTP-only client: it imports nothing under msb-manager/internal.
// Holding this line is the test that the client stayed opaque and keeps a later
// extraction to its own repository trivial. It speaks HTTP and JSON only.
func TestBoundary_NoInternalImports(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	fset := token.NewFileSet()
	for _, path := range files {
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			if strings.HasPrefix(p, "msb-manager/internal") {
				t.Errorf("%s imports %q — cmd/msbctl must not import internal/ (ADR-0007)", path, p)
			}
		}
	}
}
