package gocurlffi

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The facade is a list of aliases with no code of its own, so its one failure
// mode is falling behind the package it aliases: a type or option added to
// requests and not re-exported here is invisible to a caller using the dotted
// form, and nothing else would notice.
//
// This compares the two sets of exported names directly from the source, which
// catches both a missing alias and a stale one.
func TestFacadeCoversRequests(t *testing.T) {
	underlying := exportedNames(t, filepath.Join("requests"))
	facade := exportedNames(t, ".")

	// A couple of names belong to the aliased package but make no sense
	// unqualified, so they are deliberately absent.
	skip := map[string]bool{}

	var missing []string
	for name := range underlying {
		if skip[name] {
			continue
		}
		if !facade[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("these exported names exist in requests but are not aliased in gocurlffi.go: %v\n"+
			"add them, or add them to skip with a reason", missing)
	}

	var stale []string
	for name := range facade {
		if !underlying[name] {
			stale = append(stale, name)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf("these names are aliased here but no longer exist in requests: %v", stale)
	}
}

// exportedNames returns the exported top-level type, func, var and const names
// declared in the non-test Go files of a package directory.
func exportedNames(t *testing.T, dir string) map[string]bool {
	t.Helper()

	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	fset := token.NewFileSet()

	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				// Methods travel with their type, so only package-level
				// functions need an alias.
				if d.Recv == nil && d.Name.IsExported() {
					out[d.Name.Name] = true
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						if s.Name.IsExported() {
							out[s.Name.Name] = true
						}
					case *ast.ValueSpec:
						for _, n := range s.Names {
							if n.IsExported() {
								out[n.Name] = true
							}
						}
					}
				}
			}
		}
	}
	return out
}

// The facade must be aliases, not copies. A type alias and a defined type are
// one character apart and behave differently: a defined type would not accept
// or return the values the rest of the library does.
func TestFacadeUsesAliasesNotNewTypes(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "gocurlffi.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			if !ts.Assign.IsValid() {
				t.Errorf("%s is a defined type; it must be an alias (`%s = ...`)",
					ts.Name.Name, ts.Name.Name)
			}
			n++
		}
	}
	if n == 0 {
		t.Fatal("no type declarations found in the facade")
	}
}
