package lint

import (
	"go/ast"
	"go/token"
	"go/types"
	"sort"
	"strings"
	"testing"
)

const corePath = "github.com/mgilbir/pdf0/internal/core"

// lineComments maps each line of f to the text of the comments on it.
func lineComments(fset *token.FileSet, f *ast.File) map[int]string {
	out := map[int]string{}
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			line := fset.Position(c.Slash).Line
			out[line] += c.Text
		}
	}
	return out
}

// isCoreType reports whether t is the named type core.<name>.
func isCoreType(t types.Type, name string) bool {
	n, ok := t.(*types.Named)
	if !ok {
		return false
	}
	obj := n.Obj()
	return obj.Pkg() != nil && obj.Pkg().Path() == corePath && obj.Name() == name
}

// TestProducerReasonIsNotDropped keeps the typed producer outcomes honest
// (audit 2026-09-22 T2).
//
// Incident: every producer that turns file data into something a check reads —
// View.Content, MetadataContent, LoadCMap, the ToUnicode parsers,
// ICCProfileData, DecodeCIDSet, the object-stream decoder — answered failure
// with nil. The same nil meant "the file is broken" and "pdf0 declined to
// look" (a limit, an unimplemented filter, ciphertext), and its consumers
// guessed: some skipped, so the verdict looked clean with nothing checked
// (C46, C63, C109); some asserted, so a file was reported for pdf0's own limit
// (C47, C49). Producers now return a core.Reason and record the trip
// themselves. What is left for a consumer to get wrong is to throw the Reason
// away and then read a nil as "empty".
//
// So a core.Reason may not be discarded silently. Assigning it to _, or calling
// a producer for nothing, needs a "// reason: …" comment on the same line
// saying why ignoring it is sound — typically "presence-only: a check that
// finds nothing asserts nothing, and the producer recorded any trip".
func TestProducerReasonIsNotDropped(t *testing.T) {
	m := load(t)
	var found []string
	for _, p := range m.pkgs {
		for _, f := range p.files {
			comments := lineComments(m.fset, f)
			justified := func(n ast.Node) bool {
				return strings.Contains(comments[m.fset.Position(n.Pos()).Line], "reason:")
			}
			reasonAt := func(call ast.Expr) []int {
				tv, ok := p.info.Types[call]
				if !ok {
					return nil
				}
				var at []int
				switch rt := tv.Type.(type) {
				case *types.Tuple:
					for i := 0; i < rt.Len(); i++ {
						if isCoreType(rt.At(i).Type(), "Reason") {
							at = append(at, i)
						}
					}
				default:
					if isCoreType(rt, "Reason") {
						at = append(at, 0)
					}
				}
				return at
			}
			ast.Inspect(f, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.AssignStmt:
					if len(n.Rhs) != 1 {
						return true
					}
					call, ok := n.Rhs[0].(*ast.CallExpr)
					if !ok {
						return true
					}
					for _, i := range reasonAt(call) {
						if i >= len(n.Lhs) {
							continue
						}
						if id, ok := n.Lhs[i].(*ast.Ident); ok && id.Name == "_" && !justified(n) {
							found = append(found, m.fset.Position(n.Pos()).String()+": a core.Reason is discarded with no \"// reason:\" comment saying why that is sound")
						}
					}
				case *ast.ExprStmt:
					if call, ok := n.X.(*ast.CallExpr); ok && len(reasonAt(call)) > 0 && !justified(n) {
						found = append(found, m.fset.Position(n.Pos()).String()+": a producer's result, core.Reason included, is discarded")
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

// rawDecoders are the decode functions that return an error and record
// nothing. A validator that calls one gets data or nil and must classify and
// report the failure itself — which is what every validator forgot, once.
var rawDecoders = map[string]bool{
	"DecodeStreamData": true, "ApplyFilter": true, "FlateDecode": true, "LZWDecode": true,
}

// TestValidatorsDecodeThroughProducers keeps a validator from decoding a
// stream past the producers that record what they decline (audit 2026-09-22
// T2). facturx decoded the invoice with core.DecodeStreamData and classified
// the error itself; pdfa's XMP well-formedness check decoded it and, on any
// error, judged the still-compressed bytes as XML. Validators decode through
// View.Decode, Content, ICCProfileData and the rest.
func TestValidatorsDecodeThroughProducers(t *testing.T) {
	m := load(t)
	var found []string
	for _, p := range m.pkgs {
		if !validatorPaths[p.path] && p.path != "github.com/mgilbir/pdf0/sign" {
			continue
		}
		for _, f := range p.files {
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				fn, ok := p.info.Uses[sel.Sel].(*types.Func)
				if ok && fn.Pkg() != nil && fn.Pkg().Path() == corePath && rawDecoders[fn.Name()] {
					found = append(found, m.fset.Position(call.Pos()).String()+": core."+fn.Name()+" records nothing it declines; decode through a View producer")
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

// validatorPaths are the packages whose checks read strings out of a document
// that may be Locked.
var validatorPaths = map[string]bool{
	"github.com/mgilbir/pdf0/pdfa":    true,
	"github.com/mgilbir/pdf0/pdfua":   true,
	"github.com/mgilbir/pdf0/pdfx":    true,
	"github.com/mgilbir/pdf0/pdfvt":   true,
	"github.com/mgilbir/pdf0/pdfr":    true,
	"github.com/mgilbir/pdf0/dpart":   true,
	"github.com/mgilbir/pdf0/facturx": true,
}

// TestValidatorsReadStringsThroughStringValue keeps ciphertext out of the
// checks (audit 2026-09-22 C63).
//
// Incident: a document that is encrypted and was not decrypted is Locked, and
// every string in it is ciphertext. PDF/UA read the catalog /Lang out of one
// as it would out of any document, judged the ciphertext as a language tag,
// and reported every such file for an invalid /Lang. The producer that knows
// is core.View.StringValue, which says ReasonLocked; a type assertion to
// object.String does not know. So a validator package may not assert to
// object.String, except where the string is one ISO 32000-2 7.6.2 leaves
// unencrypted (the file identifier, a signature's /Contents) and a
// "// string: …" comment on the same line says so.
func TestValidatorsReadStringsThroughStringValue(t *testing.T) {
	m := load(t)
	var found []string
	for _, p := range m.pkgs {
		if !validatorPaths[p.path] {
			continue
		}
		for _, f := range p.files {
			comments := lineComments(m.fset, f)
			ast.Inspect(f, func(n ast.Node) bool {
				ta, ok := n.(*ast.TypeAssertExpr)
				if !ok || ta.Type == nil {
					return true
				}
				tv, ok := p.info.Types[ta.Type]
				if !ok {
					return true
				}
				named, ok := tv.Type.(*types.Named)
				if !ok || named.Obj().Pkg() == nil || named.Obj().Pkg().Path() != "github.com/mgilbir/pdf0/object" || named.Obj().Name() != "String" {
					return true
				}
				if !strings.Contains(comments[m.fset.Position(ta.Pos()).Line], "string:") {
					found = append(found, m.fset.Position(ta.Pos()).String()+": a string is read by type assertion; read it with View.StringValue, which says when it is ciphertext")
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
