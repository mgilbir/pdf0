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

// TestResolveBeforeAssert catches a PDF value whose type is asked before it is
// resolved. Almost any value in a PDF dictionary or array may be written as an
// indirect reference — `/N 7 0 R` is as legal as `/N 3` — so
// `d.Get("N").(object.Integer)` is false for a file the specification permits,
// and the check reading it silently sees "absent". In a validator that is two
// bugs at once: a requirement on the value reads as unmet (a false positive —
// an indirect /Type /Metadata was "not /Metadata") and a prohibition on it as
// not triggered (an evasion — an indirect /Filter /LZWDecode, /BM /Bogus or
// NeedAppearances true went unreported). It has shipped repeatedly: an
// ICCBased profile whose /N was indirect lost its transparency-group coverage
// and produced a device-colour false positive (audit 2026-09-22 C152), and the
// previous audit fixed the PDF/A sites it named (C18) while the shape stayed
// at twenty more (C36).
//
// The check asks the type checker, so it sees the shape however it is spelt.
// A type assertion or type switch is flagged — and so is an == or !=
// comparison, or an expression switch, against anything but nil, which is a
// type assertion in disguise — when its operand is
//   - a call of (*object.Dictionary).Get, or the value of Lookup,
//   - an element of an object.Array (an index expression, or the value of a
//     range over one),
//   - the value of a range over a Dictionary's All or Values,
//   - a parameter of type object.Object (callers hand dictionary values
//     straight through), or
//   - a local variable every assignment to which is one of the above.
//
// Asserting object.IndirectRef is exempt: asking whether a value is a
// reference is the one question that must not resolve it. So is a type switch
// with an object.IndirectRef case, which handles references itself — the
// structural walks that visit each indirect object on their own say so that
// way.
//
// resolveAssertAllowlist names the sites that predate the check, each with
// the finding that owns it. A site is identified by package, file, enclosing
// function and what was asserted (a dictionary key, "[]" for an array
// element, "Lookup", "All", "Values" or "param:<name>") rather than by line,
// so an unrelated edit does not disturb the list. The test fails for a site
// that is not listed, and for a listed site that has gone, so the list can
// only shrink.
func TestResolveBeforeAssert(t *testing.T) {
	m := load(t)
	got := map[string]int{}
	seen := 0
	for _, p := range m.pkgs {
		if !resolveAssertScope[p.path] {
			continue
		}
		seen++
		for _, f := range p.files {
			for _, site := range resolveAssertSites(m.fset, p, f) {
				got[site]++
			}
		}
	}
	if seen != len(resolveAssertScope) {
		t.Fatalf("found %d of the %d packages in scope; a package moved and the check no longer sees it", seen, len(resolveAssertScope))
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
			out = append(out, fmt.Sprintf("%s: %d value(s) type-asserted or compared without being resolved (allowlisted: %d); resolve first with View.Resolve, ResolveInt, ResolveName, ResolveNumber, ResolveDict", site, n, allow[site]))
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

// validatorPackages are the packages whose checks turn what they read into a
// verdict, or the lack of one.
var validatorPackages = map[string]bool{
	"github.com/mgilbir/pdf0/pdfa":  true,
	"github.com/mgilbir/pdf0/pdfua": true,
	"github.com/mgilbir/pdf0/pdfx":  true,
	"github.com/mgilbir/pdf0/pdfvt": true,
	"github.com/mgilbir/pdf0/pdfr":  true,
	"github.com/mgilbir/pdf0/dpart": true,
}

// resolveAssertScope is the packages the check covers: the shared engine and
// every validator.
var resolveAssertScope = func() map[string]bool {
	m := map[string]bool{"github.com/mgilbir/pdf0/internal/core": true}
	for p := range validatorPackages {
		m[p] = true
	}
	return m
}()

// resolveAssertSites returns the unresolved-assert sites in one file, as
// "pkg/file.go:Func:what" strings, one per assert.
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
		site := func(what string) string { return fmt.Sprintf("%s/%s:%s:%s", pkg, file, name, what) }

		// Local variables whose every assignment is an unresolved value, with
		// what the first such value was.
		unresolved := map[types.Object]string{}
		other := map[types.Object]bool{}
		for _, field := range fn.Type.Params.List {
			for _, id := range field.Names {
				if obj := p.info.Defs[id]; obj != nil && isObjectType(obj.Type(), "Object") {
					unresolved[obj] = "param:" + id.Name
				}
			}
		}
		note := func(id *ast.Ident, what string) {
			obj := p.info.Defs[id]
			if obj == nil {
				obj = p.info.Uses[id]
			}
			if obj == nil {
				return
			}
			if what == "" {
				other[obj] = true
				return
			}
			if _, ok := unresolved[obj]; !ok {
				unresolved[obj] = what
			}
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch s := n.(type) {
			case *ast.AssignStmt:
				switch {
				case len(s.Lhs) == len(s.Rhs):
					for i, l := range s.Lhs {
						if id, ok := l.(*ast.Ident); ok {
							note(id, unresolvedValue(p, s.Rhs[i]))
						}
					}
				case len(s.Rhs) == 1 && isDictMethodCall(p, s.Rhs[0], "Lookup"):
					// v, ok := d.Lookup(k): v is the unresolved value.
					if id, ok := s.Lhs[0].(*ast.Ident); ok {
						note(id, "Lookup")
					}
					for _, l := range s.Lhs[1:] {
						if id, ok := l.(*ast.Ident); ok {
							note(id, "")
						}
					}
				default:
					for _, l := range s.Lhs {
						if id, ok := l.(*ast.Ident); ok {
							note(id, "")
						}
					}
				}
			case *ast.ValueSpec:
				for i, id := range s.Names {
					what := ""
					if len(s.Values) == len(s.Names) {
						what = unresolvedValue(p, s.Values[i])
					}
					note(id, what)
				}
			case *ast.RangeStmt:
				// Over an Array or Dictionary.All the value is the second
				// variable; over Dictionary.Values, an iter.Seq, the first.
				what, first := rangeValue(p, s.X)
				keyWhat, valWhat := "", what
				if first {
					keyWhat, valWhat = what, ""
				}
				if id, ok := s.Key.(*ast.Ident); ok {
					note(id, keyWhat)
				}
				if id, ok := s.Value.(*ast.Ident); ok {
					note(id, valWhat)
				}
			}
			return true
		})
		describe := func(x ast.Expr) string {
			x = ast.Unparen(x)
			if what := unresolvedValue(p, x); what != "" {
				return what
			}
			if id, ok := x.(*ast.Ident); ok {
				if obj := p.info.Uses[id]; obj != nil && !other[obj] {
					return unresolved[obj]
				}
			}
			return ""
		}
		isNil := func(e ast.Expr) bool {
			tv, ok := p.info.Types[ast.Unparen(e)]
			return ok && tv.IsNil()
		}
		// A type switch with an object.IndirectRef case is asking whether the
		// value is a reference, like an assertion to IndirectRef: exempt.
		refSwitch := map[*ast.TypeAssertExpr]bool{}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch s := n.(type) {
			case *ast.TypeSwitchStmt:
				var ta *ast.TypeAssertExpr
				switch a := s.Assign.(type) {
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
				for _, st := range s.Body.List {
					for _, e := range st.(*ast.CaseClause).List {
						if isObjectTypeExpr(p, e, "IndirectRef") {
							refSwitch[ta] = true
						}
					}
				}
			case *ast.TypeAssertExpr:
				if (s.Type != nil && isObjectTypeExpr(p, s.Type, "IndirectRef")) || refSwitch[s] {
					return true
				}
				if what := describe(s.X); what != "" {
					out = append(out, site(what))
				}
			case *ast.BinaryExpr:
				// v == object.Name("X") is a type assertion in disguise: an
				// indirect v is never equal to it. Comparing with nil asks
				// whether the key is present, which needs no resolving.
				if s.Op != token.EQL && s.Op != token.NEQ {
					return true
				}
				for _, pair := range [][2]ast.Expr{{s.X, s.Y}, {s.Y, s.X}} {
					if isNil(pair[1]) {
						continue
					}
					if what := describe(pair[0]); what != "" {
						out = append(out, site(what))
						break
					}
				}
			case *ast.SwitchStmt:
				// switch v { case object.Name("X"): } is the same comparison.
				if s.Tag == nil {
					return true
				}
				what := describe(s.Tag)
				if what == "" {
					return true
				}
				for _, c := range s.Body.List {
					for _, e := range c.(*ast.CaseClause).List {
						if !isNil(e) {
							out = append(out, site(what))
							return true
						}
					}
				}
			}
			return true
		})
	}
	return out
}

// unresolvedValue says what e is when it is a value read straight out of a
// dictionary or array — the dictionary key of a Get call ("?" when computed),
// or "[]" for an array element — and returns "" otherwise.
func unresolvedValue(p *checkedPackage, e ast.Expr) string {
	e = ast.Unparen(e)
	if key, ok := dictGetCall(p, e); ok {
		return key
	}
	if ix, ok := e.(*ast.IndexExpr); ok && isObjectTypeExpr(p, ix.X, "Array") {
		return "[]"
	}
	return ""
}

// dictGetCall reports whether e is a call of (*object.Dictionary).Get, and
// the key it passes: the constant, or "?" when the key is computed.
func dictGetCall(p *checkedPackage, e ast.Expr) (string, bool) {
	call, ok := ast.Unparen(e).(*ast.CallExpr)
	if !ok || len(call.Args) != 1 || !isDictMethodCall(p, call, "Get") {
		return "", false
	}
	key := "?"
	if kv, ok := p.info.Types[call.Args[0]]; ok && kv.Value != nil && kv.Value.Kind() == constant.String {
		key = constant.StringVal(kv.Value)
	}
	return key, true
}

// isDictMethodCall reports whether e is a call of (*object.Dictionary).<method>.
func isDictMethodCall(p *checkedPackage, e ast.Expr, method string) bool {
	call, ok := ast.Unparen(e).(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == method && isObjectTypeExpr(p, sel.X, "Dictionary")
}

// rangeValue says what the values a range over x yield are when they are
// unresolved — "[]" over an object.Array, "All" or "Values" over a
// Dictionary's iterators — and whether they arrive in the first range
// variable (Values, an iter.Seq) rather than the second.
func rangeValue(p *checkedPackage, x ast.Expr) (what string, first bool) {
	x = ast.Unparen(x)
	if isObjectTypeExpr(p, x, "Array") {
		return "[]", false
	}
	for _, m := range []string{"All", "Values"} {
		if isDictMethodCall(p, x, m) {
			return m, m == "Values"
		}
	}
	return "", false
}

func isObjectTypeExpr(p *checkedPackage, e ast.Expr, name string) bool {
	tv, ok := p.info.Types[e]
	return ok && isObjectType(tv.Type, name)
}

// isObjectType reports whether t is object.<name> or a pointer to it.
func isObjectType(t types.Type, name string) bool {
	if t == nil {
		return false
	}
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
	l, ok := d.Lookup("L")
	if ok {
		_, _ = l.(object.Name)
	}
}

func Arrays(a object.Array, d *object.Dictionary) {
	_, _ = a[0].(object.Name)
	for _, e := range a {
		_, _ = e.(object.Integer)
	}
	for _, v := range d.All() {
		_, _ = v.(object.Name)
	}
	for v := range d.Values() {
		_, _ = v.(object.Real)
	}
}

func Param(o object.Object) {
	switch o.(type) {
	case object.Name:
	}
}

func Compare(d *object.Dictionary, a object.Array) {
	_ = d.Get("T") != object.Name("Metadata")
	_ = object.Boolean(true) == a[0]
	switch d.Get("S") {
	case object.Name("X"):
	}
	_ = d.Get("P") == nil
	_ = nil != a[1]
	switch d.Get("Q") {
	case nil:
	}
}

func Clean(d *object.Dictionary, a object.Array, o object.Object) {
	_, _ = resolve(d.Get("N")).(object.Integer)
	_, _ = d.Get("R").(object.IndirectRef)
	_, _ = a[1].(object.IndirectRef)
	switch d.Get("S").(type) {
	case object.IndirectRef, object.Name:
	}
	switch o.(type) {
	case object.IndirectRef:
	case object.Name:
	}
	x := d.Get("X")
	x = resolve(x)
	_, _ = x.(object.Name)
	for _, e := range a {
		_, _ = resolve(e).(object.Name)
	}
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
		"snippet/snippet.go:Arrays:All",
		"snippet/snippet.go:Arrays:Values",
		"snippet/snippet.go:Arrays:[]",
		"snippet/snippet.go:Arrays:[]",
		"snippet/snippet.go:Compare:S",
		"snippet/snippet.go:Compare:T",
		"snippet/snippet.go:Compare:[]",
		"snippet/snippet.go:Direct:F",
		"snippet/snippet.go:Direct:N",
		"snippet/snippet.go:Direct:P",
		"snippet/snippet.go:Param:param:o",
		"snippet/snippet.go:ViaVar:Lookup",
		"snippet/snippet.go:ViaVar:M",
		"snippet/snippet.go:ViaVar:N",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("sites:\n got %s\nwant %s", strings.Join(got, "\n     "), strings.Join(want, "\n     "))
	}
	if msgs := compareAllowlist(map[string]int{"a": 2, "b": 1}, map[string]int{"a": 1, "b": 1, "c": 1}); len(msgs) != 2 {
		t.Errorf("compareAllowlist reported %d problems, want 2 (a over its allowance, c gone): %v", len(msgs), msgs)
	}
}
