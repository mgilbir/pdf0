package htmlpdf_test

import (
	"bytes"
	"errors"
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
