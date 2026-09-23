package lint

import (
	"go/types"
	"sort"
	"strings"
	"testing"
)

// TestValidatorsHaveOneShape holds the shape docs/validators.md gives every
// conformance validator in the root package: a free function
// ValidateX(doc *Document, …) and a ValidateXContext(ctx context.Context, doc
// *Document, …) with the same parameters after the context and the same
// results.
//
// Incident, audit 2026-09-22 T5/C144: the validators differed in parameter
// name (doc, d, v) and, before the stack settled them, in nil handling and in
// which had a Context variant; each was a small surprise to a caller moving
// from one standard to the next, and nothing held the shape once it was
// fixed.
func TestValidatorsHaveOneShape(t *testing.T) {
	m := load(t)
	for _, p := range m.pkgs {
		if p.path != rootPath {
			continue
		}
		for _, f := range validatorShapeErrors(p.types) {
			t.Error(f)
		}
		return
	}
	t.Fatal("the root package was not loaded")
}

// validatorShapeErrors checks every exported package-level function of pkg
// whose name starts with "Validate" against the shape, with pkg's own
// Document type.
func validatorShapeErrors(pkg *types.Package) []string {
	docObj := pkg.Scope().Lookup("Document")
	if docObj == nil {
		return []string{pkg.Path() + ": no Document type"}
	}
	docPtr := types.NewPointer(docObj.Type())
	isContext := func(t types.Type) bool {
		n, ok := t.(*types.Named)
		return ok && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == "context" && n.Obj().Name() == "Context"
	}
	var out []string
	sigOf := func(name string) *types.Signature {
		if fn, ok := pkg.Scope().Lookup(name).(*types.Func); ok {
			return fn.Type().(*types.Signature)
		}
		return nil
	}
	for _, name := range pkg.Scope().Names() {
		if !strings.HasPrefix(name, "Validate") {
			continue
		}
		sig := sigOf(name)
		if sig == nil || !pkg.Scope().Lookup(name).Exported() {
			continue
		}
		params := sig.Params()
		first := 0
		if base, ok := strings.CutSuffix(name, "Context"); ok {
			if params.Len() == 0 || !isContext(params.At(0).Type()) || params.At(0).Name() != "ctx" {
				out = append(out, name+": the first parameter is not ctx context.Context")
				continue
			}
			first = 1
			bs := sigOf(base)
			if bs == nil {
				out = append(out, name+": no "+base+" without a context")
				continue
			}
			if bs.Params().Len() != params.Len()-1 || !types.Identical(bs.Results(), sig.Results()) {
				out = append(out, name+": its parameters after ctx, or its results, differ from "+base+"'s")
				continue
			}
			for i := 0; i < bs.Params().Len(); i++ {
				a, b := bs.Params().At(i), params.At(i+1)
				if !types.Identical(a.Type(), b.Type()) || a.Name() != b.Name() {
					out = append(out, name+": parameter "+b.Name()+" differs from "+base+"'s "+a.Name())
				}
			}
		} else if sigOf(name+"Context") == nil {
			out = append(out, name+": no "+name+"Context")
		}
		if params.Len() <= first || !types.Identical(params.At(first).Type(), docPtr) || params.At(first).Name() != "doc" {
			out = append(out, name+": the document parameter is not doc *Document")
		}
	}
	sort.Strings(out)
	return out
}

// TestEveryFindingTypeIsAViolation: every exported type of a public package
// whose name ends in "Violation" — a validator's finding — satisfies the root
// package's Violation interface, so findings combine across standards and
// IsCheckerFinding, which reads RuleID, classifies every one of them. The
// root package's list of finding types (violations.go) is written by hand;
// this finds the ones it does not name.
func TestEveryFindingTypeIsAViolation(t *testing.T) {
	m := load(t)
	var iface *types.Interface
	for _, p := range m.pkgs {
		if p.path == rootPath {
			iface, _ = p.types.Scope().Lookup("Violation").Type().Underlying().(*types.Interface)
		}
	}
	if iface == nil {
		t.Fatal("the root package's Violation interface was not found")
	}
	n := 0
	for _, p := range m.pkgs {
		if p.path == rootPath || isInternalPath(p.path) || p.types.Name() == "main" {
			continue
		}
		bad, checked := findingTypesNotViolations(p.types, iface)
		n += checked
		for _, b := range bad {
			t.Error(b)
		}
	}
	// pdfa, pdfua, pdfx, pdfvt, pdfr, dpart, and facturx's two.
	if n < 8 {
		t.Errorf("checked %d finding types; the validators have at least 8", n)
	}
}

// findingTypesNotViolations returns the exported non-interface types of pkg
// named *Violation that do not implement iface, and how many it checked.
func findingTypesNotViolations(pkg *types.Package, iface *types.Interface) (bad []string, checked int) {
	for _, name := range pkg.Scope().Names() {
		tn, ok := pkg.Scope().Lookup(name).(*types.TypeName)
		if !ok || !tn.Exported() || !strings.HasSuffix(name, "Violation") {
			continue
		}
		if _, isIface := tn.Type().Underlying().(*types.Interface); isIface {
			continue
		}
		checked++
		if !types.Implements(tn.Type(), iface) {
			bad = append(bad, pkg.Path()+"."+name+" does not satisfy pdf0.Violation (error, RuleID, ObjectNum)")
		}
	}
	return bad, checked
}

// TestValidatorShapeChecksDetect is both checks' planted bug.
func TestValidatorShapeChecksDetect(t *testing.T) {
	const src = `package snippet

import "context"

type Document struct{}
type Level int

type Violation interface {
	error
	RuleID() string
	ObjectNum() int
}

type GoodViolation struct{ Rule string }

func (v GoodViolation) Error() string  { return v.Rule }
func (v GoodViolation) RuleID() string { return v.Rule }
func (v GoodViolation) ObjectNum() int { return 0 }

type BadViolation struct{ Clause string }

func (v BadViolation) Error() string { return v.Clause }

func ValidateGood(doc *Document, level Level) []GoodViolation { return nil }
func ValidateGoodContext(ctx context.Context, doc *Document, level Level) []GoodViolation {
	return nil
}
func ValidateNamedD(d *Document) []GoodViolation { return nil }
func ValidateNamedDContext(ctx context.Context, d *Document) []GoodViolation { return nil }
func ValidateAlone(doc *Document) []GoodViolation { return nil }
func ValidateResults(doc *Document) []GoodViolation { return nil }
func ValidateResultsContext(ctx context.Context, doc *Document) []BadViolation { return nil }
func ValidateOrphanContext(ctx context.Context, doc *Document) []GoodViolation { return nil }
func validateUnexported(d *Document) {}
`
	_, p, _ := typeCheckModuleSnippet(t, src, "context")
	got := validatorShapeErrors(p.types)
	want := []string{
		"ValidateAlone: no ValidateAloneContext",
		"ValidateNamedD: the document parameter is not doc *Document",
		"ValidateNamedDContext: the document parameter is not doc *Document",
		"ValidateOrphanContext: no ValidateOrphan without a context",
		"ValidateResultsContext: its parameters after ctx, or its results, differ from ValidateResults's",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("shape errors:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	iface := p.types.Scope().Lookup("Violation").Type().Underlying().(*types.Interface)
	bad, checked := findingTypesNotViolations(p.types, iface)
	if checked != 2 || len(bad) != 1 || !strings.Contains(bad[0], ".BadViolation ") {
		t.Errorf("finding types: checked %d, flagged %v; want 2 checked and BadViolation flagged", checked, bad)
	}
}
