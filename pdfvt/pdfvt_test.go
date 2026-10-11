package pdfvt

import (
	"testing"

	"github.com/mgilbir/pdf0/dpart"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/finding"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfx"
)

// panickingNested returns what a nested validator returns when one of its
// checks panics: its own Guarded boundary recovers the panic into an "internal"
// finding, alongside an ordinary finding reported before it and a "limit"
// finding standing for a guard trip on the shared recorder.
func panickingNested(add func(rule, msg string, obj int)) {
	add("some-rule", "an ordinary finding", 7)
	add(finding.LimitRule, "a guard tripped", 0)
	finding.Guarded(add, func() { panic("boom") })
}

// TestNestedCheckerFindingsKeepTheirReservedNames pins docs audit 2026-10-11
// D25. PDF/VT composes the PDF/X and DPart validators and namespaces their
// findings ("pdfx-4/", "pdfx-5/", "dpart/"). A recovered panic inside either
// used to come out as "pdfx-4/internal" or "dpart/internal", which
// IsCheckerFinding does not recognise, so a crash in pdf0 read as a
// non-conformance of the document.
func TestNestedCheckerFindingsKeepTheirReservedNames(t *testing.T) {
	savedX, savedD := pdfxValidateView, dpartValidateView
	t.Cleanup(func() { pdfxValidateView, dpartValidateView = savedX, savedD })
	pdfxValidateView = func(core.View, pdfx.Level) []pdfx.Violation {
		var out []pdfx.Violation
		panickingNested(func(rule, msg string, obj int) {
			out = append(out, pdfx.Violation{Rule: rule, Message: msg, Object: obj})
		})
		return out
	}
	dpartValidateView = func(core.View) []dpart.Violation {
		var out []dpart.Violation
		panickingNested(func(rule, msg string, obj int) {
			out = append(out, dpart.Violation{Rule: rule, Message: msg, Object: obj})
		})
		return out
	}

	for _, tc := range []struct{ part, base string }{{"1", "pdfx-4/"}, {"2", "pdfx-5/"}} {
		t.Run("PDF/VT-"+tc.part, func(t *testing.T) {
			v := core.View{
				Objects: map[int]*object.IndirectObject{},
				Trailer: &object.Dictionary{},
				Limits:  core.DefaultLimits(),
				Run:     core.NewRun(&core.Recorder{}),
			}
			got := map[string]int{}
			for _, f := range validateView(v, tc.part) {
				got[f.Rule]++
			}
			// One "internal" per nested panic, under the bare name: the two
			// nested validators each recovered one, and nothing else saw them.
			if got[finding.InternalRule] != 2 {
				t.Errorf("bare %q findings = %d, want 2 (one per nested panic); rules: %v", finding.InternalRule, got[finding.InternalRule], got)
			}
			for _, prefix := range []string{tc.base, "dpart/"} {
				if n := got[prefix+finding.InternalRule]; n != 0 {
					t.Errorf("%q reported %d times; a checker finding must keep its reserved name", prefix+finding.InternalRule, n)
				}
				if n := got[prefix+finding.LimitRule]; n != 0 {
					t.Errorf("%q reported %d times", prefix+finding.LimitRule, n)
				}
				// Ordinary findings are still namespaced.
				if got[prefix+"some-rule"] != 1 {
					t.Errorf("%q = %d, want 1; rules: %v", prefix+"some-rule", got[prefix+"some-rule"], got)
				}
			}
			// The nested runs share this run's recorder, which reports their
			// guard trips once at the boundary (pdfvt_api.go); adopting the
			// nested "limit" findings as well would count each trip twice.
			if n := got[finding.LimitRule]; n != 0 {
				t.Errorf("nested %q findings adopted %d times; the shared recorder reports them", finding.LimitRule, n)
			}
		})
	}
}
