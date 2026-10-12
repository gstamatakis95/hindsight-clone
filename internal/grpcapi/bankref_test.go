package grpcapi

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoBankRefLiterals: no code outside internal/domain builds a domain.BankRef with a keyed literal. A literal
// that forgets the Org field compiles and silently names the bank of org "" in every query; domain.NewBankRef takes
// all three ids, so the compiler catches a missing one. The empty literal domain.BankRef{} (the zero ref returned
// with an error) is allowed.
func TestNoBankRefLiterals(t *testing.T) {
	root := repoRoot(t)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() {
			if rel == "gen" || rel == filepath.Join("internal", "domain") || strings.HasPrefix(d.Name(), ".") && rel != "." {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") {
			checkBankRefLiterals(t, path, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// checkBankRefLiterals reports every non-empty domain.BankRef composite literal in the file, including elements of a
// []domain.BankRef or map[...]domain.BankRef literal written without their type.
func checkBankRefLiterals(t *testing.T, path, rel string) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		if isBankRef(lit.Type) && len(lit.Elts) > 0 {
			t.Errorf("%s:%d: domain.BankRef literal; use domain.NewBankRef", rel, fset.Position(lit.Pos()).Line)
		}
		if elem := containerElem(lit.Type); elem != nil && isBankRef(elem) {
			for _, e := range lit.Elts {
				if kv, ok := e.(*ast.KeyValueExpr); ok {
					e = kv.Value
				}
				if inner, ok := e.(*ast.CompositeLit); ok && inner.Type == nil && len(inner.Elts) > 0 {
					t.Errorf("%s:%d: domain.BankRef literal; use domain.NewBankRef", rel, fset.Position(inner.Pos()).Line)
				}
			}
		}
		return true
	})
}

// isBankRef reports whether e is the type expression domain.BankRef.
func isBankRef(e ast.Expr) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "BankRef" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "domain"
}

// containerElem returns the element type of a slice, array or map type expression, else nil.
func containerElem(e ast.Expr) ast.Expr {
	switch x := e.(type) {
	case *ast.ArrayType:
		return x.Elt
	case *ast.MapType:
		return x.Value
	}
	return nil
}

// repoRoot walks up from the working directory to the directory holding go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}
