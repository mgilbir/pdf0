package lint

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"sort"
	"strings"
	"testing"
)

// rawTypeReaders are the functions allowed to read a structure element's
// written type: the checks whose question is whether that written type is
// mapped at all.
var rawTypeReaders = map[string]bool{
	"github.com/mgilbir/pdf0/pdfua.checkUARoleMap":            true,
	"github.com/mgilbir/pdf0/pdfua.checkUA2NamespaceRoleMaps": true,
	"github.com/mgilbir/pdf0/pdfa.checkLevelAStructTypes":     true,
}

// TestStructureChecksReadTheResolvedType rejects two ways of reading a
// structure element's type as written rather than as resolved.
//
// Incident, audit 2026-09-22 C80/C81 (siblings of the previous audit's C29):
// checkFigureAlt compared StructNode.RawS with "Figure", so an /Img mapped to
// /Figure needed no alternate text; checkUAAnnotStructType walked /K itself
// and compared the parent's /S, so a /MyLink mapped to /Link was "not a Link".
// The previous fix moved the heading checks to StdType and left these two.
// And the PDF 2.0 namespaces were known to nobody, so /Title in the PDF 2.0
// namespace was "neither standard nor mapped".
//
// Flagged, outside internal/core:
//   - reading the field core.StructNode.RawS, except in rawTypeReaders;
//   - in a validator package, a function that descends /K itself (calls
//     StructKids, or reads /K) and reads a dictionary's /S: ask
//     core.ResolveStructType, core.StandardStructType or the flattened tree.
func TestStructureChecksReadTheResolvedType(t *testing.T) {
	m := load(t)
	var found []string
	for _, p := range m.pkgs {
		if p.path == corePath {
			continue
		}
		for _, f := range p.files {
			found = append(found, rawStructTypeReads(m.fset, p, f, documentRulePackages[p.path])...)
		}
	}
	sort.Strings(found)
	for _, f := range found {
		t.Error(f)
	}
}

func rawStructTypeReads(fset *token.FileSet, p *checkedPackage, f *ast.File, validator bool) []string {
	var out []string
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		name := p.path + "." + fd.Name.Name
		var sReads []ast.Node
		descends := false
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.SelectorExpr:
				sel, ok := p.info.Selections[n]
				if ok && sel.Kind() == types.FieldVal && n.Sel.Name == "RawS" && stIsCoreType(sel.Recv(), "StructNode") && !rawTypeReaders[name] {
					out = append(out, fset.Position(n.Pos()).String()+": "+fd.Name.Name+" reads StructNode.RawS, the type as written; compare StdType")
				}
			case *ast.CallExpr:
				switch fn := n.Fun.(type) {
				case *ast.SelectorExpr:
					if fn.Sel.Name == "StructKids" || fn.Sel.Name == "structKids" {
						descends = true
					}
					if (fn.Sel.Name == "Get" || fn.Sel.Name == "Lookup") && len(n.Args) == 1 && stIsDictMethod(p, fn) {
						switch stConstString(p, n.Args[0]) {
						case "S":
							sReads = append(sReads, n)
						case "K":
							descends = true
						}
					}
				case *ast.Ident:
					if fn.Name == "structKids" {
						descends = true
					}
				}
			}
			return true
		})
		if validator && descends {
			for _, n := range sReads {
				out = append(out, fset.Position(n.Pos()).String()+": "+fd.Name.Name+" walks /K and reads /S as written; "+
					"resolve the type with core.ResolveStructType or read the flattened tree")
			}
		}
	}
	return out
}

func stIsCoreType(t types.Type, name string) bool {
	t = types.Unalias(t)
	if ptr, ok := t.(*types.Pointer); ok {
		t = types.Unalias(ptr.Elem())
	}
	named, ok := t.(*types.Named)
	return ok && named.Obj().Name() == name && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == corePath
}

func stIsDictMethod(p *checkedPackage, fn *ast.SelectorExpr) bool {
	sel, ok := p.info.Selections[fn]
	if !ok {
		return false
	}
	t := types.Unalias(sel.Recv())
	if ptr, ok := t.(*types.Pointer); ok {
		t = types.Unalias(ptr.Elem())
	}
	named, ok := t.(*types.Named)
	return ok && named.Obj().Name() == "Dictionary" && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == "github.com/mgilbir/pdf0/object"
}

func stConstString(p *checkedPackage, e ast.Expr) string {
	tv, ok := p.info.Types[e]
	if !ok || tv.Value == nil || tv.Value.Kind() != constant.String {
		return ""
	}
	return constant.StringVal(tv.Value)
}

// TestStructureChecksReadTheResolvedTypeDetects is the check's planted bug.
func TestStructureChecksReadTheResolvedTypeDetects(t *testing.T) {
	const src = `package snippet

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

func figures(nodes []core.StructNode) int {
	c := 0
	for _, n := range nodes {
		if n.RawS == "Figure" {
			c++
		}
	}
	return c
}

func walk(d core.View, elem *object.Dictionary) {
	s := elem.Get("S")
	for _, k := range core.StructKids(d, elem) {
		_ = k
	}
	_ = s
}

func walkK(elem *object.Dictionary) {
	_ = elem.Get("K")
	_, _ = elem.Lookup("S")
}

func clean(d core.View, nodes []core.StructNode, action *object.Dictionary) {
	for _, n := range nodes {
		_ = n.StdType
	}
	_ = action.Get("S") // an action's /S, in a function that walks no /K
}

// pdfua names the node type through an alias.
type node = core.StructNode

func viaAlias(ns []node) bool { return len(ns) > 0 && ns[0].RawS == "X" }
`
	fset, p, f := typeCheckModuleSnippet(t, src, corePath, "github.com/mgilbir/pdf0/object")
	var got []string
	for _, s := range rawStructTypeReads(fset, p, f, true) {
		got = append(got, s[:strings.Index(s, ": ")])
	}
	sort.Strings(got)
	want := []string{"snippet.go:11:6", "snippet.go:19:7", "snippet.go:28:9", "snippet.go:41:55"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("flagged %v, want %v", got, want)
	}
}
