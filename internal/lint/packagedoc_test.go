package lint

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// TestEveryPublicPackageHasADoc requires a package doc comment — a comment
// directly above the package clause, beginning "Package <name> " — on every
// package a caller can import: not an internal package, and not a command.
// It also requires that only one file carries it, because go/doc joins every
// file's package comment into one, in file-name order, and a second one
// reads as a non sequitur in the middle of the first.
//
// Incident, docs audit 2026-10-11 D9: ten public packages (pdfa, pdfua, pdfx,
// pdfvt, pdfr, dpart, facturx, images, sign and syntax) had no package doc.
// Each had a good overview, written as a "// This file …" comment after the
// package clause, which godoc never shows, so pkg.go.dev opened each of them
// on a constant.
func TestEveryPublicPackageHasADoc(t *testing.T) {
	m := load(t)
	checked := 0
	for _, p := range m.pkgs {
		if isInternalPath(p.path) || p.types.Name() == "main" {
			continue
		}
		checked++
		for _, msg := range packageDocProblems(m.fset, p.types.Name(), p.files) {
			t.Errorf("package %s: %s", p.path, msg)
		}
	}
	if checked < 10 {
		t.Fatalf("checked %d public packages; the scan is looking in the wrong place", checked)
	}
}

// packageDocProblems reports what is wrong with the package doc comment of
// the package name made of files, or nothing.
func packageDocProblems(fset *token.FileSet, name string, files []*ast.File) []string {
	var docs []string
	var problems []string
	for _, f := range files {
		if f.Doc == nil {
			continue
		}
		where := filepath.Base(fset.Position(f.Package).Filename)
		docs = append(docs, where)
		if want := "Package " + name + " "; !strings.HasPrefix(f.Doc.Text(), want) {
			problems = append(problems, fmt.Sprintf("%s: the package comment does not begin %q", where, want))
		}
	}
	switch {
	case len(docs) == 0:
		problems = append(problems, `no package doc comment; write one ("// Package `+name+` …" directly above the package clause, conventionally in doc.go)`)
	case len(docs) > 1:
		problems = append(problems, fmt.Sprintf("package doc comments in %d files (%s); go/doc joins them, so keep one", len(docs), strings.Join(docs, ", ")))
	}
	return problems
}

// TestPackageDocProblemsDetects is the check's planted bugs: a package with
// no doc, one whose overview is detached below the package clause (the D9
// shape), one with two, and one whose comment does not name the package.
func TestPackageDocProblemsDetects(t *testing.T) {
	cases := []struct {
		name  string
		files []string
		want  string // a substring of the one expected problem, or "" for none
	}{
		{"documented", []string{"// Package p does a thing.\npackage p\n", "package p\n"}, ""},
		{"none", []string{"package p\n\nconst X = 1\n"}, "no package doc comment"},
		{"detached", []string{"package p\n\n// This file implements p, which does a thing.\n\nconst X = 1\n"}, "no package doc comment"},
		{"separated by a blank line", []string{"// Package p does a thing.\n\npackage p\n"}, "no package doc comment"},
		{"two", []string{"// Package p does a thing.\npackage p\n", "// Package p does another.\npackage p\n"}, "package doc comments in 2 files"},
		{"misnamed", []string{"// Package q does a thing.\npackage p\n"}, `does not begin "Package p "`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fset := token.NewFileSet()
			var files []*ast.File
			for i, src := range c.files {
				f, err := parser.ParseFile(fset, fmt.Sprintf("f%d.go", i), src, parser.ParseComments)
				if err != nil {
					t.Fatal(err)
				}
				files = append(files, f)
			}
			got := packageDocProblems(fset, "p", files)
			switch {
			case c.want == "" && len(got) != 0:
				t.Errorf("want no problem, got %q", got)
			case c.want != "" && (len(got) != 1 || !strings.Contains(got[0], c.want)):
				t.Errorf("want one problem containing %q, got %q", c.want, got)
			}
		})
	}
}
