package lint

import (
	"fmt"
	"go/types"
	"sort"
	"strings"
	"testing"
)

// TestPublicAPIMentionsNoInternalType rejects an exported identifier of a
// public package whose signature, fields or method set mentions a type from
// an internal package.
//
// Incident, audit 2026-09-22 C163: about thirty exported functions in sign,
// pdfa, facturx, dpart, pdfx, pdfua, pdfvt, pdfr and images took
// internal/core.View. No caller outside the module can name that type, so
// none could call them, yet pkg.go.dev listed them as API beside the ones a
// caller can use. They existed only so the root package could reach the
// implementation across the package boundary. That reach now goes through
// internal/bridge, and this check keeps it there.
//
// A public package is any package of the module that is not under an
// internal/ directory and is not a command (package main). Flagged: an
// exported function, variable or constant whose type mentions an internal
// type; an exported type that is an alias of one, or whose exported fields
// (embedded ones included) or exported methods (promoted ones included)
// mention one.
func TestPublicAPIMentionsNoInternalType(t *testing.T) {
	m := load(t)
	var found []string
	for _, p := range m.pkgs {
		found = append(found, internalTypeLeaks(p.types)...)
	}
	sort.Strings(found)
	for _, f := range found {
		t.Error(f)
	}
}

// internalTypeLeaks lists the exported identifiers of pkg that mention an
// internal type, one line each, or nothing if pkg is not public.
func internalTypeLeaks(pkg *types.Package) []string {
	if isInternalPath(pkg.Path()) || pkg.Name() == "main" {
		return nil
	}
	var out []string
	report := func(name string, t types.Type) {
		if leak := internalTypeIn(t, map[types.Type]bool{}); leak != "" {
			out = append(out, fmt.Sprintf("%s.%s mentions %s, which no caller outside the module can name",
				pkg.Path(), name, leak))
		}
	}
	scope := pkg.Scope()
	for _, name := range scope.Names() {
		obj := scope.Lookup(name)
		if !obj.Exported() {
			continue
		}
		switch obj := obj.(type) {
		case *types.Func, *types.Var, *types.Const:
			report(name, obj.Type())
		case *types.TypeName:
			if obj.IsAlias() {
				report(name, types.Unalias(obj.Type()))
				continue
			}
			if st, ok := obj.Type().Underlying().(*types.Struct); ok {
				for i := 0; i < st.NumFields(); i++ {
					if f := st.Field(i); f.Exported() {
						report(name+"."+f.Name(), f.Type())
					}
				}
			} else if _, ok := obj.Type().Underlying().(*types.Interface); !ok {
				// A defined non-struct type: its underlying type is
				// visible in godoc (type T []core.X).
				report(name, obj.Type().Underlying())
			}
			// Method sets: of T and of *T, promoted methods included, and
			// an interface's own methods.
			seen := map[string]bool{}
			for _, recv := range []types.Type{obj.Type(), types.NewPointer(obj.Type())} {
				if _, isIface := obj.Type().Underlying().(*types.Interface); isIface && recv != obj.Type() {
					continue
				}
				ms := types.NewMethodSet(recv)
				for i := 0; i < ms.Len(); i++ {
					fn := ms.At(i).Obj()
					if !fn.Exported() || seen[fn.Name()] {
						continue
					}
					seen[fn.Name()] = true
					report(name+"."+fn.Name(), fn.Type())
				}
			}
		}
	}
	return out
}

// internalTypeIn returns the first internal named type that t mentions, or
// "". A named type is judged by its own package, not by its structure:
// mentioning object.Dictionary is fine however it is built.
func internalTypeIn(t types.Type, seen map[types.Type]bool) string {
	if t == nil || seen[t] {
		return ""
	}
	seen[t] = true
	switch t := t.(type) {
	case *types.Alias:
		if obj := t.Obj(); obj.Pkg() != nil && isInternalPath(obj.Pkg().Path()) {
			return obj.Pkg().Path() + "." + obj.Name()
		}
		return internalTypeIn(types.Unalias(t), seen)
	case *types.Named:
		if obj := t.Obj(); obj.Pkg() != nil && isInternalPath(obj.Pkg().Path()) {
			return obj.Pkg().Path() + "." + obj.Name()
		}
		if args := t.TypeArgs(); args != nil {
			for i := 0; i < args.Len(); i++ {
				if s := internalTypeIn(args.At(i), seen); s != "" {
					return s
				}
			}
		}
		return ""
	case *types.Pointer:
		return internalTypeIn(t.Elem(), seen)
	case *types.Slice:
		return internalTypeIn(t.Elem(), seen)
	case *types.Array:
		return internalTypeIn(t.Elem(), seen)
	case *types.Chan:
		return internalTypeIn(t.Elem(), seen)
	case *types.Map:
		if s := internalTypeIn(t.Key(), seen); s != "" {
			return s
		}
		return internalTypeIn(t.Elem(), seen)
	case *types.Signature:
		for _, tup := range []*types.Tuple{t.Params(), t.Results()} {
			for i := 0; i < tup.Len(); i++ {
				if s := internalTypeIn(tup.At(i).Type(), seen); s != "" {
					return s
				}
			}
		}
		if tps := t.TypeParams(); tps != nil {
			for i := 0; i < tps.Len(); i++ {
				if s := internalTypeIn(tps.At(i).Constraint(), seen); s != "" {
					return s
				}
			}
		}
		return ""
	case *types.Struct:
		// An unnamed struct in a signature shows all its fields.
		for i := 0; i < t.NumFields(); i++ {
			if s := internalTypeIn(t.Field(i).Type(), seen); s != "" {
				return s
			}
		}
		return ""
	case *types.Interface:
		for i := 0; i < t.NumMethods(); i++ {
			if s := internalTypeIn(t.Method(i).Type(), seen); s != "" {
				return s
			}
		}
		for i := 0; i < t.NumEmbeddeds(); i++ {
			if s := internalTypeIn(t.EmbeddedType(i), seen); s != "" {
				return s
			}
		}
		return ""
	case *types.Union:
		for i := 0; i < t.Len(); i++ {
			if s := internalTypeIn(t.Term(i).Type(), seen); s != "" {
				return s
			}
		}
		return ""
	case *types.TypeParam:
		return internalTypeIn(t.Constraint(), seen)
	}
	return ""
}

// isInternalPath reports whether an import path is under an internal/
// directory, which the go tool forbids importing from outside its parent.
func isInternalPath(path string) bool {
	return strings.HasPrefix(path, "internal/") || strings.Contains(path, "/internal/") ||
		strings.HasSuffix(path, "/internal") || path == "internal"
}

// TestPublicAPIMentionsNoInternalTypeDetects is the check's planted bug:
// every shape through which an internal type reaches a public package's
// godoc, and the shapes that do not.
func TestPublicAPIMentionsNoInternalTypeDetects(t *testing.T) {
	const src = `package snippet

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/finding"
	"github.com/mgilbir/pdf0/object"
)

func Param(d core.View) {}
func Result() *core.View { return nil }
func InSlice(vs []core.View) {}
func InFunc(f func(core.View) error) {}
func InMap(m map[string]*core.View) {}
func Clean(d *object.Dictionary) error { return nil }
func unexported(d core.View) {}

var Hook func(core.View)
var fine = func(core.View) {}

type Alias = core.View
type Slice []core.View
type Carrier struct {
	Exported core.View
	hidden   core.View
	Ok       int
}
type Embeds struct {
	core.View
}
type Methods struct{}

func (Methods) Check(d core.View) []string { return nil }
func (*Methods) Ptr() *core.View            { return nil }
func (Methods) Fine() error                 { return nil }
func (Methods) private(d core.View)         {}

type Iface interface {
	Check(d core.View)
}
type Generic[T any] struct{ V T }

var Inst Generic[finding.V]
`
	_, p, _ := typeCheckModuleSnippet(t, src,
		"github.com/mgilbir/pdf0/internal/core", "github.com/mgilbir/pdf0/internal/finding", "github.com/mgilbir/pdf0/object")
	var got []string
	for _, s := range internalTypeLeaks(p.types) {
		got = append(got, strings.TrimPrefix(s[:strings.Index(s, " mentions")], "example/snippet."))
	}
	sort.Strings(got)
	want := []string{
		"Alias", "Carrier.Exported", "Embeds.View", "Hook", "Iface.Check", "InFunc", "InMap", "InSlice",
		"Inst", "Methods.Check", "Methods.Ptr", "Param", "Result", "Slice",
	}
	// Embeds also promotes View's exported methods; those are flagged only if
	// their signatures mention an internal type, so compare the prefix set.
	var gotTop []string
	for _, g := range got {
		if strings.HasPrefix(g, "Embeds.") && g != "Embeds.View" {
			continue
		}
		gotTop = append(gotTop, g)
	}
	if strings.Join(gotTop, " ") != strings.Join(want, " ") {
		t.Errorf("flagged %v, want %v", gotTop, want)
	}
	// A public package's path is what makes it public: the same file under
	// internal/ is not checked.
	if !isInternalPath("github.com/mgilbir/pdf0/internal/core") || isInternalPath("github.com/mgilbir/pdf0/pdfa") ||
		isInternalPath("github.com/mgilbir/pdf0/internalish") {
		t.Error("isInternalPath misjudges a path")
	}
}
