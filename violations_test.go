package pdf0

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

// TestViolationInterface is the C45 guard for the finding types: every
// validator's concrete finding satisfies the shared Violation interface and
// reports its rule and object through it, so results from different validators
// can be combined.
func TestViolationInterface(t *testing.T) {
	cases := []Violation{
		ValidationError{Rule: "6.1.3", Level: PDFA2b, Message: "m", Object: 7},
		UAViolation{Clause: "7.4.2", Message: "m", Object: 7},
		PDFXViolation{Rule: "output-intent", Message: "m", Object: 7},
		PDFVTViolation{Rule: "dpart/one", Message: "m", Object: 7},
		PDFRViolation{Rule: "version", Message: "m", Object: 7},
		DPartViolation{Rule: "14.12.2", Message: "m", Object: 7},
	}
	for _, v := range cases {
		if v.RuleID() == "" {
			t.Errorf("%T: empty RuleID", v)
		}
		if v.ObjectNum() != 7 {
			t.Errorf("%T: ObjectNum = %d, want 7", v, v.ObjectNum())
		}
		if !strings.Contains(v.Error(), "m") {
			t.Errorf("%T: Error() %q does not contain the message", v, v.Error())
		}
	}
}

// TestViolationsCombine collects findings from two different validators into
// one []Violation — the interop C45 asked for.
func TestViolationsCombine(t *testing.T) {
	base := buildMinimalPDF()
	doc, err := Read(bytes.NewReader(base), int64(len(base)))
	if err != nil {
		t.Fatal(err)
	}
	var all []Violation
	for _, e := range ValidatePDFA(doc, PDFA2b) {
		all = append(all, e)
	}
	for _, e := range ValidatePDFUA(doc) {
		all = append(all, e)
	}
	if len(all) == 0 {
		t.Fatal("a minimal PDF should violate both PDF/A and PDF/UA rules")
	}
	for _, v := range all {
		if v.RuleID() == "" {
			t.Errorf("%T via interface: empty RuleID", v)
		}
	}
}

// TestValidatorsAreFreeFunctions is the C45 guard for the call shape: the
// validator fleet uses one convention — a free function taking the *Document
// as its first parameter.
func TestValidatorsAreFreeFunctions(t *testing.T) {
	docType := reflect.TypeOf((*Document)(nil))
	for name, fn := range map[string]any{
		"ValidatePDFA":      ValidatePDFA,
		"ValidatePDFABytes": ValidatePDFABytes,
		"ValidatePDFUA":     ValidatePDFUA,
		"ValidatePDFUA2":    ValidatePDFUA2,
		"ValidatePDFX":      ValidatePDFX,
		"ValidatePDFVT":     ValidatePDFVT,
		"ValidatePDFVT2":    ValidatePDFVT2,
		"ValidatePDFR":      ValidatePDFR,
		"ValidateDParts":    ValidateDParts,
		"ValidateFacturX":   ValidateFacturX,
		"ValidateOrderX":    ValidateOrderX,
	} {
		ft := reflect.TypeOf(fn)
		if ft.Kind() != reflect.Func || ft.NumIn() == 0 || ft.In(0) != docType {
			t.Errorf("%s: first parameter is not *Document", name)
		}
	}
}
