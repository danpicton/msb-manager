package server

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWireBodiesBuiltFromAPI enforces ADR-0006 structurally: every success
// (200 OK) response that goes through writeJSON must hand it a value built by
// the internal/api package, never an internal/msb adapter struct passed
// straight through. A golden-bytes test can't catch a regression here (the DTO
// JSON is byte-identical to the adapter JSON by design), so this AST check is
// the guard that the *seam itself* stays in place.
//
// The rule checked: for any call writeJSON(w, http.StatusOK, body), `body`
// must be a call expression whose callee is a selector on the `api` package
// (e.g. api.NewSandboxDetail(...)). Non-OK writeJSON calls (error maps, 201
// create acks) are out of scope — they carry no adapter type.
func TestWireBodiesBuiltFromAPI(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}

	fset := token.NewFileSet()
	var checked int
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		f, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}

		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			ident, ok := call.Fun.(*ast.Ident)
			if !ok || ident.Name != "writeJSON" || len(call.Args) != 3 {
				return true
			}
			if !isStatusOK(call.Args[1]) {
				return true
			}
			checked++
			pos := fset.Position(call.Pos())
			if !isAPIConstructor(call.Args[2]) {
				t.Errorf("%s: writeJSON(..., http.StatusOK, %s) — 200 body must be built from internal/api (ADR-0006)",
					pos, exprString(call.Args[2]))
			}
			return true
		})
	}

	// Guard against the check silently matching nothing (e.g. writeJSON renamed):
	// the read endpoints we expect to cover are list/inspect/metrics/volumes/snapshots.
	if checked < 5 {
		t.Fatalf("only inspected %d http.StatusOK writeJSON calls; expected >= 5 — has the response path changed?", checked)
	}
}

func isStatusOK(e ast.Expr) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "http" && sel.Sel.Name == "StatusOK"
}

// isAPIConstructor reports whether e is a call of the form api.Something(...).
func isAPIConstructor(e ast.Expr) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "api"
}

func exprString(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.CallExpr:
		return exprString(v.Fun) + "(...)"
	case *ast.SelectorExpr:
		return exprString(v.X) + "." + v.Sel.Name
	default:
		return "<expr>"
	}
}
