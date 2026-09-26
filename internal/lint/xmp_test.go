package lint

import (
	"go/ast"
	"go/constant"
	"go/types"
	"sort"
	"strings"
	"testing"
)

// xmpModelPath is the one package allowed to spell XMP markup: the model every
// reader and writer goes through.
const xmpModelPath = "github.com/mgilbir/pdf0/internal/xmp"

// xmpMarkup is markup that only a hand-built packet contains.
var xmpMarkup = []string{"<rdf:", "</rdf:", "x:xmpmeta", "xmlns:"}

// xmpPrefixes are the prefixes a substring scraper searches the packet text
// for.
var xmpPrefixes = []string{"pdfaid:", "pdfuaid:", "pdfxid:", "pdfvtid:", "fx:", "zf:", "dc:", "xmp:", "pdf:", "rdf:", "xmlns:"}

// TestXMPReadAndWrittenThroughTheModel keeps XMP to one reader and one writer
// (internal/xmp).
//
// Incident, audit 2026-09-22 C32/C35/C44/C141: metadata was written by
// building packets from string templates, so every writer regenerated the
// packet and destroyed what other writers had put in it; and it was read by
// searching the packet text for "<pdfaid:conformance>" or `pdfuaid:part="`,
// which read a value out of a comment, missed one written with whitespace
// around "=", assumed the canonical prefix, and compared escaped text with
// unescaped. Two shapes are flagged outside the model package: a string
// constant holding packet markup, and a call into package strings or bytes
// whose constant argument names an XMP property by its prefix.
func TestXMPReadAndWrittenThroughTheModel(t *testing.T) {
	m := load(t)
	var found []string
	for _, p := range m.pkgs {
		if p.path == xmpModelPath {
			continue
		}
		for _, f := range p.files {
			ast.Inspect(f, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.BasicLit:
					tv, ok := p.info.Types[n]
					if !ok || tv.Value == nil || tv.Value.Kind() != constant.String {
						return true
					}
					s := constant.StringVal(tv.Value)
					for _, mk := range xmpMarkup {
						if strings.Contains(s, mk) {
							found = append(found, m.fset.Position(n.Pos()).String()+": a string constant holds XMP markup ("+mk+"); build packets with internal/xmp")
							break
						}
					}
				case *ast.CallExpr:
					sel, ok := n.Fun.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					fn, ok := p.info.Uses[sel.Sel].(*types.Func)
					if !ok || fn.Pkg() == nil || (fn.Pkg().Path() != "strings" && fn.Pkg().Path() != "bytes") {
						return true
					}
					for _, arg := range n.Args {
						tv, ok := p.info.Types[arg]
						if !ok || tv.Value == nil || tv.Value.Kind() != constant.String {
							continue
						}
						s := constant.StringVal(tv.Value)
						for _, pfx := range xmpPrefixes {
							if strings.Contains(s, pfx) {
								found = append(found, m.fset.Position(n.Pos()).String()+": "+fn.Pkg().Path()+"."+fn.Name()+" searches for XMP property text ("+pfx+"); read the property through internal/xmp")
								break
							}
						}
					}
				}
				return true
			})
		}
	}
	sort.Strings(found)
	for _, f := range found {
		t.Error(f)
	}
}
