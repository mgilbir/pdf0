package lint

import (
	"go/ast"
	"go/token"
	"go/types"
	"sort"
	"strings"
	"testing"
)

const rootPath = "github.com/mgilbir/pdf0"

// TestRootReexportsOnlyTheObjectModel holds the root package's re-export
// policy (object_api.go): the twelve object-model aliases are the only second
// names it declares. Flagged in the root package:
//   - an exported type alias of a type from another public package of this
//     module, other than the object package;
//   - an exported variable initialised to an exported variable of another
//     public package of this module;
//   - an exported function whose body only forwards its parameters, in
//     order, to an exported function of another public package of this
//     module.
//
// Incident, audit 2026-09-22 C160: object_api.go said "nothing else is
// re-exported" while PDFAOptions, PDFAOutputIntent, the five syntax
// constructors and Equal were — and CheckCertRevocation, GenerateXMPMetadata
// and DefaultSRGBProfile were forwarders too. A name declared in an internal
// package and given its only public name here (ErrWrongPassword) is not a
// second name, and is not flagged.
func TestRootReexportsOnlyTheObjectModel(t *testing.T) {
	m := load(t)
	for _, p := range m.pkgs {
		if p.path != rootPath {
			continue
		}
		for _, f := range secondNames(m.fset, p, rootPath) {
			t.Error(f)
		}
		return
	}
	t.Fatal("the root package was not loaded")
}

// secondNames lists the exported declarations of p that give a second name to
// something another public package of module declares, except aliases of the
// object package.
func secondNames(fset *token.FileSet, p *checkedPackage, module string) []string {
	public := func(obj types.Object) bool {
		pkg := obj.Pkg()
		return pkg != nil && pkg != p.types && obj.Exported() &&
			(pkg.Path() == module || strings.HasPrefix(pkg.Path(), module+"/")) && !isInternalPath(pkg.Path())
	}
	var out []string
	flag := func(pos token.Pos, what string) {
		out = append(out, fset.Position(pos).String()+": "+what+
			"; the root package re-exports only the object model — name it from its own package")
	}
	for _, f := range p.files {
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						if !s.Name.IsExported() || !s.Assign.IsValid() {
							continue
						}
						named, ok := types.Unalias(p.info.Defs[s.Name].Type()).(*types.Named)
						if ok && public(named.Obj()) && named.Obj().Pkg().Path() != module+"/object" {
							flag(s.Pos(), s.Name.Name+" is an alias of "+named.Obj().Pkg().Name()+"."+named.Obj().Name())
						}
					case *ast.ValueSpec:
						for i, n := range s.Names {
							if !n.IsExported() || i >= len(s.Values) {
								continue
							}
							if obj := referencedObject(p, s.Values[i]); obj != nil {
								if _, isVar := obj.(*types.Var); isVar && public(obj) {
									flag(n.Pos(), n.Name+" is "+obj.Pkg().Name()+"."+obj.Name())
								}
							}
						}
					}
				}
			case *ast.FuncDecl:
				if d.Recv != nil || !d.Name.IsExported() || d.Body == nil || len(d.Body.List) != 1 {
					continue
				}
				if target := forwardedTo(p, d); target != nil && public(target) {
					flag(d.Pos(), d.Name.Name+" only forwards to "+target.Pkg().Name()+"."+target.Name())
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

// referencedObject is the package-level object e names, if e is a qualified
// identifier (pkg.Name).
func referencedObject(p *checkedPackage, e ast.Expr) types.Object {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return nil
	}
	if _, isPkg := p.info.Uses[id].(*types.PkgName); !isPkg {
		return nil
	}
	return p.info.Uses[sel.Sel]
}

// forwardedTo is the function fd's single statement calls with fd's own
// parameters, in order and unchanged, or nil if fd does anything else.
func forwardedTo(p *checkedPackage, fd *ast.FuncDecl) types.Object {
	var call *ast.CallExpr
	switch s := fd.Body.List[0].(type) {
	case *ast.ReturnStmt:
		if len(s.Results) != 1 {
			return nil
		}
		call, _ = s.Results[0].(*ast.CallExpr)
	case *ast.ExprStmt:
		call, _ = s.X.(*ast.CallExpr)
	}
	if call == nil {
		return nil
	}
	target := referencedObject(p, call.Fun)
	if _, isFunc := target.(*types.Func); !isFunc {
		return nil
	}
	var params []string
	for _, field := range fd.Type.Params.List {
		for _, n := range field.Names {
			params = append(params, n.Name)
		}
	}
	if len(params) != len(call.Args) {
		return nil
	}
	for i, a := range call.Args {
		id, ok := a.(*ast.Ident)
		if !ok || id.Name != params[i] {
			return nil
		}
	}
	return target
}

// TestRootReexportsOnlyTheObjectModelDetects is the check's planted bug: the
// three kinds of second name, and the declarations that are not one.
func TestRootReexportsOnlyTheObjectModelDetects(t *testing.T) {
	const src = `package snippet

import (
	"github.com/mgilbir/pdf0/internal/xmp"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
	"github.com/mgilbir/pdf0/syntax"
)

type Dictionary = object.Dictionary
type Options = pdfa.SkeletonOptions
type own = pdfa.SkeletonOptions

var ErrOwned = xmp.ErrInvalidText
var ErrSecond = pdfa.ErrNotAProfile

func NewParser(data []byte) *syntax.Parser { return syntax.NewParser(data) }
func Equal(a, b object.Object) bool        { return object.Equal(a, b) }
func Swapped(a, b object.Object) bool      { return object.Equal(b, a) }
func Adds(a, b object.Object) bool         { return object.Equal(a, b) && a != nil }
func unexported(data []byte) *syntax.Parser { return syntax.NewParser(data) }
`
	fset, p, _ := typeCheckModuleSnippet(t, src, "github.com/mgilbir/pdf0/internal/xmp",
		"github.com/mgilbir/pdf0/object", "github.com/mgilbir/pdf0/pdfa", "github.com/mgilbir/pdf0/syntax")
	var got []string
	for _, s := range secondNames(fset, p, rootPath) {
		got = append(got, s[:strings.Index(s, ": ")])
	}
	want := []string{"snippet.go:11:6", "snippet.go:15:5", "snippet.go:17:1", "snippet.go:18:1"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("flagged %v, want %v", got, want)
	}
}
