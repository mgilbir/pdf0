package pdf0

import (
	"context"
	"github.com/mgilbir/pdf0/internal/core"
	"strings"
	"testing"

	"github.com/mgilbir/formalis"
)

// This file pins the properties the Factur-X / Order-X containers acquired when
// they stopped carrying formalis.Violation and grew Context variants: that their
// findings are ordinary pdf0 findings, that a rule engine which could not read
// the attachment cannot leave the result looking clean, that a cancelled run
// says so in the one reserved spelling both modules share, and that what the
// engine did *not* evaluate reaches the caller as data rather than as noise.

// facturxTestDoc builds a minimal Factur-X-shaped document: a catalog with an
// associated invoice XML of the caller's choosing and an XMP packet declaring
// the given conformance level, which is what makes the rule engine run at all
// (formalis refuses a profile it does not implement before reading a byte).
func facturxTestDoc(profile formalis.Profile, xml string) *Document {
	d := afDoc("factur-x.xml", "Data", "text/xml")
	d.Objects[10].Value.(*Stream).Data = []byte(xml)
	meta := &Stream{Dict: Dictionary{}, Data: facturxXMPPacket(profile, "INVOICE", "")}
	meta.Dict.Set("Type", Name("Metadata"))
	meta.Dict.Set("Subtype", Name("XML"))
	d.Objects[20] = &IndirectObject{Number: 20, Value: meta}
	cat := d.ResolveDict(d.Trailer.Get("Root"))
	cat.Set("Metadata", IndirectRef{Number: 20})
	return d
}

// findRule returns the first finding with the given rule identifier.
func findRule[T Violation](v []T, rule string) (T, bool) {
	var zero T
	for _, e := range v {
		if e.RuleID() == rule {
			return e, true
		}
	}
	return zero, false
}

// TestFacturXFindingsSatisfyViolation is the compile-time and run-time half of
// the claim the package documentation used to have to qualify: Factur-X and
// Order-X findings are ordinary pdf0 findings. The interface has no severity, so
// only the fatal half is ever put in Violations (adoptInvoiceFindings).
func TestFacturXFindingsSatisfyViolation(t *testing.T) {
	var all []Violation
	all = append(all, FacturXViolation{Rule: "attachment", Message: "m", Object: 7})
	all = append(all, OrderXViolation{Rule: "ORDER-01", Message: "m", Source: formalis.SourceOrderX})
	if all[0].RuleID() != "attachment" || all[0].ObjectNum() != 7 {
		t.Errorf("FacturXViolation does not report its rule and object: %q/%d", all[0].RuleID(), all[0].ObjectNum())
	}
	if all[1].RuleID() != "ORDER-01" || all[1].ObjectNum() != 0 {
		t.Errorf("OrderXViolation does not report its rule and object: %q/%d", all[1].RuleID(), all[1].ObjectNum())
	}
	// The authority is in the rendering, because a rule identifier is unique only
	// within its Source.
	if got := all[1].Error(); !strings.Contains(got, string(formalis.SourceOrderX)) {
		t.Errorf("an adopted finding must name its authority; got %q", got)
	}
	if got := all[0].Error(); strings.Contains(got, string(formalis.SourceOrderX)) {
		t.Errorf("a container finding has no authority to name; got %q", got)
	}
}

// TestUnreadableInvoiceXMLIsNotAClean Result pins the error half of the formalis
// contract. A document whose attachment is not XML at all comes back as an error
// with the zero Report — no findings, nothing evaluated — so dropping it would
// leave a container whose invoice was never checked indistinguishable from one
// whose invoice passed.
func TestUnreadableInvoiceXMLIsNotACleanResult(t *testing.T) {
	doc := facturxTestDoc(formalis.ProfileEN16931, "<not-xml")
	res := ValidateFacturXContext(context.Background(), doc, nil)

	v, ok := findRule(res.Violations, facturxXMLRule)
	if !ok {
		t.Fatalf("an unreadable invoice XML must be reported under %q; got %v", facturxXMLRule, res.Violations)
	}
	if !strings.Contains(v.Message, "could not be read") {
		t.Errorf("the finding must say the XML could not be read; got %q", v.Message)
	}
	// It is a statement about the file, not about pdf0 stopping: a caller
	// filtering checker findings out of a conformance count must still see it.
	if IsCheckerFinding(v) {
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
	d.Objects[10].Value.(*Stream).Data = []byte("<not-xml")
	res := ValidateOrderXContext(context.Background(), d, nil)
	if _, ok := findRule(res.Violations, orderXMLRule); !ok {
		t.Fatalf("an unreadable order XML must be reported under %q; got %v", orderXMLRule, res.Violations)
	}
	if res.OrderComplete {
		t.Error("a run whose order could not be read cannot be complete")
	}
}

// TestAdoptedLimitFindingIsACheckerFinding pins the shared reserved identifier
// across the module seam. formalis reports a cancelled or budget-stopped run as
// RuleLimit, pdf0 reports its own guards as limitRule, and the two are the same
// string on purpose — so an adopted limit finding must come out the far side of
// adoptInvoiceFindings still recognised by IsCheckerFinding. A prefix, a rename
// or a namespace on either side turns "the checker stopped" into "the invoice is
// bad" for every caller filtering the mixed slice.
func TestAdoptedLimitFindingIsACheckerFinding(t *testing.T) {
	if formalis.RuleLimit != limitRule {
		t.Fatalf("the two modules must spell the reserved rule alike: %q vs %q", formalis.RuleLimit, limitRule)
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

	limit, ok := findRule(fatal, limitRule)
	if !ok {
		t.Fatalf("the adopted findings must keep the bare %q identifier; got %v", limitRule, fatal)
	}
	if !IsCheckerFinding(limit) {
		t.Errorf("IsCheckerFinding must recognise the adopted limit finding %q", limit.RuleID())
	}
	if br, ok := findRule(fatal, "BR-01"); !ok {
		t.Error("a fatal business rule belongs in the verdict")
	} else if IsCheckerFinding(br) {
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

// TestCancelledFacturXIsNeverClean pins the property every pdf0 Context variant
// guarantees, now that these two have one: a cancelled run reports a checker
// finding, so it can be told apart from both a clean container and a
// non-conforming one.
func TestCancelledFacturXIsNeverClean(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	for _, tc := range []struct {
		name string
		run  func() []Violation
	}{
		{"ValidateFacturX", func() []Violation {
			var out []Violation
			for _, v := range ValidateFacturXContext(ctx, facturxTestDoc(formalis.ProfileEN16931, validCII), nil).Violations {
				out = append(out, v)
			}
			return out
		}},
		{"ValidateOrderX", func() []Violation {
			d := afDoc("order-x.xml", "Data", "text/xml")
			d.Objects[10].Value.(*Stream).Data = []byte(validOrderXML)
			var out []Violation
			for _, v := range ValidateOrderXContext(ctx, d, nil).Violations {
				out = append(out, v)
			}
			return out
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := tc.run()
			if len(v) == 0 {
				t.Fatal("a cancelled run must never return an empty result")
			}
			limit, ok := findRule(v, limitRule)
			if !ok {
				t.Fatalf("a cancelled run must report a %q finding; got %v", limitRule, v)
			}
			if !IsCheckerFinding(limit) {
				t.Errorf("%q must be a checker finding", limit.RuleID())
			}
			// Each half that stopped says so in its own words — the container half
			// about the file, the rule engine about the invoice — and neither repeats
			// the other. What must not appear is a third copy from the entry point's
			// own poll, which only speaks when nobody else has.
			seen := map[string]bool{}
			for _, e := range v {
				if e.RuleID() != limitRule {
					continue
				}
				if seen[e.Error()] {
					t.Errorf("the same cancellation was reported twice: %s", e)
				}
				seen[e.Error()] = true
			}
		})
	}
}

// TestReportCancellationOnlySpeaksWhenNobodyElseHas covers the window neither
// composed half can: a run cancelled between the PDF/A-3 validation and the rule
// engine, or one that never reached the rule engine because there was no
// embedded XML to hand it. It is a unit test because that window cannot be hit
// deterministically from the entry point.
func TestReportCancellationOnlySpeaksWhenNobodyElseHas(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := core.NewCanceler(ctx)

	var out []FacturXViolation
	add := func(rule, msg string, obj int) {
		out = append(out, FacturXViolation{Rule: rule, Message: msg, Object: obj})
	}

	// Nobody has spoken: the poll owes the caller the finding.
	reportCancellation(c, out, add)
	if len(out) != 1 || out[0].Rule != limitRule {
		t.Fatalf("a cancelled run with no finding must get one; got %v", out)
	}
	if !IsCheckerFinding(out[0]) {
		t.Error("the cancellation finding must be a checker finding")
	}
	// A half has already spoken: the poll adds nothing.
	reportCancellation(c, out, add)
	if len(out) != 1 {
		t.Errorf("the poll must not repeat a cancellation already reported; got %v", out)
	}
	// A live context says nothing at all.
	out = nil
	reportCancellation(core.NewCanceler(context.Background()), out, add)
	if len(out) != 0 {
		t.Errorf("a run that was not cancelled must report nothing; got %v", out)
	}
}

// TestFacturXReportsWhatItDidNotEvaluate pins the third state formalis's Report
// exists to name. A clean invoice validated against a rule set with published
// gaps is not the same answer as a clean invoice validated against a complete
// one, and the difference has to reach the caller as data — never as a finding
// per gap, which would fire on every invoice ever validated and say nothing
// about any of them.
func TestFacturXReportsWhatItDidNotEvaluate(t *testing.T) {
	// The Order-X rule engine checks five head terms out of a whole document rule
	// set, so it always has a published gap to name.
	d := afDoc("order-x.xml", "Data", "text/xml")
	d.Objects[10].Value.(*Stream).Data = []byte(validOrderXML)
	res := ValidateOrderXContext(context.Background(), d, nil)

	if len(res.OrderNotEvaluated) == 0 {
		t.Fatal("a rule set with published gaps must name them on the result")
	}
	if res.OrderComplete {
		t.Error("a run with an unevaluated family is not complete")
	}
	for _, f := range res.OrderNotEvaluated {
		if f.Rules == "" || f.Reason == "" {
			t.Errorf("an unevaluated family must be usable: %+v", f)
		}
		// A gap is not a finding: nothing stopped, and no rule was broken.
		if _, ok := findRule(res.Violations, f.Rules); ok {
			t.Errorf("an unevaluated family must not be reported as a violation: %q", f.Rules)
		}
	}
	// A clean order still reports no violation about its own coverage.
	for _, v := range res.Violations {
		if strings.Contains(v.Message, "not evaluated") {
			t.Errorf("coverage must not be manufactured into a finding: %s", v)
		}
	}
}
