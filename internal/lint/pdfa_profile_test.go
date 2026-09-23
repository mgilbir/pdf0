package lint

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestPDFAChecksAskTheProfile catches a PDF/A rule that gates on a Level by
// comparing it with a constant.
//
// A pdfa.Level names a whole target profile — part, conformance level and
// PDF/A-4 variant — and a rule almost never means the whole profile. `level ==
// PDFA4` reads as "at part 4" and is false at PDF/A-4e and -4f; `level ==
// PDFA1b` is false at 1a. That is how the variants' own rules came to be
// unreachable once the run flattened PDFA4F to PDFA4, and how the fix for that
// (flattening before the checks) cost them the variant (audit 2026-09-22 C64;
// T5). The checks now ask the profile what they mean — level.Part(),
// level.Conformance(), level.IsA(), the variant — and this keeps them doing it.
//
// Only pdfa/level.go, which defines the profiles, may compare a Level with a
// Level constant, by ==, != or as a switch case.
func TestPDFAChecksAskTheProfile(t *testing.T) {
	m := load(t)
	var found []string
	var seen bool
	for _, p := range m.pkgs {
		if p.path != "github.com/mgilbir/pdf0/pdfa" {
			continue
		}
		seen = true
		level := p.types.Scope().Lookup("Level")
		if level == nil {
			t.Fatal("pdfa.Level not found")
		}
		for _, f := range p.files {
			if filepath.Base(m.fset.Position(f.Pos()).Filename) == "level.go" {
				continue
			}
			found = append(found, levelConstComparisons(m.fset, p.info, level.Type(), f)...)
		}
	}
	if !seen {
		t.Fatal("package pdfa not loaded")
	}
	sort.Strings(found)
	for _, s := range found {
		t.Errorf("%s: a Level compared with a constant; ask the profile (level.Part(), level.Conformance(), level.IsA(), level.variant())", s)
	}
}

// levelConstComparisons returns "file:line" for every ==, != or switch case
// comparing a value of type level with a constant of that type.
func levelConstComparisons(fset *token.FileSet, info *types.Info, level types.Type, f *ast.File) []string {
	isLevel := func(e ast.Expr) bool {
		tv, ok := info.Types[e]
		return ok && types.Identical(tv.Type, level)
	}
	isConst := func(e ast.Expr) bool {
		tv, ok := info.Types[e]
		return ok && tv.Value != nil && types.Identical(tv.Type, level)
	}
	var out []string
	at := func(n ast.Node) {
		pos := fset.Position(n.Pos())
		out = append(out, fmt.Sprintf("%s:%d", filepath.Base(pos.Filename), pos.Line))
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.BinaryExpr:
			if (s.Op == token.EQL || s.Op == token.NEQ) && isLevel(s.X) && isLevel(s.Y) && (isConst(s.X) || isConst(s.Y)) {
				at(s)
			}
		case *ast.SwitchStmt:
			if s.Tag == nil || !isLevel(s.Tag) {
				return true
			}
			for _, c := range s.Body.List {
				for _, e := range c.(*ast.CaseClause).List {
					if isConst(e) {
						at(e)
					}
				}
			}
		}
		return true
	})
	return out
}

// TestPDFAChecksAskTheProfileDetects is the check's planted bug.
func TestPDFAChecksAskTheProfileDetects(t *testing.T) {
	const src = `package snippet

type Level int

const (
	A Level = iota
	B
)

func (l Level) Part() int { return int(l) }

func gates(level Level, other Level) {
	_ = level == B
	_ = A != level
	switch level {
	case A, B:
	}
	_ = level.Part() == 1
	_ = level == other
	switch level.Part() {
	case 1:
	}
}
`
	fset := token.NewFileSet()
	f, info, pkg := typeCheckSnippet(t, fset, src)
	got := levelConstComparisons(fset, info, pkg.Scope().Lookup("Level").Type(), f)
	want := []string{"snippet.go:13", "snippet.go:14", "snippet.go:16", "snippet.go:16"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("got %v, want %v", got, want)
	}
}

// typeCheckSnippet type-checks a self-contained source file.
func typeCheckSnippet(t *testing.T, fset *token.FileSet, src string) (*ast.File, *types.Info, *types.Package) {
	t.Helper()
	f, err := parser.ParseFile(fset, "snippet.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}, Uses: map[*ast.Ident]types.Object{}, Defs: map[*ast.Ident]types.Object{}}
	pkg, err := (&types.Config{}).Check("example/snippet", fset, []*ast.File{f}, info)
	if err != nil {
		t.Fatal(err)
	}
	return f, info, pkg
}
