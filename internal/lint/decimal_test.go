package lint

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"testing"
)

// TestNoOpenCodedDecimalAccumulator rejects v = v*10 + d outside
// internal/checked. The loop wraps silently on a long run of digits, and every
// such run in this module is read from a file: an inline image's
// /L 9223372036854775807 plus its data offset came out negative and indexed a
// content stream before its start (audit 2026-09-22 C16), and six more copies
// of the loop — xref subsection counts, CMap CIDs, /WMode, content-stream
// integers — wrapped the same way or guarded it by hand. checked.Decimal is
// the one implementation, and it saturates or refuses instead of wrapping.
func TestNoOpenCodedDecimalAccumulator(t *testing.T) {
	m := load(t)
	var found []string
	for _, p := range m.pkgs {
		if p.path == "github.com/mgilbir/pdf0/internal/checked" {
			continue
		}
		for _, f := range p.files {
			for _, pos := range decimalAccumulators(f) {
				found = append(found, m.fset.Position(pos).String())
			}
		}
	}
	sort.Strings(found)
	for _, f := range found {
		t.Errorf("%s: an open-coded decimal accumulator; use checked.Decimal, which does not wrap", f)
	}
}

// decimalAccumulators returns the positions of assignments v = v*10 + x,
// v = 10*v + x, or either with the operands of + swapped.
func decimalAccumulators(f *ast.File) []token.Pos {
	var out []token.Pos
	ast.Inspect(f, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
			return true
		}
		lhs, ok := as.Lhs[0].(*ast.Ident)
		if !ok {
			return true
		}
		sum, ok := unparen(as.Rhs[0]).(*ast.BinaryExpr)
		if !ok || sum.Op != token.ADD {
			return true
		}
		for _, side := range []ast.Expr{sum.X, sum.Y} {
			if timesTenOf(side, lhs.Name) {
				out = append(out, as.Pos())
				break
			}
		}
		return true
	})
	return out
}

// timesTenOf reports whether e is name*10 or 10*name.
func timesTenOf(e ast.Expr, name string) bool {
	mul, ok := unparen(e).(*ast.BinaryExpr)
	if !ok || mul.Op != token.MUL {
		return false
	}
	isName := func(e ast.Expr) bool {
		id, ok := unparen(e).(*ast.Ident)
		return ok && id.Name == name
	}
	isTen := func(e ast.Expr) bool {
		lit, ok := unparen(e).(*ast.BasicLit)
		return ok && lit.Kind == token.INT && lit.Value == "10"
	}
	return (isName(mul.X) && isTen(mul.Y)) || (isTen(mul.X) && isName(mul.Y))
}

func unparen(e ast.Expr) ast.Expr {
	for {
		p, ok := e.(*ast.ParenExpr)
		if !ok {
			return e
		}
		e = p.X
	}
}

// The check has teeth: each spelling of the loop is found, and near misses
// are not.
func TestDecimalAccumulators(t *testing.T) {
	src := `package p
func f(s string) {
	v, w, n := 0, 0, 0
	for i := range s {
		v = v*10 + int(s[i]-'0')    // found
		w = int(s[i]-'0') + (10 * w) // found
		n = n*16 + int(s[i])         // hex: not this check
		v = w*10 + 1                 // a different variable
	}
}`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	var lines []int
	for _, pos := range decimalAccumulators(f) {
		lines = append(lines, fset.Position(pos).Line)
	}
	if len(lines) != 2 || lines[0] != 5 || lines[1] != 6 {
		t.Errorf("found accumulators on lines %v, want [5 6]", lines)
	}
}
