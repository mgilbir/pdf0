package lint

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestResolveBeforeAssert catches a dictionary value type-asserted without
// being resolved first. Almost any value in a PDF dictionary may be written as
// an indirect reference — `/N 7 0 R` is as legal as `/N 3` — so
// `d.Get("N").(object.Integer)` is false for a file the specification permits,
// and the check reading it silently sees "absent". It has shipped repeatedly:
// an ICCBased profile whose /N was indirect lost its transparency-group
// coverage and produced a device-colour false positive (audit 2026-09-22
// C152), and the PDF/A rule inputs of C36 are the same bug at dozens of sites.
//
// The check asks the type checker, so it sees the shape however it is spelt:
// a type assertion or type switch whose operand is a call of
// (*object.Dictionary).Get, or a local variable every assignment to which is
// such a call. Asserting object.IndirectRef, or a type switch with an
// IndirectRef case, is exempt: asking whether a value is a reference is the
// one question that must not resolve it.
//
// resolveAssertAllowlist names the sites that predate the check, each with
// the finding that owns it. A site is identified by package, file, enclosing
// function and dictionary key rather than by line, so an unrelated edit does
// not disturb the list. The test fails for a site that is not listed, and for
// a listed site that has gone, so the list can only shrink.
func TestResolveBeforeAssert(t *testing.T) {
	m := load(t)
	got := map[string]int{}
	for _, p := range m.pkgs {
		if !resolveAssertScope[p.path] {
			continue
		}
		for _, f := range p.files {
			for _, site := range resolveAssertSites(m.fset, p, f) {
				got[site]++
			}
		}
	}
	for _, msg := range compareAllowlist(got, resolveAssertAllowlist) {
		t.Error(msg)
	}
}

// compareAllowlist reports every site found more often than the allowlist
// permits, and every allowlisted site found less often than listed.
func compareAllowlist(got, allow map[string]int) []string {
	var out []string
	for site, n := range got {
		if n > allow[site] {
			out = append(out, fmt.Sprintf("%s: %d dictionary value(s) type-asserted without being resolved (allowlisted: %d); resolve first with View.Resolve, ResolveInt, ResolveName, ResolveDict", site, n, allow[site]))
		}
	}
	for site, want := range allow {
		if got[site] < want {
			out = append(out, fmt.Sprintf("%s: allowlisted for %d unresolved assert(s), found %d; shrink resolveAssertAllowlist", site, want, got[site]))
		}
	}
	sort.Strings(out)
	return out
}

// resolveAssertScope is the packages the check covers: the shared engine and
// the PDF/A validator, where a value read as absent becomes a verdict.
var resolveAssertScope = map[string]bool{
	"github.com/mgilbir/pdf0/internal/core": true,
	"github.com/mgilbir/pdf0/pdfa":          true,
}

// resolveAssertSites returns the unresolved-assert sites in one file, as
// "pkg/file.go:Func:Key" strings, one per assert.
func resolveAssertSites(fset *token.FileSet, p *checkedPackage, f *ast.File) []string {
	file := filepath.Base(fset.Position(f.Pos()).Filename)
	pkg := p.path[strings.LastIndex(p.path, "/")+1:]
	var out []string
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		name := fn.Name.Name
		if fn.Recv != nil && len(fn.Recv.List) > 0 {
			name = recvName(fn.Recv.List[0].Type) + "." + name
		}
		site := func(key string) string { return fmt.Sprintf("%s/%s:%s:%s", pkg, file, name, key) }

		// Local variables whose every assignment is a Dictionary.Get call,
		// with the key of the first such call.
		fromGet := map[types.Object]string{}
		other := map[types.Object]bool{}
		note := func(id *ast.Ident, rhs ast.Expr) {
			obj := p.info.Defs[id]
			if obj == nil {
				obj = p.info.Uses[id]
			}
			if obj == nil {
				return
			}
			if rhs != nil {
				if key, ok := dictGetCall(p, rhs); ok {
					if _, seen := fromGet[obj]; !seen {
						fromGet[obj] = key
					}
					return
				}
			}
			other[obj] = true
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch s := n.(type) {
			case *ast.AssignStmt:
				for i, l := range s.Lhs {
					id, ok := l.(*ast.Ident)
					if !ok {
						continue
					}
					if len(s.Lhs) == len(s.Rhs) {
						note(id, s.Rhs[i])
					} else {
						note(id, nil)
					}
				}
			case *ast.ValueSpec:
				for i, id := range s.Names {
					var rhs ast.Expr
					if len(s.Values) == len(s.Names) {
						rhs = s.Values[i]
					}
					note(id, rhs)
				}
			case *ast.RangeStmt:
				for _, e := range []ast.Expr{s.Key, s.Value} {
					if id, ok := e.(*ast.Ident); ok {
						note(id, nil)
					}
				}
			}
			return true
		})
		// A type switch with an object.IndirectRef case is asking whether the
		// value is a reference, like an assertion to IndirectRef: exempt.
		refSwitch := map[*ast.TypeAssertExpr]bool{}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			ts, ok := n.(*ast.TypeSwitchStmt)
			if !ok {
				return true
			}
			var ta *ast.TypeAssertExpr
			switch a := ts.Assign.(type) {
			case *ast.AssignStmt:
				if len(a.Rhs) == 1 {
					ta, _ = ast.Unparen(a.Rhs[0]).(*ast.TypeAssertExpr)
				}
			case *ast.ExprStmt:
				ta, _ = ast.Unparen(a.X).(*ast.TypeAssertExpr)
			}
			if ta == nil {
				return true
			}
			for _, st := range ts.Body.List {
				for _, e := range st.(*ast.CaseClause).List {
					if isObjectTypeExpr(p, e, "IndirectRef") {
						refSwitch[ta] = true
					}
				}
			}
			return true
		})
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			ta, ok := n.(*ast.TypeAssertExpr)
			if !ok {
				return true
			}
			if (ta.Type != nil && isObjectTypeExpr(p, ta.Type, "IndirectRef")) || refSwitch[ta] {
				return true
			}
			x := ast.Unparen(ta.X)
			if key, ok := dictGetCall(p, x); ok {
				out = append(out, site(key))
				return true
			}
			if id, ok := x.(*ast.Ident); ok {
				if obj := p.info.Uses[id]; obj != nil {
					if key, ok := fromGet[obj]; ok && !other[obj] {
						out = append(out, site(key))
					}
				}
			}
			return true
		})
	}
	return out
}

// dictGetCall reports whether e is a call of (*object.Dictionary).Get, and
// the key it passes: the constant, or "?" when the key is computed.
func dictGetCall(p *checkedPackage, e ast.Expr) (string, bool) {
	call, ok := ast.Unparen(e).(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return "", false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Get" {
		return "", false
	}
	tv, ok := p.info.Types[sel.X]
	if !ok || !isObjectType(tv.Type, "Dictionary") {
		return "", false
	}
	key := "?"
	if kv, ok := p.info.Types[call.Args[0]]; ok && kv.Value != nil && kv.Value.Kind() == constant.String {
		key = constant.StringVal(kv.Value)
	}
	return key, true
}

func isObjectTypeExpr(p *checkedPackage, e ast.Expr, name string) bool {
	tv, ok := p.info.Types[e]
	return ok && isObjectType(tv.Type, name)
}

// isObjectType reports whether t is object.<name> or a pointer to it.
func isObjectType(t types.Type, name string) bool {
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	n, ok := t.(*types.Named)
	if !ok {
		return false
	}
	obj := n.Obj()
	return obj.Name() == name && obj.Pkg() != nil && obj.Pkg().Path() == "github.com/mgilbir/pdf0/object"
}

func recvName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return recvName(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr:
		return recvName(t.X)
	}
	return "?"
}

// resolveAssertAllowlist: site -> number of unresolved asserts at it. Every
// entry predates the check and belongs to a finding that is fixed elsewhere
// in the remediation stack; burn it down.
var resolveAssertAllowlist = map[string]int{
	// Decode parameters: the filter chain refuses a stream whose /Filter or
	// /DecodeParms holds any indirect reference (filterEntriesIndirect), so
	// these read values already known to be direct (audit 2026-09-22 C151).
	"core/filters.go:ApplyFilter:EarlyChange": 1,
	"core/filters.go:PredictorFromDict:?":     1,

	// A trailer parsed from raw bytes, with no object table behind it: the
	// byte-level checks move onto the source record in PR 14 (C69).
	"pdfa/filestructure.go:collectTrailerIDFirstElements:ID": 1,

	// PDF/A rule inputs: audit 2026-09-22 C36, fixed with the target profile
	// (PR 13).
	"pdfa/pdfa.go:checkCatalogVersion:Version":          1,
	"pdfa/pdfa.go:checkEmbeddedFileSpecs:Subtype":       1,
	"pdfa/pdfa.go:checkExtGState:BM":                    1,
	"pdfa/pdfa.go:checkExtGState:TR2":                   1,
	"pdfa/pdfa.go:checkFileID:ID":                       1,
	"pdfa/pdfa.go:checkHalftoneErrors:HalftoneType":     1,
	"pdfa/pdfa.go:checkICCBasedProfiles:N":              1,
	"pdfa/pdfa.go:checkNamedActions:A":                  1,
	"pdfa/pdfa.go:checkNeedAppearances:NeedAppearances": 1,
	"pdfa/pdfa.go:checkNoForbiddenActions:A":            1,
	"pdfa/pdfa.go:checkNoTransparency:?":                1,
	"pdfa/pdfa.go:checkNoTransparency:BM":               1,
	"pdfa/pdfa.go:checkNoTransparency:SMask":            1,
	"pdfa/pdfa.go:checkOutputIntentProfile:N":           1,
	"pdfa/pdfa.go:checkOutputIntents:S":                 1,
	"pdfa/pdfa.go:checkSeparationDeviceN:Resources":     2,
	"pdfa/pdfa.go:find1bTransparencyXObjects:SMask":     1,
	"pdfa/pdfa.go:getNonStandardFilter:Filter":          2,
	"pdfa/pdfa.go:hasFilter:Filter":                     2,
}

// TestResolveBeforeAssertDetects is the check's own planted bug: a snippet
// with every shape the check claims to see, and the shapes it must leave
// alone, type-checked against the real object package.
func TestResolveBeforeAssertDetects(t *testing.T) {
	const src = `package snippet

import "github.com/mgilbir/pdf0/object"

func resolve(o object.Object) object.Object { return o }

func Direct(d *object.Dictionary) {
	_, _ = d.Get("N").(object.Integer)
	switch d.Get("F").(type) {
	case object.Name:
	}
	_ = (d.Get("P")).(object.Array)
}

func ViaVar(d *object.Dictionary) {
	n := d.Get("N")
	if n != nil {
		_, _ = n.(object.Integer)
	}
	var m = d.Get("M")
	_, _ = m.(object.Name)
}

func Clean(d *object.Dictionary) {
	_, _ = resolve(d.Get("N")).(object.Integer)
	_, _ = d.Get("R").(object.IndirectRef)
	switch d.Get("S").(type) {
	case object.IndirectRef, object.Name:
	}
	x := d.Get("X")
	x = resolve(x)
	_, _ = x.(object.Name)
}
`
	exports, err := goList("-export", "-deps", "-f", "{{.ImportPath}}\t{{.Export}}", "github.com/mgilbir/pdf0/object")
	if err != nil {
		t.Fatal(err)
	}
	exportFile := map[string]string{}
	for _, line := range exports {
		path, file, _ := strings.Cut(line, "\t")
		exportFile[path] = file
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "snippet.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	p := &checkedPackage{path: "example/snippet", files: []*ast.File{f}}
	p.info = &types.Info{Types: map[ast.Expr]types.TypeAndValue{}, Uses: map[*ast.Ident]types.Object{}, Defs: map[*ast.Ident]types.Object{}}
	imp := importer.ForCompiler(fset, "gc", func(path string) (io.ReadCloser, error) {
		return os.Open(exportFile[path])
	})
	if p.types, err = (&types.Config{Importer: imp}).Check(p.path, fset, p.files, p.info); err != nil {
		t.Fatal(err)
	}
	got := resolveAssertSites(fset, p, f)
	sort.Strings(got)
	want := []string{
		"snippet/snippet.go:Direct:F",
		"snippet/snippet.go:Direct:N",
		"snippet/snippet.go:Direct:P",
		"snippet/snippet.go:ViaVar:M",
		"snippet/snippet.go:ViaVar:N",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("sites:\n got %v\nwant %v", got, want)
	}
	if msgs := compareAllowlist(map[string]int{"a": 2, "b": 1}, map[string]int{"a": 1, "b": 1, "c": 1}); len(msgs) != 2 {
		t.Errorf("compareAllowlist reported %d problems, want 2 (a over its allowance, c gone): %v", len(msgs), msgs)
	}
}
