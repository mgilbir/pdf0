package facturx

import (
	"github.com/mgilbir/pdf0/internal/finding"
	"github.com/mgilbir/pdf0/pdfa"
	"testing"
)

// TestAdoptPDFAFindingsKeepsReservedRulesBare pins the exception in
// adoptPDFAFindings. ValidateFacturX and ValidateOrderX namespace the PDF/A-3
// findings they adopt so that container rules cannot collide with invoice
// rules, but "limit" and "internal" belong to neither namespace: they say the
// checker stopped or crashed, and a caller watching for them keys on the bare
// name. Prefixing one produced "pdfa-3/limit", an identifier nothing documents
// and no predicate recognises — which hides exactly the event these identifiers
// exist to make visible.
func TestAdoptPDFAFindingsKeepsReservedRulesBare(t *testing.T) {
	var out []Violation
	add := func(rule, msg string, obj int) {
		out = append(out, Violation{Rule: rule, Message: msg, Object: obj})
	}
	adoptPDFAFindings(add, "pdfa-3/", []pdfa.Violation{
		{Rule: "6.1.2", Message: "a real PDF/A rule"},
		{Rule: finding.LimitRule, Message: "a guard tripped"},
		{Rule: finding.InternalRule, Message: "a check panicked"},
	})

	want := []string{"pdfa-3/6.1.2", finding.LimitRule, finding.InternalRule}
	if len(out) != len(want) {
		t.Fatalf("got %d findings, want %d: %v", len(out), len(want), out)
	}
	for i, w := range want {
		if out[i].Rule != w {
			t.Errorf("finding %d: rule %q, want %q", i, out[i].Rule, w)
		}
	}
	// The point of keeping them bare is that the exported predicate still
	// recognises them after adoption.
	for _, v := range out[1:] {
		if !finding.IsCheckerFinding(pdfa.Violation{Rule: v.Rule, Message: v.Message}) {
			t.Errorf("adopted %q is no longer recognised as a checker finding", v.Rule)
		}
	}
}
