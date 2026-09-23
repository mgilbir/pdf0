package lint

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"sort"
	"strings"
	"testing"
)

// documentRulePackages are the packages whose rules judge a document: the
// validators, and facturx, whose container rules do too.
var documentRulePackages = map[string]bool{
	"github.com/mgilbir/pdf0/pdfa":    true,
	"github.com/mgilbir/pdf0/pdfua":   true,
	"github.com/mgilbir/pdf0/pdfx":    true,
	"github.com/mgilbir/pdf0/pdfvt":   true,
	"github.com/mgilbir/pdf0/pdfr":    true,
	"github.com/mgilbir/pdf0/dpart":   true,
	"github.com/mgilbir/pdf0/facturx": true,
}

// TestValidatorsWalkTheReachableGraph rejects, in the validator packages, a
// range over the object table — a map[int]*object.IndirectObject, which is
// what View.Objects and Document.Objects are.
//
// Incident, audit 2026-09-22 C83: rules that asked "does the document do X?"
// answered by ranging over the object table, which is the wrong set twice
// over. A dictionary written directly inside another object is not in the
// table, so a Link's inline /A << /S /JavaScript >> — the shape most producers
// write — passed PDF/X's prohibition on JavaScript; and an object nothing
// refers to is in the table, so an orphan accused the document of something it
// does not contain. The previous audit's fix (a second pass for the direct
// annotations of page /Annots arrays) closed one shape at a few sites and left
// every other rule and every other direct dictionary open.
//
// Walk View.ReachableDicts (or View.ReachableObjectNums for a rule that
// descends each object itself). A rule about the file's syntax — what every
// indirect object in the file must look like, used or not — does range over
// the table, and says so with a `// allobjects: <reason>` comment on the range
// line or the line above.
func TestValidatorsWalkTheReachableGraph(t *testing.T) {
	m := load(t)
	var found []string
	for _, p := range m.pkgs {
		if !documentRulePackages[p.path] {
			continue
		}
		for _, f := range p.files {
			found = append(found, objectTableRanges(m.fset, p, f)...)
		}
	}
	sort.Strings(found)
	for _, f := range found {
		t.Error(f)
	}
}

// objectTableRanges returns the ranges over an object table in f that carry
// no allobjects comment.
func objectTableRanges(fset *token.FileSet, p *checkedPackage, f *ast.File) []string {
	// A comment group that opens with "allobjects:" exempts the line it ends
	// on (a trailing comment) and so the line after it (a comment above).
	exempt := map[int]bool{}
	for _, cg := range f.Comments {
		if strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(cg.List[0].Text, "//")), "allobjects:") {
			exempt[fset.Position(cg.End()).Line] = true
		}
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		rs, ok := n.(*ast.RangeStmt)
		if !ok {
			return true
		}
		tv, ok := p.info.Types[rs.X]
		if !ok || !isObjectTable(tv.Type) {
			return true
		}
		line := fset.Position(rs.Pos()).Line
		if exempt[line] || exempt[line-1] {
			return true
		}
		out = append(out, fset.Position(rs.Pos()).String()+": a rule ranges over the object table, "+
			"which misses direct dictionaries and judges orphans; walk View.ReachableDicts, "+
			"or mark a file-syntax rule `// allobjects: <reason>`")
		return true
	})
	return out
}

// isObjectTable reports whether t is map[int]*object.IndirectObject.
func isObjectTable(t types.Type) bool {
	mt, ok := t.Underlying().(*types.Map)
	if !ok {
		return false
	}
	if b, ok := mt.Key().Underlying().(*types.Basic); !ok || b.Kind() != types.Int {
		return false
	}
	ptr, ok := mt.Elem().(*types.Pointer)
	if !ok {
		return false
	}
	named, ok := ptr.Elem().(*types.Named)
	return ok && named.Obj().Name() == "IndirectObject" && named.Obj().Pkg() != nil &&
		named.Obj().Pkg().Path() == "github.com/mgilbir/pdf0/object"
}

// typeCheckModuleSnippet type-checks one source file against the module's real
// packages (their export data), for a check's planted-bug test.
func typeCheckModuleSnippet(t *testing.T, src string, imports ...string) (*token.FileSet, *checkedPackage, *ast.File) {
	t.Helper()
	exports, err := goList(append([]string{"-export", "-deps", "-f", "{{.ImportPath}}\t{{.Export}}"}, imports...)...)
	if err != nil {
		t.Fatal(err)
	}
	exportFile := map[string]string{}
	for _, line := range exports {
		path, file, _ := strings.Cut(line, "\t")
		exportFile[path] = file
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "snippet.go", src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	p := &checkedPackage{path: "example/snippet", files: []*ast.File{f}}
	p.info = &types.Info{Types: map[ast.Expr]types.TypeAndValue{}, Uses: map[*ast.Ident]types.Object{},
		Defs: map[*ast.Ident]types.Object{}, Selections: map[*ast.SelectorExpr]*types.Selection{}}
	imp := importer.ForCompiler(fset, "gc", func(path string) (io.ReadCloser, error) {
		return os.Open(exportFile[path])
	})
	if p.types, err = (&types.Config{Importer: imp}).Check(p.path, fset, p.files, p.info); err != nil {
		t.Fatal(err)
	}
	return fset, p, f
}

// TestValidatorsWalkTheReachableGraphDetects is the check's planted bug: the
// shapes it must flag and the ones it must leave alone, type-checked against
// the real packages.
func TestValidatorsWalkTheReachableGraphDetects(t *testing.T) {
	const src = `package snippet

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

func Rule(d core.View) {
	for num, iobj := range d.Objects {
		_, _ = num, iobj
	}
	for num := range d.Objects {
		_ = num
	}
	table := map[int]*object.IndirectObject{}
	for range table {
	}
}

func Clean(d core.View, other map[int]bool) {
	// allobjects: a syntax rule, required of every object in the file
	for num := range d.Objects {
		_ = num
	}
	for num := range d.Objects { // allobjects: likewise, on the line itself
		_ = num
	}
	for _, r := range d.ReachableDicts() {
		_ = r
	}
	for k := range other {
		_ = k
	}
}
`
	fset, p, f := typeCheckModuleSnippet(t, src, "github.com/mgilbir/pdf0/internal/core", "github.com/mgilbir/pdf0/object")
	var got []string
	for _, s := range objectTableRanges(fset, p, f) {
		got = append(got, s[:strings.Index(s, ": ")])
	}
	want := []string{"snippet.go:9:2", "snippet.go:12:2", "snippet.go:16:2"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("flagged %v, want %v", got, want)
	}
}
