package render_test

import (
	"bytes"
	"testing"

	"github.com/mgilbir/pdf0/render"
)

// This file imports github.com/mgilbir/pdf0/render and nothing else of either
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
	out, err := render.Render(render.Input{
		HTML: `<h1>Aurora</h1><p class="note">Payment within 30 days.</p>`,
		CSS: []render.Stylesheet{{Source: `
			body { font-family: Helvetica; font-size: 11pt }
			h1 { font-size: 20pt; border-bottom: 2pt solid #b8860b }
			.note { font-size: 9pt }`}},
	}, render.Options{Page: render.A5})
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
	page := render.PageSizePt(419.53, 595.28).WithMarginPt(42.52)
	var opts render.Options
	opts.Page = page

	in := render.Input{HTML: `<p>x</p>`}
	in.CSS = append(in.CSS, render.Stylesheet{Source: `p { color: #333333 }`})

	out, err := render.Render(in, opts)
	if err != nil {
		t.Fatalf("rendering: %v", err)
	}
	if out.Document == nil {
		t.Fatalf("no document: %v", out.Findings)
	}
	// Result's own fields are the aliased types too, so this assignment is the
	// other half of the same check.
	var natural render.Size = out.NaturalSize
	var findings []render.Finding = out.Findings
	if natural.W <= 0 {
		t.Errorf("the content reported a natural width of %v", natural.W)
	}
	_ = findings
}

// TestTheNamedSheetsAreDistinct guards against an alias block that compiles and
// says the wrong thing — three names pointing at one sheet would pass every
// test above.
func TestTheNamedSheetsAreDistinct(t *testing.T) {
	sheets := map[string]render.PageSize{
		"A4": render.A4, "A5": render.A5, "Letter": render.Letter,
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
