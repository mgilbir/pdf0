package lint

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"sort"
	"strings"
	"testing"
)

// TestRecursiveWalkersAreBounded catches a recursive function over the object
// graph that has no depth bound.
//
// A document is a graph the file controls. A walker that recurses once per
// level of it — a structure tree, a page tree, a chain of forms, a colour space
// built on a colour space — needs as many stack frames as the file has levels,
// and a visited set does not help: a chain of a hundred thousand distinct
// objects has no cycle to find. At about a kilobyte a frame, a chain a few
// megabytes long ends in "fatal error: stack overflow", which no recover can
// catch (audit 2026-09-22 C84, and before it C13, C14 and C17, each fixed at
// one site while its siblings kept the shape).
//
// So every function that is part of a recursion (directly, or through other
// functions or closures of its package) and that takes a View, a Document or
// an object-model value must be bounded, in one of two ways:
//
//   - it calls the uniform depth guard, core.View.Descend, which reports the
//     walk-depth limit and ends the run's work when a walk goes deeper than
//     core.MaxWalkDepth; or
//   - it takes a depth parameter (an integer parameter whose name contains
//     "depth") and compares it against a bound, or hands it to Descend — a
//     local bound for a walker whose limit is part of its definition, such as
//     the function evaluator's. A depth parameter that is only passed along
//     bounds nothing, and does not count.
//
// recursionAllowlist names the recursions bounded some other way, each with
// the reason. The test fails for an unbounded recursion that is not listed,
// and for a listed one that is no longer found, so the list can only shrink.
func TestRecursiveWalkersAreBounded(t *testing.T) {
	m := load(t)
	var found []string
	for _, p := range m.pkgs {
		if !recursionScope[p.path] {
			continue
		}
		for _, fn := range unboundedRecursions(p) {
			site := p.path + "." + fn
			if _, ok := recursionAllowlist[site]; ok {
				delete(remaining(t), site)
				continue
			}
			found = append(found, site)
		}
	}
	sort.Strings(found)
	for _, f := range found {
		t.Errorf("%s: recursive over the object graph with no depth bound; call View.Descend, take a depth parameter, or make it iterative", f)
	}
	for site := range remaining(t) {
		t.Errorf("%s: allowlisted as a bounded recursion but no longer found; remove it from recursionAllowlist", site)
	}
}

var remainingSites map[string]string

// remaining is the allowlist entries not yet matched in this run.
func remaining(t *testing.T) map[string]string {
	if remainingSites == nil {
		remainingSites = map[string]string{}
		for k, v := range recursionAllowlist {
			remainingSites[k] = v
		}
	}
	return remainingSites
}

// recursionScope is the packages that walk a file's object graph.
var recursionScope = map[string]bool{
	"github.com/mgilbir/pdf0":               true,
	"github.com/mgilbir/pdf0/internal/core": true,
	"github.com/mgilbir/pdf0/pdfa":          true,
	"github.com/mgilbir/pdf0/pdfua":         true,
	"github.com/mgilbir/pdf0/pdfx":          true,
	"github.com/mgilbir/pdf0/pdfvt":         true,
	"github.com/mgilbir/pdf0/pdfr":          true,
	"github.com/mgilbir/pdf0/dpart":         true,
	"github.com/mgilbir/pdf0/images":        true,
}

// recursionAllowlist: recursions over object-graph values that are bounded by
// something other than a depth guard. Keyed "package.function".
var recursionAllowlist = map[string]string{
	// Recursions over direct values only: they stop at an indirect reference,
	// so their depth is the nesting of arrays and dictionaries written inline,
	// which the parser caps (syntax's maxParseDepth, 1000) for any document
	// that was read.
	"github.com/mgilbir/pdf0.(*Document).dropEncryption.walk":   "direct values only; parser-capped nesting",
	"github.com/mgilbir/pdf0.(*Document).encryptReachable.walk": "direct values only (references go on an explicit stack); parser-capped nesting",
	"github.com/mgilbir/pdf0.holdsSignatureDict":                "direct values only; parser-capped nesting",
	"github.com/mgilbir/pdf0.(*pageImporter).copyValue":         "direct values only (a reference is queued by copyRef, not followed); parser-capped nesting",
	"github.com/mgilbir/pdf0.(*pageImporter).copyDict":          "direct values only; parser-capped nesting",
	"github.com/mgilbir/pdf0.(*pageImporter).remapDest":         "copies a destination's direct elements; parser-capped nesting",
	"github.com/mgilbir/pdf0/pdfa.collectOCGRefs":               "direct arrays only; parser-capped nesting",
	"github.com/mgilbir/pdf0.swapRefs.walk":                     "walks objects pdf0's own font embedder has just built, not the file",

	// Recursions over the caller's Go values, not the file.
	"github.com/mgilbir/pdf0.(*Document).checkOutlinePages":   "the caller's []OutlineItem tree",
	"github.com/mgilbir/pdf0.(*Document).writeOutlineLevel":   "the caller's []OutlineItem tree",
	"github.com/mgilbir/pdf0.(*Document).checkStructurePages": "the caller's []StructElem tree",
	"github.com/mgilbir/pdf0.(*Document).writeStructLevel":    "the caller's []StructElem tree",

	// Bounded by a depth kept outside the parameter list.
	"github.com/mgilbir/pdf0/images.(*csResolver).resolve":             "csResolver.depth, against maxColorSpaceDepth (audit 2026-09-22 C13)",
	"github.com/mgilbir/pdf0/images.(*csResolver).iccBased":            "through csResolver.resolve and its depth",
	"github.com/mgilbir/pdf0/images.(*csResolver).indexed":             "through csResolver.resolve and its depth",
	"github.com/mgilbir/pdf0/images.(*csResolver).tint":                "through csResolver.resolve and its depth",
	"github.com/mgilbir/pdf0/internal/core.(*contentEngine).run":       "contentEngine.depth, against maxExecDepth",
	"github.com/mgilbir/pdf0/internal/core.(*contentEngine).interpret": "through contentEngine.run and its depth",
	"github.com/mgilbir/pdf0/internal/core.(*contentEngine).showType3": "through contentEngine.run and its depth",

	// Functions that carry the depth through their cycle and leave the
	// comparison to the partner that makes it.
	"github.com/mgilbir/pdf0/internal/core.evalType3":        "through evalFunctionDepth, which compares depth to maxFunctionDepth",
	"github.com/mgilbir/pdf0.(*Document).checkUncoloredForm": "through checkUncoloredXObject, which compares depth to maxUncoloredFormDepth",
	"github.com/mgilbir/pdf0.(*pageImporter).refDropsOut":    "through dropsOutAt, which calls Descend with the depth",

	"github.com/mgilbir/pdf0/internal/core.(*resolvedEqual).values": "through resolvedEqual.equal, which compares depth to maxResolvedEqualDepth",
	"github.com/mgilbir/pdf0/internal/core.(*resolvedEqual).dicts":  "through resolvedEqual.equal, which compares depth to maxResolvedEqualDepth",

	// The embedded-PDF/A check validates each embedded document through the
	// same entry point, bounded by pdfa.MaxEmbeddedDepth and the embedded
	// budget, and every level of it charges the same work meter.
	"github.com/mgilbir/pdf0.validatePDFABudget": "embedded documents, bounded by pdfa.MaxEmbeddedDepth",
}

// fnNode is a function in the call graph: a declared function or method, or a
// closure assigned to a local variable (the `var walk func(...); walk =
// func(...) {...}` shape every recursive closure in the module uses).
type fnNode struct {
	name   string
	obj    types.Object // *types.Func, or the *types.Var a closure is assigned to
	sig    *types.Signature
	body   *ast.BlockStmt
	params *ast.FieldList
	recv   *ast.FieldList
}

func unboundedRecursions(p *checkedPackage) []string {
	nodes := map[types.Object]*fnNode{}
	var order []*fnNode
	for _, f := range p.files {
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			obj := p.info.Defs[fd.Name]
			if obj == nil {
				continue
			}
			name := fd.Name.Name
			if fd.Recv != nil && len(fd.Recv.List) == 1 {
				name = recursionRecvName(fd.Recv.List[0].Type) + "." + name
			}
			n := &fnNode{name: name, obj: obj, sig: obj.Type().(*types.Signature), body: fd.Body, params: fd.Type.Params, recv: fd.Recv}
			nodes[obj] = n
			order = append(order, n)
			// Closures assigned to a variable inside it.
			ast.Inspect(fd.Body, func(x ast.Node) bool {
				as, ok := x.(*ast.AssignStmt)
				if !ok || len(as.Lhs) != len(as.Rhs) {
					return true
				}
				for i, rhs := range as.Rhs {
					lit, ok := rhs.(*ast.FuncLit)
					if !ok {
						continue
					}
					id, ok := as.Lhs[i].(*ast.Ident)
					if !ok {
						continue
					}
					v := p.info.Uses[id]
					if v == nil {
						v = p.info.Defs[id]
					}
					if v == nil {
						continue
					}
					sig, _ := v.Type().(*types.Signature)
					if sig == nil {
						continue
					}
					cn := &fnNode{name: name + "." + id.Name, obj: v, sig: sig, body: lit.Body, params: lit.Type.Params}
					nodes[v] = cn
					order = append(order, cn)
				}
				return true
			})
		}
	}
	// Edges: calls from a body to another node. A closure's body belongs to
	// its own node, not its enclosing function's.
	edges := map[*fnNode][]*fnNode{}
	closureBodies := map[*ast.BlockStmt]bool{}
	for _, n := range nodes {
		if _, isVar := n.obj.(*types.Var); isVar {
			closureBodies[n.body] = true
		}
	}
	for _, n := range order {
		ast.Inspect(n.body, func(x ast.Node) bool {
			if lit, ok := x.(*ast.FuncLit); ok && closureBodies[lit.Body] && lit.Body != n.body {
				return false
			}
			call, ok := x.(*ast.CallExpr)
			if !ok {
				return true
			}
			var id *ast.Ident
			switch fun := ast.Unparen(call.Fun).(type) {
			case *ast.Ident:
				id = fun
			case *ast.SelectorExpr:
				id = fun.Sel
			case *ast.IndexExpr:
				if i, ok := fun.X.(*ast.Ident); ok {
					id = i
				}
			}
			if id == nil {
				return true
			}
			obj := p.info.Uses[id]
			if f, ok := obj.(*types.Func); ok {
				obj = f.Origin()
			}
			if to, ok := nodes[obj]; ok {
				edges[n] = append(edges[n], to)
			}
			return true
		})
	}
	var out []string
	for _, scc := range tarjan(order, edges) {
		cyclic := len(scc) > 1
		if len(scc) == 1 {
			for _, to := range edges[scc[0]] {
				if to == scc[0] {
					cyclic = true
				}
			}
		}
		if !cyclic {
			continue
		}
		for _, n := range scc {
			if !overObjectGraph(n.sig) {
				continue
			}
			if hasDepthParam(p, n) || callsDescend(p, n) {
				continue
			}
			out = append(out, n.name)
		}
	}
	return out
}

func recursionRecvName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return "(*" + recursionRecvName(t.X) + ")"
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr:
		return recursionRecvName(t.X)
	}
	return fmt.Sprintf("%T", e)
}

// overObjectGraph reports whether a signature takes (or is a method on) a
// value the object graph is made of, or the View or Document that holds it.
func overObjectGraph(sig *types.Signature) bool {
	check := func(t types.Type) bool {
		s := types.TypeString(t, nil)
		for _, want := range []string{
			"github.com/mgilbir/pdf0/object.",
			"github.com/mgilbir/pdf0/internal/core.View",
			"github.com/mgilbir/pdf0.Document",
		} {
			if strings.Contains(s, want) {
				return true
			}
		}
		return false
	}
	if r := sig.Recv(); r != nil && check(r.Type()) {
		return true
	}
	for i := 0; i < sig.Params().Len(); i++ {
		if check(sig.Params().At(i).Type()) {
			return true
		}
	}
	return false
}

// hasDepthParam reports whether n takes an integer parameter named for depth
// and its body compares that parameter against something, or hands it to the
// depth guard — a depth parameter that is only passed along bounds nothing.
func hasDepthParam(p *checkedPackage, n *fnNode) bool {
	depthVars := map[types.Object]bool{}
	if n.params != nil {
		for _, f := range n.params.List {
			for _, id := range f.Names {
				v := p.info.Defs[id]
				if v == nil || !strings.Contains(strings.ToLower(id.Name), "depth") {
					continue
				}
				if b, ok := v.Type().Underlying().(*types.Basic); ok && b.Info()&types.IsInteger != 0 {
					depthVars[v] = true
				}
			}
		}
	}
	if len(depthVars) == 0 {
		return false
	}
	mentions := func(e ast.Expr) bool {
		found := false
		ast.Inspect(e, func(x ast.Node) bool {
			if id, ok := x.(*ast.Ident); ok && depthVars[p.info.Uses[id]] {
				found = true
			}
			return !found
		})
		return found
	}
	bounded := false
	ast.Inspect(n.body, func(x ast.Node) bool {
		switch e := x.(type) {
		case *ast.BinaryExpr:
			switch e.Op {
			case token.LSS, token.GTR, token.LEQ, token.GEQ:
				if mentions(e.X) || mentions(e.Y) {
					bounded = true
				}
			}
		case *ast.CallExpr:
			if sel, ok := e.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Descend" {
				for _, a := range e.Args {
					if mentions(a) {
						bounded = true
					}
				}
			}
		}
		return !bounded
	})
	return bounded
}

// callsDescend reports whether n's own body calls core's depth guard.
func callsDescend(p *checkedPackage, n *fnNode) bool {
	found := false
	ast.Inspect(n.body, func(x ast.Node) bool {
		if lit, ok := x.(*ast.FuncLit); ok && lit.Body != n.body {
			return false
		}
		sel, ok := x.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Descend" {
			return true
		}
		if f, ok := p.info.Uses[sel.Sel].(*types.Func); ok && f.Pkg() != nil && f.Pkg().Path() == "github.com/mgilbir/pdf0/internal/core" {
			found = true
		}
		return true
	})
	return found
}

// tarjan returns the strongly connected components of the graph.
func tarjan(nodes []*fnNode, edges map[*fnNode][]*fnNode) [][]*fnNode {
	index := map[*fnNode]int{}
	low := map[*fnNode]int{}
	on := map[*fnNode]bool{}
	var stack []*fnNode
	var out [][]*fnNode
	next := 0
	var strong func(v *fnNode)
	strong = func(v *fnNode) {
		index[v], low[v] = next, next
		next++
		stack = append(stack, v)
		on[v] = true
		for _, w := range edges[v] {
			if _, seen := index[w]; !seen {
				strong(w)
				low[v] = min(low[v], low[w])
			} else if on[w] {
				low[v] = min(low[v], index[w])
			}
		}
		if low[v] == index[v] {
			var scc []*fnNode
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				on[w] = false
				scc = append(scc, w)
				if w == v {
					break
				}
			}
			out = append(out, scc)
		}
	}
	for _, v := range nodes {
		if _, seen := index[v]; !seen {
			strong(v)
		}
	}
	return out
}
