package htmlpdf_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/htmlpdf"
)

// This file imports github.com/mgilbir/pdf0/htmlpdf and nothing else of either
// module's, deliberately, and it is an external test package so that it can
// only reach what a caller can reach.
//
// That is the assertion. Rendering a page is what this package exists for, and
// until the aliases in api.go every name in the call below came from
// github.com/mgilbir/forme/layout — so the headline use took two imports and a
// working knowledge of which module declared which type. A name dropped from
// api.go stops this compiling, which is the only way to notice.

// TestTheHeadlineCallNeedsOneImport is HTML, CSS and a page size going in and a
// PDF coming out, written the way a caller would write it.
func TestTheHeadlineCallNeedsOneImport(t *testing.T) {
	out, err := htmlpdf.Render(htmlpdf.Input{
		HTML: `<h1>Aurora</h1><p class="note">Payment within 30 days.</p>`,
		CSS: []htmlpdf.Stylesheet{{Source: `
			body { font-family: Helvetica; font-size: 11pt }
			h1 { font-size: 20pt; border-bottom: 2pt solid #b8860b }
			.note { font-size: 9pt }`}},
	}, htmlpdf.Options{Page: htmlpdf.A5})
	if err != nil {
		t.Fatalf("rendering: %v", err)
	}
	if out.Document == nil {
		t.Fatalf("no document was produced: %v", out.Findings)
	}
	if len(out.Findings) != 0 {
		t.Errorf("an ordinary document raised %v", out.Findings)
	}

	var buf bytes.Buffer
	if err := out.Document.Write(&buf); err != nil {
		t.Fatalf("writing: %v", err)
	}
	if !bytes.HasPrefix(buf.Bytes(), []byte("%PDF-")) {
		t.Errorf("the bytes written do not begin with a PDF header: %q",
			buf.Bytes()[:min(16, buf.Len())])
	}
}

// TestTheAliasesAreTheSameTypes is what makes them aliases worth having rather
// than a second API to keep in step.
//
// A caller that outgrows the shortlist imports layout and finds its values fit
// with no conversion. If any of these were defined types instead of aliases,
// this would not compile — which is the point of writing it down.
func TestTheAliasesAreTheSameTypes(t *testing.T) {
	// Built through the alias, read through the alias's own field names, and
	// handed to the same call: a defined type would need a conversion here.
	page := htmlpdf.PageSizePt(419.53, 595.28).WithMarginPt(42.52)
	var opts htmlpdf.Options
	opts.Page = page

	in := htmlpdf.Input{HTML: `<p>x</p>`}
	in.CSS = append(in.CSS, htmlpdf.Stylesheet{Source: `p { color: #333333 }`})

	out, err := htmlpdf.Render(in, opts)
	if err != nil {
		t.Fatalf("rendering: %v", err)
	}
	if out.Document == nil {
		t.Fatalf("no document: %v", out.Findings)
	}
	// Result's own fields are the aliased types too, so this assignment is the
	// other half of the same check.
	var natural htmlpdf.Size = out.NaturalSize
	var findings []htmlpdf.Finding = out.Findings
	if natural.W <= 0 {
		t.Errorf("the content reported a natural width of %v", natural.W)
	}
	_ = findings
}

// TestTheNamedSheetsAreDistinct guards against an alias block that compiles and
// says the wrong thing — three names pointing at one sheet would pass every
// test above.
func TestTheNamedSheetsAreDistinct(t *testing.T) {
	sheets := map[string]htmlpdf.PageSize{
		"A4": htmlpdf.A4, "A5": htmlpdf.A5, "Letter": htmlpdf.Letter,
	}
	for name, s := range sheets {
		if s.Width <= 0 || s.Height <= 0 {
			t.Errorf("%s is %v x %v", name, s.Width, s.Height)
		}
		if s.Height <= s.Width {
			t.Errorf("%s is %v x %v, which is not portrait", name, s.Width, s.Height)
		}
	}
	if sheets["A4"].Width == sheets["A5"].Width {
		t.Error("A4 and A5 are the same width")
	}
	if sheets["Letter"].Width == sheets["A4"].Width {
		t.Error("Letter and A4 are the same width")
	}
	// A5 is half of A4, which is the whole idea of the A series: the long side
	// halves and the short side becomes the new long one.
	if got, want := sheets["A4"].Width.Px(), sheets["A5"].Height.Px(); !within(got, want, 0.5) {
		t.Errorf("A4's width is %v and A5's height %v; they should be the same edge", got, want)
	}
}

func within(a, b, tol float64) bool { return a-b < tol && b-a < tol }

// How Render says no.
//
// There is one channel and it is the error, and the tests below are mostly
// about that being true rather than about any particular message. The shape
// before this was a nil Document with a nil error, which reads reasonably in a
// doc comment and produces a nil dereference in the five lines everybody
// actually writes.

// refusable is a document the engine will not produce: three-point text is
// below the legibility floor Options.MinFontSizePt puts under scale-to-fit.
const refusable = `<p>far too small to read</p>`

const refusableCSS = `p { font-size: 3pt; font-family: Helvetica }`

// TestARefusedDocumentIsAnError is the change itself.
func TestARefusedDocumentIsAnError(t *testing.T) {
	out, err := htmlpdf.Render(htmlpdf.Input{
		HTML: refusable,
		CSS:  []htmlpdf.Stylesheet{{Source: refusableCSS}},
	}, htmlpdf.Options{Page: htmlpdf.A4})

	if err == nil {
		t.Fatal("three-point text produced a document with no error")
	}
	var refused *htmlpdf.RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("the error is %T (%v), want a *htmlpdf.RefusedError", err, err)
	}
	if len(refused.Findings) == 0 {
		t.Error("the refusal carries no findings, so nothing says why")
	}
	// The message has to name a reason: a caller that only logs err must learn
	// something from it.
	if !strings.Contains(err.Error(), "min-font-size") {
		t.Errorf("the message is %q and does not name the rule that fired", err)
	}
	// And the Result still came back, which is what makes a refusal worth
	// catching rather than just reporting.
	if out.NaturalSize.W <= 0 {
		t.Errorf("a refused render reported a natural width of %v; the page was "+
			"laid out, which is how the rule fired at all", out.NaturalSize.W)
	}
	if len(out.Findings) == 0 {
		t.Error("a refused render reported no findings on the Result")
	}
}

// TestTheErrorAndTheDocumentNeverDisagree is the invariant the whole shape
// rests on, and the reason a caller needs only one check.
//
// Both directions, over a document that renders and one that does not: an error
// with a document would make the error ignorable, and a nil error with no
// document is the trap this replaced.
func TestTheErrorAndTheDocumentNeverDisagree(t *testing.T) {
	cases := []struct {
		name, html, css string
		wantDoc         bool
	}{
		{"an ordinary page", `<h1>Aurora</h1>`, `h1 { font-size: 20pt }`, true},
		{"text below the legibility floor", refusable, refusableCSS, false},
	}
	for _, c := range cases {
		out, err := htmlpdf.Render(htmlpdf.Input{
			HTML: c.html,
			CSS:  []htmlpdf.Stylesheet{{Source: c.css}},
		}, htmlpdf.Options{Page: htmlpdf.A4})

		if got := out.Document != nil; got != c.wantDoc {
			t.Errorf("%s: got a document = %v, want %v (findings %v)", c.name, got, c.wantDoc, out.Findings)
		}
		if (err == nil) != (out.Document != nil) {
			t.Errorf("%s: err is %v and Document != nil is %v — the two must agree, "+
				"or a caller has to check both", c.name, err, out.Document != nil)
		}
	}
}

// TestCheckingOnlyTheErrorIsEnough writes the five lines a caller writes, over
// a document that is refused. Before the change this panicked.
func TestCheckingOnlyTheErrorIsEnough(t *testing.T) {
	write := func(html, css string) error {
		out, err := htmlpdf.Render(htmlpdf.Input{
			HTML: html,
			CSS:  []htmlpdf.Stylesheet{{Source: css}},
		}, htmlpdf.Options{Page: htmlpdf.A4})
		if err != nil {
			return err
		}
		return out.Document.Write(io.Discard)
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("the ordinary caller shape panicked: %v", r)
		}
	}()
	if err := write(refusable, refusableCSS); err == nil {
		t.Error("a refused document reported no error to the caller")
	}
	if err := write(`<h1>Aurora</h1>`, `h1 { font-size: 20pt }`); err != nil {
		t.Errorf("an ordinary document failed: %v", err)
	}
}

// TestFindingsAreNotRefusals: a document can be produced and still be worth
// complaining about, so the two must not be the same signal.
func TestFindingsAreNotRefusals(t *testing.T) {
	// A property the engine does not implement is a warning, not a refusal.
	out, err := htmlpdf.Render(htmlpdf.Input{
		HTML: `<p>text</p>`,
		CSS:  []htmlpdf.Stylesheet{{Source: `p { font-family: Helvetica; mix-blend-mode: multiply }`}},
	}, htmlpdf.Options{Page: htmlpdf.A4})
	if err != nil {
		t.Fatalf("an unimplemented property refused the document: %v", err)
	}
	if out.Document == nil {
		t.Fatal("no document, but no error either")
	}
	if len(out.Findings) == 0 {
		t.Error("an unimplemented property raised nothing; this test needs a " +
			"property the engine reports, or it is asserting nothing")
	}
}

// TestADocumentCanActuallyTruncateItsReport is the end-to-end case that was
// missing, and the reason the field was kept when nothing could reach it.
//
// Truncated used to be unreachable from here. The engine deduplicates findings
// hard enough that a flood of two thousand at-rules produces 201 of them
// against a limit of five hundred, so the flag was carried, asserted only
// through a value built by hand, and documented as a gap — kept rather than
// dropped because the ceiling was incidental rather than a promise, and a
// backend without the flag would one day present a cut list as a complete one.
//
// That day arrived. glyph-missing is reported per run of text, so a document
// with two thousand distinct characters no standard face has fills the report
// and overruns it. Both halves are asserted: that the list stops at the bound,
// and that the document says so rather than looking complete.
func TestADocumentCanActuallyTruncateItsReport(t *testing.T) {
	// Each paragraph holds one CJK ideograph, which none of the fourteen
	// standard faces has a glyph for. Distinct characters at distinct places,
	// so the recorder folds none of them into another.
	var doc strings.Builder
	for i := 0; i < 2000; i++ {
		fmt.Fprintf(&doc, "<p>%c</p>", rune(0x4E00+i))
	}

	out, err := htmlpdf.Render(htmlpdf.Input{HTML: doc.String()},
		htmlpdf.Options{Page: htmlpdf.A4, MinScale: 0.01})

	if !out.Truncated {
		t.Fatalf("%d findings and no truncation; this test needs the report to "+
			"overflow or it asserts nothing about the flag", len(out.Findings))
	}
	if len(out.Findings) >= 2000 {
		t.Errorf("%d findings for 2000 problems — the list did not stop at the "+
			"bound, so Truncated is describing something else", len(out.Findings))
	}

	// A document the engine could not set is refused, and the refusal carries
	// the same warning: the reason may not be in the list it hands back.
	var refused *htmlpdf.RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("a document with two thousand missing glyphs was not refused: %v", err)
	}
	if !refused.Truncated {
		t.Error("the Result says its report was cut and the error does not")
	}
	if !strings.Contains(refused.Error(), "cut at the reporting limit") {
		t.Errorf("a refusal whose reasons were cut reads %q, which sends a "+
			"reader looking for a reason that is not there", refused)
	}

	// And the other way, so the flag is not simply always on: an ordinary
	// document reports nothing cut.
	clean, cleanErr := htmlpdf.Render(htmlpdf.Input{HTML: "<p>Hello.</p>"},
		htmlpdf.Options{Page: htmlpdf.A4})
	if cleanErr != nil {
		t.Fatalf("a one-paragraph document was refused: %v", cleanErr)
	}
	if clean.Truncated {
		t.Error("a one-paragraph document reported a truncated report")
	}
}

// TestARefusalSaysSoWhenItCannotSayWhy is the case the message used to call
// unreachable.
//
// The engine counts a rule the moment it fires and only then tries to record
// it, so a document that trips enough rules to fill the report can be refused
// by one the limit dropped. The refusal is still right; the list just cannot
// explain it. A message of "refused" and nothing else would send a reader
// looking through the findings for a reason that is not in them.
func TestARefusalSaysSoWhenItCannotSayWhy(t *testing.T) {
	err := &htmlpdf.RefusedError{
		Findings:  []htmlpdf.Finding{{Rule: "unsupported-property", Message: "not implemented"}},
		Truncated: true,
	}
	if !strings.Contains(err.Error(), "cut at the reporting limit") {
		t.Errorf("a refusal whose reason was cut says %q, which sends a reader "+
			"looking for a reason that is not there", err)
	}

	// And with room in the list, the reason is named rather than hedged.
	err = &htmlpdf.RefusedError{Findings: []htmlpdf.Finding{
		{Rule: "min-font-size", Message: "3pt", Severity: htmlpdf.Error},
	}}
	got := err.Error()
	if !strings.Contains(got, "min-font-size") || strings.Contains(got, "cut at") {
		t.Errorf("a refusal with its reason in the list says %q", got)
	}
}

// TestACutListIsNotReportedAsATotal is the defect the test above found.
//
// Error() mentioned the cut only when *no* error finding survived the bound.
// With findings present it said "(and 499 more)" — a precise count of a list
// that had been cut, for a document with two thousand problems in it. The
// number is a floor, and reading it as a total is the mistake the flag exists
// to prevent.
func TestACutListIsNotReportedAsATotal(t *testing.T) {
	many := []htmlpdf.Finding{
		{Rule: "glyph-missing", Message: "no glyph for U+4E00", Severity: htmlpdf.Error},
		{Rule: "glyph-missing", Message: "no glyph for U+4E01", Severity: htmlpdf.Error},
		{Rule: "glyph-missing", Message: "no glyph for U+4E02", Severity: htmlpdf.Error},
	}

	cut := (&htmlpdf.RefusedError{Findings: many, Truncated: true}).Error()
	if !strings.Contains(cut, "at least") || !strings.Contains(cut, "cut at the reporting limit") {
		t.Errorf("a cut list reports %q, which reads as a total", cut)
	}

	// With the whole list in hand the count is exact, and saying "at least"
	// would be hedging about something known.
	whole := (&htmlpdf.RefusedError{Findings: many}).Error()
	if strings.Contains(whole, "at least") || strings.Contains(whole, "cut at") {
		t.Errorf("a complete list reports %q, which hedges a count it knows", whole)
	}
	if !strings.Contains(whole, "and 2 more") {
		t.Errorf("a complete list of three reports %q, want the other two counted", whole)
	}

	// And the single-reason case, which has no count to qualify and so has to
	// say it in words.
	one := []htmlpdf.Finding{{Rule: "min-scale", Message: "32%", Severity: htmlpdf.Error}}
	cut1 := (&htmlpdf.RefusedError{Findings: one, Truncated: true}).Error()
	if !strings.Contains(cut1, "cut at the reporting limit") {
		t.Errorf("one surviving reason out of a cut list reports %q, which reads "+
			"as though it were the only one", cut1)
	}
	if whole1 := (&htmlpdf.RefusedError{Findings: one}).Error(); strings.Contains(whole1, "cut at") {
		t.Errorf("the only reason there was reports %q", whole1)
	}
}
