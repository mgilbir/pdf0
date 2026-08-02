package pdf0

import (
	"context"
	"github.com/mgilbir/pdf0/facturx"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/finding"
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
// afDoc builds a minimal document whose catalog carries one associated-file
// specification for an embedded XML named via /UF (UTF-16) with the given
// relationship and embedded-stream subtype.
// utf16be encodes s as a PDF text string: a UTF-16BE byte-order mark followed by
// big-endian code units, as Unicode file-spec /UF entries are stored.
func utf16be(s string) []byte {
	out := []byte{0xFE, 0xFF}
	for _, r := range s {
		out = append(out, byte(r>>8), byte(r))
	}
	return out
}

func afDoc(ufName string, rel Name, subtype Name) *Document {
	d := &Document{Objects: map[int]*IndirectObject{}, Version: "1.6"}
	stream := &Stream{Dict: Dictionary{}, Data: []byte("<xml/>")}
	stream.Dict.Set("Subtype", subtype)
	d.Objects[10] = &IndirectObject{Number: 10, Value: stream}
	ef := &Dictionary{}
	ef.Set("F", IndirectRef{Number: 10})
	fs := &Dictionary{}
	fs.Set("Type", Name("Filespec"))
	fs.Set("F", String{Value: []byte(ufName)})
	fs.Set("UF", String{Value: utf16be(ufName)})
	fs.Set("AFRelationship", rel)
	fs.Set("EF", ef)
	d.Objects[9] = &IndirectObject{Number: 9, Value: fs}
	cat := &Dictionary{}
	cat.Set("Type", Name("Catalog"))
	cat.Set("AF", Array{IndirectRef{Number: 9}})
	d.Objects[1] = &IndirectObject{Number: 1, Value: cat}
	d.Trailer = Dictionary{}
	d.Trailer.Set("Root", IndirectRef{Number: 1})
	return d
}

func facturxTestDoc(profile formalis.Profile, xml string) *Document {
	d := afDoc("factur-x.xml", "Data", "text/xml")
	d.Objects[10].Value.(*Stream).Data = []byte(xml)
	meta := &Stream{Dict: Dictionary{}, Data: facturx.XMPPacket(profile, "INVOICE", "")}
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
			limit, ok := findRule(v, finding.LimitRule)
			if !ok {
				t.Fatalf("a cancelled run must report a %q finding; got %v", finding.LimitRule, v)
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
				if e.RuleID() != finding.LimitRule {
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
	finding.ReportCancellation(c, out, add)
	if len(out) != 1 || out[0].Rule != finding.LimitRule {
		t.Fatalf("a cancelled run with no finding must get one; got %v", out)
	}
	if !IsCheckerFinding(out[0]) {
		t.Error("the cancellation finding must be a checker finding")
	}
	// A half has already spoken: the poll adds nothing.
	finding.ReportCancellation(c, out, add)
	if len(out) != 1 {
		t.Errorf("the poll must not repeat a cancellation already reported; got %v", out)
	}
	// A live context says nothing at all.
	out = nil
	finding.ReportCancellation(core.NewCanceler(context.Background()), out, add)
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

// TestFacturXCarriesPDFABaseFindings pins the composition itself, not either
// half of it. A Factur-X file shall be PDF/A-3, so ValidateFacturX runs the
// PDF/A-3 validator and adopts its findings under a "pdfa-3/" prefix; the
// container rules live in a package that cannot reach that validator, and are
// handed it per run. Nothing else notices if that hand-off stops happening —
// the container findings still arrive, the report still looks well formed, and
// a document that is not PDF/A-3 at all comes back carrying no sign of it.
//
// The fixture is deliberately a plain PDF, not a conforming PDF/A-3 file: it
// has no XMP identification, no OutputIntent and no document-level structure a
// PDF/A validator would accept, so the base pass has plenty to say.
func TestFacturXCarriesPDFABaseFindings(t *testing.T) {
	d := afDoc("factur-x.xml", "Data", "text/xml")

	n := 0
	for _, v := range ValidateFacturX(d, nil).Violations {
		if strings.HasPrefix(v.Rule, "pdfa-3/") {
			n++
		}
	}
	if n == 0 {
		t.Error("no PDF/A-3 finding reached the Factur-X report: the base validation was not composed in")
	}

	// The Order-X container composes the same base, by the same route.
	n = 0
	for _, v := range ValidateOrderX(d, nil).Violations {
		if strings.HasPrefix(v.Rule, "pdfa-3/") {
			n++
		}
	}
	if n == 0 {
		t.Error("no PDF/A-3 finding reached the Order-X report: the base validation was not composed in")
	}
}
