package render

import (
	"bytes"
	"strings"
	"testing"

	pdf0 "github.com/mgilbir/pdf0"
)

// text-transform, CSS Text §2.1.
//
// The assertions are about the text that reaches the *display list*, not about
// Box.Text, and that is deliberate: the display list is what becomes the page
// and what a reader extracts, so a transform that changed the box tree and not
// the drawing would satisfy a Box.Text assertion and produce an untransformed
// page. The width assertions are the other half — a transform that changed the
// drawing and not the measurement would leave the lines broken for the original
// text.

// drawn joins the text of every run painted, in painting order.
func drawn(ops []Op) string {
	var out strings.Builder
	for _, op := range ops {
		if t, ok := op.(DrawText); ok {
			out.WriteString(t.Text)
		}
	}
	return out.String()
}

func TestTextTransformChangesTheTextThatIsDrawn(t *testing.T) {
	cases := map[string]string{
		"none":       "one two",
		"uppercase":  "ONE TWO",
		"lowercase":  "one two",
		"capitalize": "One Two",
	}
	for value, want := range cases {
		ops := paintOf(t, `<div id="p">one two</div>`,
			noDefaults+`#p { text-transform: `+value+` }`)
		if got := drawn(ops); got != want {
			t.Errorf("text-transform:%s drew %q, want %q", value, got, want)
		}
	}
}

func TestTextTransformIsMeasuredNotJustDrawn(t *testing.T) {
	// The transform has to happen before the line breaking, or the lines are
	// broken for the text that was written and drawn with the text that was
	// asked for. Courier is fixed pitch, so upper case is no wider — Helvetica is
	// not, and that is what makes this measurable: "iiii" in Helvetica-at-100px
	// is 4 x 22.2 = 88.8px, and "IIII" is 4 x 27.8 = 111.2px. A box 100px wide
	// fits the first on one line and cannot fit the second.
	root := layoutOf(t, 600, `<div id="p">iiii iiii</div>`,
		noDefaults+`#p { font-family: Helvetica; font-size: 100px; width: 100px;
			text-transform: uppercase }`)
	f := find(t, root, "p")
	if len(f.Lines) != 2 {
		t.Fatalf("the uppercased text broke into %d lines, want 2 — it was measured "+
			"as the lower-case text it was written as", len(f.Lines))
	}
	// And the same document without the transform fits each word on its own line
	// too, so the count alone would prove nothing: assert the width the runs
	// actually took.
	if got := f.Lines[0].Runs[0].Width.Px(); got < 111 || got > 112 {
		t.Errorf("the first line's run is %gpx wide, want about 111.2 — four "+
			"upper-case I at 100px", got)
	}
}

func TestCapitalizeCrossesAnElementBoundary(t *testing.T) {
	// A word can span two text nodes. "<b>e</b>xample" is one word, so only the
	// "e" is capitalised — a version that started each node afresh would produce
	// "EXample", which is a real word set wrongly.
	ops := paintOf(t, `<div id="p"><b>e</b>xample two</div>`,
		noDefaults+`#p { text-transform: capitalize }`)
	if got := drawn(ops); got != "Example Two" {
		t.Errorf("capitalize across an element boundary drew %q, want %q",
			got, "Example Two")
	}
}

func TestCapitalizeStartsAfreshInEachBlock(t *testing.T) {
	// A block begins a new line of text, so a word cannot run into it from
	// whatever was written before — and cannot run out of it into whatever comes
	// after. The three documents are the three ways that happens, and they are
	// separate cases rather than one: the first is answered by a reset on either
	// side of a block, and each of the other two by exactly one of them.
	//
	// Written as three because a single "block after block" document passes with
	// either reset alone, which is how the pair was first tested and what a
	// planted defect showed: removing the reset on the way *in* changed nothing.
	cases := []struct{ html, want string }{
		{`<div>hi</div><div>there</div>`, "HiThere"},
		// Inline text, then a block. Only the reset on entering the block can
		// stop the "t" continuing the word "hi".
		{`<div>hi<div>there</div></div>`, "HiThere"},
		// A block, then inline text beside it. Only the reset on leaving the
		// block can stop the "t" continuing "hi".
		{`<div><div>hi</div>there</div>`, "HiThere"},
	}
	for _, c := range cases {
		ops := paintOf(t, c.html, noDefaults+`div { text-transform: capitalize }`)
		if got := drawn(ops); got != c.want {
			t.Errorf("%s drew %q, want %q", c.html, got, c.want)
		}
	}
}

func TestCapitalizeUsesWordBoundariesNotSpaces(t *testing.T) {
	// A hyphen ends a word and an apostrophe does not, which is what browsers
	// produce for both and what makes "don't" come out as a word rather than as
	// "Don'T".
	ops := paintOf(t, `<div id="p">well-known don't x</div>`,
		noDefaults+`#p { text-transform: capitalize }`)
	if got := drawn(ops); got != "Well-Known Don't X" {
		t.Errorf("capitalize drew %q, want %q", got, "Well-Known Don't X")
	}
}

func TestTextTransformIsInherited(t *testing.T) {
	// It inherits, which is how "body { text-transform: uppercase }" works at
	// all. The declaration is on the outer box and the text is two levels down.
	ops := paintOf(t, `<div id="o"><div><span>quiet</span></div></div>`,
		noDefaults+`#o { text-transform: uppercase }`)
	if got := drawn(ops); got != "QUIET" {
		t.Errorf("an inherited text-transform drew %q, want %q", got, "QUIET")
	}
}

func TestTextTransformReachesTheExtractedText(t *testing.T) {
	// The PDF carries what was drawn, so a transformed heading extracts in the
	// case it was set in. This pins the consequence rather than leaving it to be
	// discovered by someone copying text out of a page.
	res, err := Render(Input{
		HTML: `<div id="p">quiet</div>`,
		CSS:  []Stylesheet{{Source: `#p { text-transform: uppercase }`}},
	}, Options{})
	if err != nil {
		t.Fatalf("rendering: %v", err)
	}
	if res.Document == nil {
		t.Fatalf("no document: %v", res.Findings)
	}
	var buf bytes.Buffer
	if err := res.Document.Write(&buf); err != nil {
		t.Fatalf("writing: %v", err)
	}
	doc, err := pdf0.Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	text := doc.ExtractText()
	if !strings.Contains(text, "QUIET") {
		t.Errorf("the page's text is %q; the transformed text is what is drawn "+
			"and so what is extracted", text)
	}
}
