package lint

import (
	"cmp"
	"fmt"
	"go/types"
	"slices"
	"strings"
	"testing"
)

// renderContextsDoc lists every exported function and method of the module's
// public packages that takes a context.Context: the entry points a caller can
// give a deadline. Each row names the variant without a context, when there is
// one, and the result types, from which a reader can see how a stop reaches
// the caller (a returned error, a final yielded error, or a finding).
//
// The list used to be written by hand, in three places, and each omitted
// variants that existed: SaveContext and StreamDataContext, then ImagesContext
// and the package-level ones in htmlpdf, fonts and facturx (docs audit
// 2026-10-11 D20). It is read from the type-checked API, so a variant added
// anywhere appears without anyone remembering to list it.
func renderContextsDoc(t *testing.T, _ string) string {
	t.Helper()
	type row struct {
		pkg     string // the package's name, "" for the root package
		prefix  string // what the entry point is written after: "fonts.Face.", "Document.", ""
		name    string
		plain   string // the variant without a context, or ""
		results string
	}
	var rows []row
	for _, p := range load(t).pkgs {
		pkg := p.types
		if pkg.Name() == "main" || isInternalPath(pkg.Path()) {
			continue
		}
		name := pkg.Name()
		if pkg.Path() == modulePath {
			name = ""
		}
		qual := func(other *types.Package) string {
			if other.Path() == modulePath {
				return "pdf0"
			}
			return other.Name()
		}
		prefix := func(recv string) string {
			s := recv
			if name != "" {
				s = name + "." + recv
			}
			if s != "" && !strings.HasSuffix(s, ".") {
				s += "."
			}
			return s
		}
		scope := pkg.Scope()
		for _, id := range scope.Names() {
			switch obj := scope.Lookup(id).(type) {
			case *types.Func:
				sig := obj.Type().(*types.Signature)
				if !obj.Exported() || !takesContext(sig) {
					continue
				}
				plain := plainVariant(id, func(n string) bool {
					_, ok := scope.Lookup(n).(*types.Func)
					return ok
				})
				rows = append(rows, row{name, prefix(""), id, plain, resultTypes(sig, qual)})
			case *types.TypeName:
				named, ok := obj.Type().(*types.Named)
				if !obj.Exported() || obj.IsAlias() || !ok {
					continue
				}
				for i := 0; i < named.NumMethods(); i++ {
					fn := named.Method(i)
					sig := fn.Type().(*types.Signature)
					if !fn.Exported() || !takesContext(sig) {
						continue
					}
					plain := plainVariant(fn.Name(), func(n string) bool {
						f, _, _ := types.LookupFieldOrMethod(types.NewPointer(named), true, pkg, n)
						_, ok := f.(*types.Func)
						return ok
					})
					rows = append(rows, row{name, prefix(id), fn.Name(), plain, resultTypes(sig, qual)})
				}
			}
		}
	}
	if len(rows) < 15 {
		t.Fatalf("found %d entry points taking a context.Context; loading the module went wrong", len(rows))
	}
	// The root package first, then the others by name; within a package,
	// functions before methods, each alphabetically.
	slices.SortFunc(rows, func(a, b row) int {
		if (a.pkg == "") != (b.pkg == "") {
			if a.pkg == "" {
				return -1
			}
			return 1
		}
		return cmp.Or(cmp.Compare(a.pkg, b.pkg), cmp.Compare(a.prefix, b.prefix), cmp.Compare(a.name, b.name))
	})
	var b strings.Builder
	b.WriteString("\n\n| Entry point | Without a context | Returns |\n|---|---|---|\n")
	for _, r := range rows {
		plain := "—"
		if r.plain != "" {
			plain = "`" + r.prefix + r.plain + "`"
		}
		fmt.Fprintf(&b, "| `%s%s` | %s | `%s` |\n", r.prefix, r.name, plain, strings.ReplaceAll(r.results, "|", "\\|"))
	}
	b.WriteString("\n")
	return b.String()
}

// modulePath is the import path of this module.
const modulePath = "github.com/mgilbir/pdf0"

// takesContext reports whether any parameter of sig is a context.Context.
func takesContext(sig *types.Signature) bool {
	ps := sig.Params()
	for i := 0; i < ps.Len(); i++ {
		if n, ok := types.Unalias(ps.At(i).Type()).(*types.Named); ok {
			if o := n.Obj(); o.Pkg() != nil && o.Pkg().Path() == "context" && o.Name() == "Context" {
				return true
			}
		}
	}
	return false
}

// plainVariant is the name of the same entry point without a context — name
// less its "Context" suffix — when exists says there is one, and "" otherwise.
func plainVariant(name string, exists func(string) bool) string {
	plain, ok := strings.CutSuffix(name, "Context")
	if !ok || plain == "" || !exists(plain) {
		return ""
	}
	return plain
}

// resultTypes is sig's result list as godoc shows it, with the root package
// named pdf0.
func resultTypes(sig *types.Signature, qual types.Qualifier) string {
	rs := sig.Results()
	parts := make([]string, rs.Len())
	for i := range parts {
		parts[i] = types.TypeString(rs.At(i).Type(), qual)
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return "(" + strings.Join(parts, ", ") + ")"
}
