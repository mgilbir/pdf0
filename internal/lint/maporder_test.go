package lint

import (
	"go/ast"
	"go/types"
	"sort"
	"strings"
	"testing"
)

// TestNoMapOrderInTheObjectModel rejects a range over a map whose body builds
// the object model: a Dictionary.Set, an object.Array append, or an Add that
// allocates an object number. Go randomises map iteration, so each of these
// makes the written file depend on it — the key order of a dictionary, the
// element order of an array, which object gets which number — and byte-
// reproducible output (pdfa_reproducible_test.go) is a stated goal. Twenty
// identical builds gave ten distinct files through exactly these sites: a
// page's unused resources, the order faces were embedded in, a structure
// tree's role map (audit 2026-09-22 C95).
//
// Iterate slices.Sorted(maps.Keys(m)) instead. A range whose order provably
// cannot reach the output — it fills another map, or it is followed by a sort
// of what it built — carries a `// maporder: <reason>` comment on the range
// line or the line above it.
func TestNoMapOrderInTheObjectModel(t *testing.T) {
	m := load(t)
	var found []string
	for _, p := range m.pkgs {
		for _, f := range p.files {
			exempt := mapOrderExemptions(m, f)
			ast.Inspect(f, func(n ast.Node) bool {
				rs, ok := n.(*ast.RangeStmt)
				if !ok {
					return true
				}
				tv, ok := p.info.Types[rs.X]
				if !ok {
					return true
				}
				if _, isMap := tv.Type.Underlying().(*types.Map); !isMap {
					return true
				}
				line := m.fset.Position(rs.Pos()).Line
				if exempt[line] || exempt[line-1] {
					return true
				}
				if what := buildsObjectModel(p, rs.Body); what != "" {
					found = append(found, m.fset.Position(rs.Pos()).String()+": "+what)
				}
				return true
			})
		}
	}
	sort.Strings(found)
	for _, f := range found {
		t.Errorf("%s inside a range over a map: the output would depend on Go's random map order; "+
			"range over slices.Sorted(maps.Keys(m)), or mark the range `// maporder: <reason>`", f)
	}
}

// mapOrderExemptions returns the lines of f carrying a maporder comment.
func mapOrderExemptions(m *module, f *ast.File) map[int]bool {
	out := map[int]bool{}
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			if strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(c.Text, "//")), "maporder:") {
				out[m.fset.Position(c.Pos()).Line] = true
			}
		}
	}
	return out
}

// isAllocator reports whether t has a method Add(object.Object) object.IndirectRef,
// which is what fonts.Allocator and images' allocator are, and what
// *pdf0.Document implements.
func isAllocator(t types.Type, isObjectType func(types.Type, string) bool) bool {
	ms := types.NewMethodSet(t)
	if _, isPtr := t.(*types.Pointer); !isPtr {
		if _, isIface := t.Underlying().(*types.Interface); !isIface {
			ms = types.NewMethodSet(types.NewPointer(t))
		}
	}
	sel := ms.Lookup(nil, "Add")
	if sel == nil {
		// Add is exported, so the package argument does not matter; Lookup
		// with nil finds exported names.
		return false
	}
	sig, ok := sel.Type().(*types.Signature)
	return ok && sig.Params().Len() == 1 && sig.Results().Len() == 1 &&
		isObjectType(sig.Results().At(0).Type(), "IndirectRef")
}

// buildsObjectModel reports the first call in body that writes the object
// model in an order-dependent way, or "".
func buildsObjectModel(p *checkedPackage, body *ast.BlockStmt) string {
	const objectPath = "github.com/mgilbir/pdf0/object"
	isObjectType := func(t types.Type, name string) bool {
		if ptr, ok := t.(*types.Pointer); ok {
			t = ptr.Elem()
		}
		named, ok := t.(*types.Named)
		return ok && named.Obj().Name() == name && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == objectPath
	}
	var what string
	ast.Inspect(body, func(n ast.Node) bool {
		if what != "" {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		// A call handed an allocator — face.Embed(d), images.Embed(d, img) —
		// numbers objects inside the callee, where the loop cannot be seen.
		for _, arg := range call.Args {
			if tv, ok := p.info.Types[arg]; ok && isAllocator(tv.Type, isObjectType) {
				what = "a call handed an object allocator"
				return false
			}
		}
		switch fn := call.Fun.(type) {
		case *ast.SelectorExpr:
			sel, ok := p.info.Selections[fn]
			if !ok {
				return true
			}
			switch fn.Sel.Name {
			case "Set":
				if isObjectType(sel.Recv(), "Dictionary") {
					what = "Dictionary.Set"
				}
			case "Add":
				if sig, ok := sel.Type().(*types.Signature); ok && sig.Results().Len() == 1 &&
					isObjectType(sig.Results().At(0).Type(), "IndirectRef") {
					what = "an Add allocating an object number"
				}
			}
		case *ast.Ident:
			if fn.Name == "append" && len(call.Args) > 0 {
				if tv, ok := p.info.Types[call.Args[0]]; ok && isObjectType(tv.Type, "Array") {
					what = "an append to an object.Array"
				}
			}
		}
		return true
	})
	return what
}
