package analyzer

import (
	"go/ast"
	"go/token"
	"go/types"
	"sort"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// Analyzer reports model calls and mis-ordered Enqueue calls inside the function literal given to InTx.
var Analyzer = &analysis.Analyzer{
	Name: "txcheck",
	Doc:  "reports model calls inside InTx literals and Enqueue calls placed before tx.Seq",
	Run:  run,
}

// aiPkg is the package that declares the model interfaces.
const aiPkg = "memd/internal/ai"

// modelInterfaces are the interfaces in aiPkg whose implementers must not be called inside a transaction.
var modelInterfaces = []string{"LLM", "Embedder", "Reranker"}

// modelTypes are the concrete types (package path, type name) whose methods must not be called inside a
// transaction. Current and planned locations are both listed; a path that does not exist is harmless.
var modelTypes = []struct{ pkg, name string }{
	{"memd/internal/engine/recall", "Engine"},
	{"memd/internal/recall", "Engine"},
	{"memd/internal/engine/reflect", "Agent"},
	{"memd/internal/reflect", "Agent"},
	{"memd/internal/engine/timex", "Normalizer"},
	{"memd/internal/timex", "Normalizer"},
	{aiPkg, "Router"},
}

func run(pass *analysis.Pass) (any, error) {
	models := modelIfaces(pass.Pkg)
	seen := map[token.Pos]bool{}
	for _, f := range pass.Files {
		ast.Inspect(f, func(n ast.Node) bool {
			if lit := inTxLiteral(pass, n); lit != nil {
				checkLiteral(pass, lit, models, seen)
			}
			return true
		})
	}
	return nil, nil
}

// inTxLiteral returns the function literal argument when n is a call to a method named InTx, else nil.
func inTxLiteral(pass *analysis.Pass, n ast.Node) *ast.FuncLit {
	call, ok := n.(*ast.CallExpr)
	if !ok || len(call.Args) == 0 {
		return nil
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "InTx" || !isMethod(pass, sel) {
		return nil
	}
	lit, _ := call.Args[len(call.Args)-1].(*ast.FuncLit)
	return lit
}

// isMethod reports whether sel selects a method value (not a package-qualified function or a field).
func isMethod(pass *analysis.Pass, sel *ast.SelectorExpr) bool {
	s := pass.TypesInfo.Selections[sel]
	return s != nil && s.Kind() == types.MethodVal
}

// checkLiteral applies both rules to the body of one InTx literal. A nested InTx literal is checked again on its
// own visit; seen keeps a position from being reported twice.
func checkLiteral(pass *analysis.Pass, lit *ast.FuncLit, models []*types.Interface, seen map[token.Pos]bool) {
	var firstSeq token.Pos
	var enqueues []*ast.CallExpr
	ast.Inspect(lit.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && isMethod(pass, sel) {
			if sel.Sel.Name == "Seq" && (firstSeq == 0 || call.Pos() < firstSeq) {
				firstSeq = call.Pos()
			}
			if t := pass.TypesInfo.TypeOf(sel.X); t != nil && isModel(t, models) && !seen[call.Pos()] {
				seen[call.Pos()] = true
				pass.Reportf(call.Pos(), "txcheck/no-model-call-in-tx: %s.%s is a model call inside InTx; "+
					"call first, then write in a short transaction", types.TypeString(t, nil), sel.Sel.Name)
			}
		}
		if isEnqueue(call) {
			enqueues = append(enqueues, call)
		}
		return true
	})
	if firstSeq == 0 {
		return
	}
	sort.Slice(enqueues, func(i, j int) bool { return enqueues[i].Pos() < enqueues[j].Pos() })
	for _, c := range enqueues {
		if c.Pos() < firstSeq && !seen[c.Pos()] {
			seen[c.Pos()] = true
			pass.Reportf(c.Pos(), "txcheck/seq-before-enqueue: %s is called before tx.Seq in the same InTx; "+
				"take tx.Seq(bank) first", calleeName(c))
		}
	}
}

// isModel reports whether t is, points to, or implements one of the forbidden model types.
func isModel(t types.Type, models []*types.Interface) bool {
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	if n, ok := t.(*types.Named); ok && n.Obj().Pkg() != nil {
		for _, m := range modelTypes {
			if n.Obj().Pkg().Path() == m.pkg && n.Obj().Name() == m.name {
				return true
			}
		}
	}
	for _, iface := range models {
		if types.Implements(t, iface) || types.Implements(types.NewPointer(t), iface) {
			return true
		}
	}
	return false
}

// modelIfaces finds the model interfaces among pkg and its transitive imports.
func modelIfaces(pkg *types.Package) []*types.Interface {
	ai := findPkg(pkg, aiPkg, map[*types.Package]bool{})
	if ai == nil {
		return nil
	}
	var out []*types.Interface
	for _, name := range modelInterfaces {
		if obj := ai.Scope().Lookup(name); obj != nil {
			if iface, ok := obj.Type().Underlying().(*types.Interface); ok {
				out = append(out, iface)
			}
		}
	}
	return out
}

// findPkg searches pkg and its transitive imports for the package with the given path.
func findPkg(pkg *types.Package, path string, seen map[*types.Package]bool) *types.Package {
	if pkg.Path() == path {
		return pkg
	}
	if seen[pkg] {
		return nil
	}
	seen[pkg] = true
	for _, imp := range pkg.Imports() {
		if p := findPkg(imp, path, seen); p != nil {
			return p
		}
	}
	return nil
}

// isEnqueue reports whether call is Enqueue*, QueueModelRefresh, or a method of those names.
func isEnqueue(call *ast.CallExpr) bool {
	name := calleeName(call)
	return strings.HasPrefix(name, "Enqueue") || name == "QueueModelRefresh"
}

// calleeName returns the bare function or method name of call, or "" for other callee shapes.
func calleeName(call *ast.CallExpr) string {
	switch f := call.Fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		return f.Sel.Name
	}
	return ""
}
