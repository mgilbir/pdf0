package facturx

import (
	"context"
	"github.com/mgilbir/formalis"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/finding"
	"github.com/mgilbir/pdf0/object"
	"strings"
	"testing"
)

// facturxTestDoc builds a minimal Factur-X-shaped document: a catalog with an
// associated invoice XML of the caller's choosing and an XMP packet declaring
// the given conformance level, which is what makes the rule engine run at all
// (formalis refuses a profile it does not implement before reading a byte).
func facturxTestDoc(profile formalis.Profile, xml string) core.View {
	d := afDoc("factur-x.xml", "Data", "text/xml")
	d.Objects[10].Value.(*object.Stream).Data = []byte(xml)
	meta := &object.Stream{Dict: object.Dictionary{}, Data: XMPPacket(profile, "INVOICE", "")}
	meta.Dict.Set("Type", object.Name("Metadata"))
	meta.Dict.Set("Subtype", object.Name("XML"))
	d.Objects[20] = &object.IndirectObject{Number: 20, Value: meta}
	cat := d.ResolveDict(d.Trailer.Get("Root"))
	cat.Set("Metadata", object.IndirectRef{Number: 20})
	return d
}

// findRule returns the first finding with the given rule identifier.
func findRule[T violation](v []T, rule string) (T, bool) {
	var zero T
	for _, e := range v {
		if e.RuleID() == rule {
			return e, true
		}
	}
	return zero, false
}

// TestUnreadableInvoiceXMLIsNotAClean Result pins the error half of the formalis
// contract. A document whose attachment is not XML at all comes back as an error
// with the zero Report — no findings, nothing evaluated — so dropping it would
// leave a container whose invoice was never checked indistinguishable from one
// whose invoice passed.
func TestUnreadableInvoiceXMLIsNotACleanResult(t *testing.T) {
	doc := facturxTestDoc(formalis.ProfileEN16931, "<not-xml")
	res := ValidateContext(context.Background(), doc, nil)

	v, ok := findRule(res.Violations, facturxXMLRule)
	if !ok {
		t.Fatalf("an unreadable invoice XML must be reported under %q; got %v", facturxXMLRule, res.Violations)
	}
	if !strings.Contains(v.Message, "could not be read") {
		t.Errorf("the finding must say the XML could not be read; got %q", v.Message)
	}
	// It is a statement about the file, not about pdf0 stopping: a caller
	// filtering checker findings out of a conformance count must still see it.
	if finding.IsCheckerFinding(v) {
		t.Errorf("%q is a container defect, not a checker finding", facturxXMLRule)
	}
	// Nothing was evaluated, and the result says so rather than implying coverage.
	if res.InvoiceComplete {
		t.Error("a run whose invoice could not be read cannot be complete")
	}
	if len(res.InvoiceNotEvaluated) != 0 {
		t.Errorf("the zero Report names no rule families; got %d", len(res.InvoiceNotEvaluated))
	}
}

// TestUnreadableOrderXMLIsNotACleanResult is the Order-X half of the same
// property.
func TestUnreadableOrderXMLIsNotACleanResult(t *testing.T) {
	d := afDoc("order-x.xml", "Data", "text/xml")
	d.Objects[10].Value.(*object.Stream).Data = []byte("<not-xml")
	res := ValidateOrderContext(context.Background(), d, nil)
	if _, ok := findRule(res.Violations, orderXMLRule); !ok {
		t.Fatalf("an unreadable order XML must be reported under %q; got %v", orderXMLRule, res.Violations)
	}
	if res.OrderComplete {
		t.Error("a run whose order could not be read cannot be complete")
	}
}

// TestAdoptedLimitFindingIsACheckerFinding pins the shared reserved identifier
// across the module seam. formalis reports a cancelled or budget-stopped run as
// RuleLimit, pdf0 reports its own guards as finding.LimitRule, and the two are the same
// string on purpose — so an adopted limit finding must come out the far side of
// adoptInvoiceFindings still recognised by IsCheckerFinding. A prefix, a rename
// or a namespace on either side turns "the checker stopped" into "the invoice is
// bad" for every caller filtering the mixed slice.
func TestAdoptedLimitFindingIsACheckerFinding(t *testing.T) {
	if formalis.RuleLimit != finding.LimitRule {
		t.Fatalf("the two modules must spell the reserved rule alike: %q vs %q", formalis.RuleLimit, finding.LimitRule)
	}
	rep := formalis.Report{Violations: []formalis.Violation{
		{Source: formalis.SourceChecker, Rule: formalis.RuleLimit, Severity: formalis.SeverityFatal, Message: "the run was cancelled"},
		{Source: formalis.SourceEN16931, Rule: "BR-01", Severity: formalis.SeverityFatal, Message: "an invoice shall have a number"},
		{Source: formalis.SourceEN16931, Rule: "CII-SR-408", Severity: formalis.SeverityWarning, Message: "advisory"},
	}}

	var fatal, advisory []FacturXViolation
	adoptInvoiceFindings(func(v formalis.Violation, warn bool) {
		f := FacturXViolation{Rule: v.Rule, Message: v.Message, Source: v.Source}
		if warn {
			advisory = append(advisory, f)
			return
		}
		fatal = append(fatal, f)
	}, rep)

	limit, ok := findRule(fatal, finding.LimitRule)
	if !ok {
		t.Fatalf("the adopted findings must keep the bare %q identifier; got %v", finding.LimitRule, fatal)
	}
	if !finding.IsCheckerFinding(limit) {
		t.Errorf("IsCheckerFinding must recognise the adopted limit finding %q", limit.RuleID())
	}
	if br, ok := findRule(fatal, "BR-01"); !ok {
		t.Error("a fatal business rule belongs in the verdict")
	} else if finding.IsCheckerFinding(br) {
		t.Error("a business rule is not a checker finding")
	}
	// The advisory rule is kept, and kept out of the verdict.
	if len(advisory) != 1 || advisory[0].Rule != "CII-SR-408" {
		t.Errorf("the advisory finding must be reported separately; got %v", advisory)
	}
	if _, ok := findRule(fatal, "CII-SR-408"); ok {
		t.Error("an advisory finding must not be counted as a non-conformance")
	}
}
