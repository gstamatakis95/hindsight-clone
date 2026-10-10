package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// skipDirs are directory paths (slash-separated, relative to a root) the check never enters.
var skipDirs = map[string]bool{
	"gen":            true,
	"internal/mocks": true,
	"tools/mockery":  true,
	"third_party":    true,
	"node_modules":   true,
}

func main() {
	roots := os.Args[1:]
	if len(roots) == 0 {
		roots = []string{"."}
	}
	var findings []string
	for _, root := range roots {
		f, err := check(root)
		if err != nil {
			fmt.Fprintln(os.Stderr, "ifacedoc:", err)
			os.Exit(2)
		}
		findings = append(findings, f...)
	}
	sort.Strings(findings)
	for _, f := range findings {
		fmt.Println(f)
	}
	if len(findings) > 0 {
		fmt.Fprintf(os.Stderr, "ifacedoc: %d finding(s)\n", len(findings))
		os.Exit(1)
	}
}

// check walks root and returns every finding under it.
func check(root string) ([]string, error) {
	var findings []string
	pkgDirs := map[string]bool{}
	//nolint:gosec // G703: roots are the repo dirs `make lint` passes; walking them is the tool's purpose.
	err := filepath.WalkDir(filepath.Clean(root), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDir(root, path, d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, generated, perr := checkFile(path)
		if perr != nil {
			return perr
		}
		if !generated {
			pkgDirs[filepath.Dir(path)] = true
		}
		findings = append(findings, f...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %s: %w", root, err)
	}
	for dir := range pkgDirs {
		findings = append(findings, checkDocFile(dir)...)
	}
	return findings, nil
}

// skipDir reports whether the walk should not descend into path.
func skipDir(root, path, name string) bool {
	if path == root {
		return false
	}
	if strings.HasPrefix(name, ".") || name == "testdata" {
		return true
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return skipDirs[filepath.ToSlash(rel)]
}

// checkFile parses one file and reports interface methods without a leading doc comment.
func checkFile(path string) (findings []string, generated bool, err error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return nil, false, fmt.Errorf("parse %s: %w", path, err)
	}
	if ast.IsGenerated(file) {
		return nil, true, nil
	}
	ast.Inspect(file, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok {
			return true
		}
		it, ok := ts.Type.(*ast.InterfaceType)
		if !ok || it.Methods == nil {
			return true
		}
		for _, m := range it.Methods.List {
			if _, isFunc := m.Type.(*ast.FuncType); !isFunc || len(m.Names) == 0 {
				continue // embedded interface or type constraint
			}
			if m.Doc == nil || strings.TrimSpace(m.Doc.Text()) == "" {
				pos := fset.Position(m.Pos())
				findings = append(findings, fmt.Sprintf("%s:%d: method %s.%s has no doc comment",
					pos.Filename, pos.Line, ts.Name.Name, m.Names[0].Name))
			}
		}
		return true
	})
	return findings, false, nil
}

// checkDocFile reports a package directory without a doc.go carrying the package comment.
func checkDocFile(dir string) []string {
	path := filepath.Join(dir, "doc.go")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments|parser.PackageClauseOnly)
	if err != nil {
		return []string{fmt.Sprintf("%s: package has no parseable doc.go", dir)}
	}
	if file.Doc == nil || strings.TrimSpace(file.Doc.Text()) == "" {
		return []string{fmt.Sprintf("%s: doc.go has no package comment", path)}
	}
	return nil
}
