package pdf0

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/finding"
	"github.com/mgilbir/pdf0/pdfa"
	"github.com/mgilbir/pdf0/pdfx"
)

// nilDocumentValidators calls every package-level validator with a nil
// document and returns its findings as errors.
var nilDocumentValidators = map[string]func() []error{
	"ValidatePDFA":             func() []error { return errs(ValidatePDFA(nil, pdfa.PDFA2b)) },
	"ValidatePDFAContext":      func() []error { return errs(ValidatePDFAContext(context.Background(), nil, pdfa.PDFA2b)) },
	"ValidatePDFABytes":        func() []error { return errs(ValidatePDFABytes(nil, pdfa.PDFA2b, nil)) },
	"ValidatePDFABytesContext": func() []error { return errs(ValidatePDFABytesContext(context.Background(), nil, pdfa.PDFA2b, nil)) },
	"ValidatePDFUA":            func() []error { return errs(ValidatePDFUA(nil)) },
	"ValidatePDFUAContext":     func() []error { return errs(ValidatePDFUAContext(context.Background(), nil)) },
	"ValidatePDFUA2":           func() []error { return errs(ValidatePDFUA2(nil)) },
	"ValidatePDFUA2Context":    func() []error { return errs(ValidatePDFUA2Context(context.Background(), nil)) },
	"ValidatePDFX":             func() []error { return errs(ValidatePDFX(nil, pdfx.PDFX4)) },
	"ValidatePDFXContext":      func() []error { return errs(ValidatePDFXContext(context.Background(), nil, pdfx.PDFX4)) },
	"ValidatePDFVT":            func() []error { return errs(ValidatePDFVT(nil)) },
	"ValidatePDFVTContext":     func() []error { return errs(ValidatePDFVTContext(context.Background(), nil)) },
	"ValidatePDFVT2":           func() []error { return errs(ValidatePDFVT2(nil)) },
	"ValidatePDFVT2Context":    func() []error { return errs(ValidatePDFVT2Context(context.Background(), nil)) },
	"ValidatePDFR":             func() []error { return errs(ValidatePDFR(nil)) },
	"ValidatePDFRContext":      func() []error { return errs(ValidatePDFRContext(context.Background(), nil)) },
	"ValidateDParts":           func() []error { return errs(ValidateDParts(nil)) },
	"ValidateDPartsContext":    func() []error { return errs(ValidateDPartsContext(context.Background(), nil)) },
	"ValidateFacturX":          func() []error { return errs(ValidateFacturX(nil, nil).Violations) },
	"ValidateFacturXContext":   func() []error { return errs(ValidateFacturXContext(context.Background(), nil, nil).Violations) },
	"ValidateOrderX":           func() []error { return errs(ValidateOrderX(nil, nil).Violations) },
	"ValidateOrderXContext":    func() []error { return errs(ValidateOrderXContext(context.Background(), nil, nil).Violations) },
}

func errs[T error](vs []T) []error {
	out := make([]error, len(vs))
	for i, v := range vs {
		out[i] = v
	}
	return out
}

// TestEveryValidatorAnswersANilDocument is C144: every validator answers a
// nil document as ValidatePDFA does — one checker finding under "limit" — and
// none panics. The list of validators is read from the package's own source,
// so a validator added without an entry here fails the test.
func TestEveryValidatorAnswersANilDocument(t *testing.T) {
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var declared []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		af, err := parser.ParseFile(fset, f, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range af.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil && fd.Name.IsExported() && strings.HasPrefix(fd.Name.Name, "Validate") {
				declared = append(declared, fd.Name.Name)
			}
		}
	}
	sort.Strings(declared)
	if len(declared) == 0 {
		t.Fatal("found no validators in the package source")
	}
	for _, name := range declared {
		call, ok := nilDocumentValidators[name]
		if !ok {
			t.Errorf("%s has no nil-document case in this test", name)
			continue
		}
		var got []error
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s(nil) panicked: %v", name, r)
				}
			}()
			got = call()
		}()
		if len(got) != 1 {
			t.Errorf("%s(nil) = %v, want exactly one finding", name, got)
			continue
		}
		v, ok := got[0].(finding.V)
		if !ok || !finding.IsCheckerFinding(v) || !strings.Contains(got[0].Error(), nilDocumentMessage) {
			t.Errorf("%s(nil) = %v, want the %q checker finding", name, got[0], nilDocumentMessage)
		}
	}
}
