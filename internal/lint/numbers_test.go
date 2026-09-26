package lint

import (
	"go/ast"
	"go/types"
	"path/filepath"
	"sort"
	"testing"
)

// numberReaders are the files allowed to call strconv.ParseFloat, each because
// it checks a grammar first and leaves strconv only the arithmetic:
// syntax.ParseNumber (PDF numbers, ISO 32000-2 7.3.3) and core's psNumber
// (PostScript numbers, for type 4 functions).
var numberReaders = map[string]bool{
	"syntax/number.go":             true,
	"internal/core/function_ps.go": true,
}

// TestNumbersAreReadWithTheirOwnGrammar rejects strconv.ParseFloat outside
// the two number readers. strconv's grammar is Go's: it takes "1e3", "0x1p3",
// "Inf" and "NaN", none of which is a PDF number, and "Inf" or "NaN" is not a
// PostScript one either. Content-stream numbers were read with it (a "-Inf"
// operand came back as an infinity), the object parser read reals with it
// behind a lexer that happened to screen them, and the PDF/A rules read
// content-stream reals with forme's CFF real reader, which read "1.2.3" as
// 1.23 and "6e2" as 62. A number a file supplies is read by
// syntax.ParseNumber; a PostScript one by psNumber.
func TestNumbersAreReadWithTheirOwnGrammar(t *testing.T) {
	m := load(t)
	all := parseFloatCalls(m)
	var found []string
	for _, c := range all {
		if !numberReaders[c.file] {
			found = append(found, c.pos)
		}
	}
	for _, f := range found {
		t.Errorf("%s: strconv.ParseFloat reads Go's number grammar; read a PDF number with syntax.ParseNumber", f)
	}
	// The check has teeth: the readers themselves are found by it, so a
	// call anywhere else would be.
	seen := map[string]bool{}
	for _, c := range all {
		seen[c.file] = true
	}
	for f := range numberReaders {
		if !seen[f] {
			t.Errorf("%s calls strconv.ParseFloat, and the check did not find it", f)
		}
	}
}

type parseFloatCall struct{ file, pos string }

// parseFloatCalls lists every reference to strconv.ParseFloat in the module's
// non-test files, with the file relative to the module root.
func parseFloatCalls(m *module) []parseFloatCall {
	root := moduleRoot(m)
	var out []parseFloatCall
	for _, p := range m.pkgs {
		for _, f := range p.files {
			ast.Inspect(f, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				fn, ok := p.info.Uses[sel.Sel].(*types.Func)
				if !ok || fn.Pkg() == nil || fn.Pkg().Path() != "strconv" || fn.Name() != "ParseFloat" {
					return true
				}
				pos := m.fset.Position(sel.Pos())
				rel, err := filepath.Rel(root, pos.Filename)
				if err != nil {
					rel = pos.Filename
				}
				out = append(out, parseFloatCall{filepath.ToSlash(rel), pos.String()})
				return true
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].pos < out[j].pos })
	return out
}

// moduleRoot is the directory of the module's root package.
func moduleRoot(m *module) string {
	for _, p := range m.pkgs {
		if p.path == "github.com/mgilbir/pdf0" && len(p.files) > 0 {
			return filepath.Dir(m.fset.Position(p.files[0].Pos()).Filename)
		}
	}
	return ""
}
