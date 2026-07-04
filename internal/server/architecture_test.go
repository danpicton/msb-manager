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

		// Walk per top-level function so ident bodies can be resolved against
		// their enclosing declaration (the local-variable DTO form, issue #13).
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			ast.Inspect(fn, func(n ast.Node) bool {
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
				if !isAPIConstructor(call.Args[2], fn) {
					t.Errorf("%s: writeJSON(..., http.StatusOK, %s) — 200 body must be built from internal/api (ADR-0006)",
						pos, exprString(call.Args[2]))
				}
				return true
			})
		}
	}

	// Guard against the check silently matching nothing (e.g. writeJSON renamed):
	// the read endpoints we expect to cover are list/inspect/metrics/volumes/snapshots.
	if checked < 5 {
		t.Fatalf("only inspected %d http.StatusOK writeJSON calls; expected >= 5 — has the response path changed?", checked)
	}
}

// The guard must accept both the inline form and the local-variable form, and
// still reject a body that never came from api (issue #13).
func TestIsAPIConstructor_LocalVariableForm(t *testing.T) {
	src := `package p

func h() {
	dto := api.NewThing(x)
	writeJSON(w, http.StatusOK, dto)
	raw := adapter()
	writeJSON(w, http.StatusOK, raw)
	writeJSON(w, http.StatusOK, api.NewOther(x))
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "synthetic.go", src, 0)
	if err != nil {
		t.Fatalf("parse synthetic source: %v", err)
	}
	fn := f.Decls[0].(*ast.FuncDecl)

	want := map[string]bool{"dto": true, "raw": false} // plus the inline call: true
	var inspected int
	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		ident, ok := call.Fun.(*ast.Ident)
		if !ok || ident.Name != "writeJSON" {
			return true
		}
		inspected++
		got := isAPIConstructor(call.Args[2], fn)
		switch arg := call.Args[2].(type) {
		case *ast.Ident:
			if got != want[arg.Name] {
				t.Errorf("isAPIConstructor(%s) = %v, want %v", arg.Name, got, want[arg.Name])
			}
		default:
			if !got {
				t.Errorf("isAPIConstructor(inline api call) = false, want true")
			}
		}
		return true
	})
	if inspected != 3 {
		t.Fatalf("inspected %d writeJSON calls in synthetic source, want 3", inspected)
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

// isAPIConstructor reports whether e is a call of the form api.Something(...),
// or an identifier assigned from such a call anywhere in scope (the
// local-variable form `dto := api.NewX(...); writeJSON(w, ..., dto)`, issue
// #13). Matching is by name within the enclosing top-level function — good
// enough for a lint-style guard; shadowing games would be caught in review.
func isAPIConstructor(e ast.Expr, scope ast.Node) bool {
	switch v := e.(type) {
	case *ast.CallExpr:
		sel, ok := v.Fun.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		pkg, ok := sel.X.(*ast.Ident)
		return ok && pkg.Name == "api"
	case *ast.Ident:
		if scope == nil {
			return false
		}
		found := false
		ast.Inspect(scope, func(n ast.Node) bool {
			assign, ok := n.(*ast.AssignStmt)
			if !ok {
				return !found
			}
			for i, lhs := range assign.Lhs {
				id, ok := lhs.(*ast.Ident)
				if !ok || id.Name != v.Name || i >= len(assign.Rhs) {
					continue
				}
				if isAPIConstructor(assign.Rhs[i], nil) {
					found = true
				}
			}
			return !found
		})
		return found
	}
	return false
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
