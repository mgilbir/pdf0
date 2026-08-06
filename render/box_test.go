package render

import (
	"strings"
	"testing"
)

// The box tree.
//
// These run whole documents through parse, style and box construction rather
// than against hand-built trees, and that is deliberate: the faults this stage
// has are about the *interaction* between the cascade and the tree — a display
// value that did not reach an element, an anonymous box generated for whitespace
// nobody wrote — and a hand-built input assumes away exactly the step that goes
// wrong.

// sketch renders a box tree as indented text, so a table reads as the shape
// being asserted.
func sketchBox(b *Box) string {
	var out strings.Builder
	var walk func(*Box, int)
	walk = func(cur *Box, depth int) {
		out.WriteString(strings.Repeat("  ", depth))
		switch {
		case cur.IsText():
			out.WriteString("text " + quoted(cur.Text))
		case cur.Anonymous():
			out.WriteString("anonymous " + cur.Outer.String())
			if cur.Inner != InnerFlow {
				// The boxes §17.2.1 and §17.4 insert differ only in their inner
				// display, so an anonymous row and an anonymous cell would
				// otherwise read identically.
				out.WriteString("/" + cur.Inner.String())
			}
		default:
			name := cur.Element.Name
			out.WriteString(name + " " + cur.Outer.String())
			if cur.Inner != InnerFlow {
				out.WriteString("/" + cur.Inner.String())
			}
			if cur.ListItem {
				out.WriteString(" list-item")
			}
		}
		out.WriteString("\n")
		for _, c := range cur.Children {
			walk(c, depth+1)
		}
	}
	if b == nil {
		return "(no boxes)\n"
	}
	walk(b, 0)
	return out.String()
}

// quoted renders text with its newlines visible, since a preserved newline and
// a collapsed one are exactly what several of these tests are about.
func quoted(s string) string {
	return "\"" + strings.ReplaceAll(s, "\n", "\\n") + "\""
}

func build(t *testing.T, htmlSrc string, cssSrc ...string) Built {
	t.Helper()
	in := Input{HTML: htmlSrc}
	for _, c := range cssSrc {
		in.CSS = append(in.CSS, Stylesheet{Source: c})
	}
	return Build(in)
}

// bodyBoxes returns the box tree under <body>, which is what most of these are
// about — the html and head frames are the same in every document.
func bodyBoxes(t *testing.T, htmlSrc string, cssSrc ...string) string {
	t.Helper()
	got := build(t, htmlSrc, cssSrc...)
	if got.Root == nil {
		return "(no boxes)\n"
	}
	var body *Box
	var find func(*Box)
	find = func(b *Box) {
		if body != nil {
			return
		}
		if b.Element != nil && b.Element.Name == "body" {
			body = b
			return
		}
		for _, c := range b.Children {
			find(c)
		}
	}
	find(got.Root)
	if body == nil {
		t.Fatalf("the document produced no body box:\n%s", sketchBox(got.Root))
	}
	var out strings.Builder
	for _, c := range body.Children {
		out.WriteString(sketchBox(c))
	}
	return out.String()
}

// TestDisplayNoneProducesNothing pins that display:none removes the subtree
// entirely rather than making an invisible box. Nothing inside is laid out,
// measured or painted, which is the whole difference from visibility:hidden.
func TestDisplayNoneProducesNothing(t *testing.T) {
	got := bodyBoxes(t, `<p>kept</p><div style="display: none"><p>gone</p></div>`)
	if strings.Contains(got, "gone") {
		t.Errorf("display:none left content behind:\n%s", got)
	}
	if !strings.Contains(got, "kept") {
		t.Errorf("display:none removed a sibling:\n%s", got)
	}

	// The head is display:none in the user-agent sheet, so its content never
	// reaches the box tree either.
	full := build(t, "<title>t</title><p>x</p>")
	if strings.Contains(sketchBox(full.Root), "title") {
		t.Errorf("the head produced boxes:\n%s", sketchBox(full.Root))
	}
}

// TestUserAgentDisplayDefaults pins that the default stylesheet reaches the box
// tree. Without it every element would be inline and the whole document one
// paragraph.
func TestUserAgentDisplayDefaults(t *testing.T) {
	cases := map[string]Outer{
		"<div>x</div>":        OuterBlock,
		"<p>x</p>":            OuterBlock,
		"<h1>x</h1>":          OuterBlock,
		"<ul><li>x</li></ul>": OuterBlock,
		"<span>x</span>":      OuterInline,
		"<em>x</em>":          OuterInline,
		"<a href=x>y</a>":     OuterInline,
	}
	for src, want := range cases {
		got := build(t, src)
		var found *Box
		var walk func(*Box)
		walk = func(b *Box) {
			if found != nil || b == nil {
				return
			}
			if b.Element != nil && b.Element.Name != "html" && b.Element.Name != "body" {
				found = b
				return
			}
			for _, c := range b.Children {
				walk(c)
			}
		}
		walk(got.Root)
		if found == nil {
			t.Errorf("%q produced no box for its element", src)
			continue
		}
		if found.Outer != want {
			t.Errorf("%q made <%s> %v, want %v", src, found.Element.Name, found.Outer, want)
		}
	}
}

// TestListItem pins that <li> carries the marker flag, which is what makes it
// different from a plain block and is easy to lose when display is read as a
// single keyword.
func TestListItem(t *testing.T) {
	got := build(t, "<ul><li>a</li></ul>")
	var li *Box
	var walk func(*Box)
	walk = func(b *Box) {
		if b == nil || li != nil {
			return
		}
		if b.Element != nil && b.Element.Name == "li" {
			li = b
			return
		}
		for _, c := range b.Children {
			walk(c)
		}
	}
	walk(got.Root)
	if li == nil {
		t.Fatal("no box for the list item")
	}
	if !li.ListItem {
		t.Error("the list item does not generate a marker")
	}
	if li.Outer != OuterBlock {
		t.Errorf("the list item is %v, want block", li.Outer)
	}
}

// TestDisplayIsTheOuterInnerPair pins the model. Reading display as a single
// keyword makes inline-block a special case everywhere; reading it as a pair
// makes it inline outside and flow-root inside, which is what it is.
func TestDisplayIsTheOuterInnerPair(t *testing.T) {
	cases := map[string]struct {
		outer Outer
		inner Inner
	}{
		"block":        {OuterBlock, InnerFlow},
		"inline":       {OuterInline, InnerFlow},
		"inline-block": {OuterInline, InnerFlowRoot},
		"flow-root":    {OuterBlock, InnerFlowRoot},
		"flex":         {OuterBlock, InnerFlex},
		"inline-flex":  {OuterInline, InnerFlex},
		"table":        {OuterBlock, InnerTable},
		"inline-table": {OuterInline, InnerTable},
		"table-cell":   {OuterBlock, InnerTableCell},
		// The two-value syntax says the same things.
		"block flow":       {OuterBlock, InnerFlow},
		"inline flow-root": {OuterInline, InnerFlowRoot},
		"block flex":       {OuterBlock, InnerFlex},
	}
	for value, want := range cases {
		got := build(t, `<div id="t" style="display: `+value+`">x</div>`)
		var box *Box
		var walk func(*Box)
		walk = func(b *Box) {
			if b == nil || box != nil {
				return
			}
			if b.Element != nil {
				if id, _ := b.Element.Attr("id"); id == "t" {
					box = b
					return
				}
			}
			for _, c := range b.Children {
				walk(c)
			}
		}
		walk(got.Root)
		if box == nil {
			t.Errorf("display:%s produced no box", value)
			continue
		}
		outer := box.Outer
		if box.Parent != nil && box.Parent.TableWrapper {
			// §17.4 splits a table into two boxes, and the outer half of the
			// display value goes to the anonymous wrapper: an inline-table is an
			// inline-level wrapper holding a block-level table box. Asking the
			// table box alone would say "block" for both values and stop
			// distinguishing them.
			outer = box.Parent.Outer
		}
		if outer != want.outer || box.Inner != want.inner {
			t.Errorf("display:%s is %v/%v, want %v/%v",
				value, outer, box.Inner, want.outer, want.inner)
		}
	}
}

// TestAnonymousBlockBoxes pins CSS Display §2.1, the rule that keeps block
// layout writable: a block container's children are either all block-level or
// all inline-level, never a mixture.
func TestAnonymousBlockBoxes(t *testing.T) {
	// Text beside a block child is wrapped.
	got := bodyBoxes(t, `<div>loose text<p>a block</p>more text</div>`)
	want := `div block
  anonymous block
    text "loose text"
  p block
    text "a block"
  anonymous block
    text "more text"
`
	if got != want {
		t.Errorf("mixed content\ngot:\n%swant:\n%s", got, want)
	}

	// All-inline content needs no anonymous box: the div is simply an inline
	// formatting context.
	got = bodyBoxes(t, `<div>just <em>text</em> here</div>`)
	want = `div block
  text "just "
  em inline
    text "text"
  text " here"
`
	if got != want {
		t.Errorf("all-inline content\ngot:\n%swant:\n%s", got, want)
	}

	// All-block content needs none either.
	got = bodyBoxes(t, `<div><p>a</p><p>b</p></div>`)
	want = `div block
  p block
    text "a"
  p block
    text "b"
`
	if got != want {
		t.Errorf("all-block content\ngot:\n%swant:\n%s", got, want)
	}
}

// TestBlockInInlineSplits pins CSS 2.1 §9.2.1.1, which is common markup rather
// than a corner: an <a> wrapping a card of block content is the everyday case.
//
// A block inside an inline does not nest. The inline is *split* — an inline
// piece holding what came before, then the block, then a second piece holding
// what came after, all siblings of whatever the inline was a child of. Leaving
// the block where it was written would put it in an inline formatting context,
// where nothing in block layout knows how to place it.
func TestBlockInInlineSplits(t *testing.T) {
	got := bodyBoxes(t, `<div>a<span>before<p>block</p>after</span>b</div>`)
	want := `div block
  anonymous block
    text "a"
    span inline
      text "before"
  p block
    text "block"
  anonymous block
    span inline
      text "after"
    text "b"
`
	if got != want {
		t.Errorf("got:\n%swant:\n%s", got, want)
	}
}

// TestBlockInInlineDropsEmptyPieces pins that a split which leaves an inline
// piece with nothing in it produces no piece at all. Two empty spans either side
// of the block would generate line boxes, and those have a height the author
// never asked for.
func TestBlockInInlineDropsEmptyPieces(t *testing.T) {
	got := bodyBoxes(t, `<div><span><p>only</p></span></div>`)
	want := `div block
  p block
    text "only"
`
	if got != want {
		t.Errorf("got:\n%swant:\n%s", got, want)
	}
}

// TestNestedInlinesSplitToo pins that the split reaches through more than one
// level of inline, since the block may be several deep.
func TestNestedInlinesSplitToo(t *testing.T) {
	got := bodyBoxes(t, `<div><em>x<strong>y<p>b</p>z</strong>w</em></div>`)
	want := `div block
  anonymous block
    em inline
      text "x"
      strong inline
        text "y"
  p block
    text "b"
  anonymous block
    em inline
      strong inline
        text "z"
      text "w"
`
	if got != want {
		t.Errorf("got:\n%swant:\n%s", got, want)
	}
}

// TestInlineWithoutABlockIsNotSplit pins that the rule only fires when it has to
// — an ordinary inline keeps its single box, and splitting one that did not need
// it would draw its border twice.
func TestInlineWithoutABlockIsNotSplit(t *testing.T) {
	got := bodyBoxes(t, `<div>a<span>b<em>c</em>d</span>e</div>`)
	want := `div block
  text "a"
  span inline
    text "b"
    em inline
      text "c"
    text "d"
  text "e"
`
	if got != want {
		t.Errorf("got:\n%swant:\n%s", got, want)
	}
}

// TestSplitPiecesKeepTheirStyle pins that both halves are still the element they
// came from. A border on the split inline is drawn on each piece, which is what
// a browser does and looks odd until you know why — but a piece that lost its
// style would silently drop the styling from half the content.
func TestSplitPiecesKeepTheirStyle(t *testing.T) {
	built := build(t, `<div>a<span id="s">before<p>b</p>after</span></div>`,
		"#s { color: rgb(1, 2, 3) }")

	var pieces int
	var walk func(*Box)
	walk = func(b *Box) {
		if b.Element != nil {
			if id, _ := b.Element.Attr("id"); id == "s" {
				pieces++
				if b.Style["color"] != "rgb(1, 2, 3)" {
					t.Errorf("a split piece has colour %q, not the span's", b.Style["color"])
				}
			}
		}
		for _, c := range b.Children {
			walk(c)
		}
	}
	walk(built.Root)
	if pieces != 2 {
		t.Errorf("the span became %d pieces, want 2", pieces)
	}
}

// TestInlineBlockIsABlockContainer pins the box that is inline on the outside
// and a block container on the inside, which is the whole of what flow-root
// means. Its children get the anonymous block treatment; an ordinary inline's
// do not.
func TestInlineBlockIsABlockContainer(t *testing.T) {
	// Mixed content inside an inline-block is wrapped, exactly as it would be
	// inside a <div>.
	got := bodyBoxes(t, `<div><span id="ib">loose<p>block</p></span></div>`,
		"#ib { display: inline-block }")
	want := `div block
  ib inline/flow-root
    anonymous block
      text "loose"
    p block
      text "block"
`
	// The sketch names elements by tag, so fix the expectation to the tag.
	want = strings.ReplaceAll(want, "ib inline/flow-root", "span inline/flow-root")
	if got != want {
		t.Errorf("got:\n%swant:\n%s", got, want)
	}

	// An inline-block *shields* what is inside it. A block within one belongs to
	// it, so an inline wrapping that inline-block has no reason to split — and
	// splitting it would tear apart markup that was perfectly well formed.
	got = bodyBoxes(t, `<div><em>a<span id="ib"><p>x</p></span>b</em></div>`,
		"#ib { display: inline-block }")
	shielded := `div block
  em inline
    text "a"
    span inline/flow-root
      p block
        text "x"
    text "b"
`
	if got != shielded {
		t.Errorf("an inline-block did not shield the block inside it:\n%swant:\n%s",
			got, shielded)
	}

	// The same markup with an ordinary inline splits instead, which is the
	// contrast that makes the assertion above about flow-root and not about
	// spans.
	got = bodyBoxes(t, `<div><span id="ib">loose<p>block</p></span></div>`)
	if strings.Contains(got, "flow-root") {
		t.Errorf("an ordinary inline became a block container:\n%s", got)
	}
	if !strings.Contains(got, "p block") {
		t.Errorf("the block was not lifted out of the inline:\n%s", got)
	}
}

// TestWhitespaceBetweenBlocksMakesNoBox is the case that would otherwise put a
// blank line into every well-formatted document. The newlines between two <p>
// elements are text nodes, and wrapping each in an anonymous block would give
// the page two lines the author cannot see in the markup.
func TestWhitespaceBetweenBlocksMakesNoBox(t *testing.T) {
	got := bodyBoxes(t, "<div>\n  <p>a</p>\n  <p>b</p>\n</div>")
	want := `div block
  p block
    text "a"
  p block
    text "b"
`
	if got != want {
		t.Errorf("got:\n%swant:\n%s", got, want)
	}
}

// TestWhitespaceCollapsing pins CSS Text §4. The rules differ per white-space
// value, and each difference is one an author chose deliberately.
func TestWhitespaceCollapsing(t *testing.T) {
	cases := []struct {
		whiteSpace string
		in, want   string
	}{
		{"normal", "a   b", "a b"},
		{"normal", "a\n\nb", "a b"},
		{"normal", "a \t\n b", "a b"},
		{"normal", "  a  ", " a "},
		{"nowrap", "a   b", "a b"},
		// pre keeps everything, which is the whole point of it.
		{"pre", "a   b", "a   b"},
		{"pre", "a\n\nb", "a\n\nb"},
		{"pre-wrap", "a   b", "a   b"},
		// pre-line collapses spaces and keeps newlines.
		{"pre-line", "a   b", "a b"},
		// Each newline, not one per run. CSS Text §3's table says "New Lines:
		// Preserve" for pre-line, and preserve means every one of them: an
		// engine that emitted a single break per run of white space would close
		// up every paragraph gap in a document written with blank lines, which
		// is the one thing pre-line is used for. This assertion previously read
		// "a\nb" and was wrong.
		{"pre-line", "a\n\nb", "a\n\nb"},
		// Rule 1 of §4.1.1: the collapsible spaces and tabs around a segment
		// break are removed *before* anything else looks at them, so the break
		// does not also leave the space that was written beside it.
		{"pre-line", "a  \n  b", "a\nb"},
		{"pre-line", "a \t \n \t b", "a\nb"},

		// A CRLF is one segment break and not two. This engine's HTML parser
		// does not fold it, so a <pre> written on Windows would otherwise gain
		// a blank line between every pair of its own.
		{"pre", "a\r\nb", "a\nb"},
		{"pre", "a\rb", "a\nb"},
		{"pre-line", "a\r\n\r\nb", "a\n\nb"},
		{"normal", "a\r\nb", "a b"},

		// The segment break transformation's one exception: a break against a
		// zero-width space is removed rather than becoming a space, so an
		// author who hard-wrapped their source at a marked break opportunity
		// does not also get a space they never wrote.
		{"normal", "a​\nb", "a​b"},
		{"normal", "a\n​b", "a​b"},
		{"normal", "a\nb", "a b"},

		// break-spaces preserves everything pre-wrap does; the two differ in
		// what happens at a line edge, not in Phase I.
		{"break-spaces", "a \t b", "a \t b"},
	}
	for _, tc := range cases {
		if got := collapseWhitespace(tc.in, tc.whiteSpace); got != tc.want {
			t.Errorf("white-space:%s on %q gave %q, want %q",
				tc.whiteSpace, tc.in, got, tc.want)
		}
	}

	// A no-break space is not collapsible white space, which is the entire
	// reason an author writes one.
	if got := collapseWhitespace("a  b", "normal"); got != "a  b" {
		t.Errorf("no-break spaces were collapsed: %q", got)
	}
}

// TestPreIsPreformattedThroughTheUserAgentSheet pins that <pre> gets its
// white-space from the default stylesheet, so the collapsing above is driven by
// the cascade rather than by a special case in the box builder.
func TestPreIsPreformattedThroughTheUserAgentSheet(t *testing.T) {
	got := bodyBoxes(t, "<pre>a   b\nc</pre>")
	if !strings.Contains(got, `text "a   b\nc"`) {
		t.Errorf("<pre> collapsed its whitespace:\n%s", got)
	}
	// And an ordinary block does collapse, so the difference is real.
	got = bodyBoxes(t, "<div>a   b</div>")
	if !strings.Contains(got, `text "a b"`) {
		t.Errorf("<div> did not collapse its whitespace:\n%s", got)
	}
}

// TestAuthorCSSBeatsTheUserAgentSheet pins that the defaults are in the cascade
// rather than beside it — an author who writes "p { display: inline }" is
// fighting a stylesheet, and wins for the reason everything else does.
func TestAuthorCSSBeatsTheUserAgentSheet(t *testing.T) {
	got := bodyBoxes(t, "<p>x</p>", "p { display: inline }")
	if !strings.Contains(got, "p inline") {
		t.Errorf("the author's display did not win:\n%s", got)
	}
	// And without the author rule it is a block, so the test is about the
	// cascade and not about the default being absent.
	got = bodyBoxes(t, "<p>x</p>")
	if !strings.Contains(got, "p block") {
		t.Errorf("the default display did not apply:\n%s", got)
	}
}

// TestOriginBeatsSpecificity pins that the user-agent sheet is in the cascade at
// its own origin, not merely earlier in the same one.
//
// The distinction needs a case where the author's rule is *less* specific than
// the default it overrides, or order alone would explain the result — which is
// what the first version of this test did, and it passed with author sheets
// mislabelled as user-agent ones.
//
// The default is "h1 { font-weight: bold }" at (0,0,1). The author writes "*" at
// (0,0,0) and must still win.
func TestOriginBeatsSpecificity(t *testing.T) {
	got := build(t, "<h1>x</h1>", "* { font-weight: normal }")

	var h1 *Box
	var walk func(*Box)
	walk = func(b *Box) {
		if b == nil || h1 != nil {
			return
		}
		if b.Element != nil && b.Element.Name == "h1" {
			h1 = b
			return
		}
		for _, c := range b.Children {
			walk(c)
		}
	}
	walk(got.Root)
	if h1 == nil {
		t.Fatal("no box for the heading")
	}
	if v := h1.Style["font-weight"]; v != "normal" {
		t.Errorf("font-weight is %q; a less specific author rule must still beat "+
			"the user-agent default", v)
	}

	// Without the author rule the default applies, so this is about origin and
	// not about the default being absent.
	got = build(t, "<h1>x</h1>")
	h1 = nil
	walk(got.Root)
	if v := h1.Style["font-weight"]; v != "bold" {
		t.Errorf("the default font-weight is %q, want bold", v)
	}
}

// TestStyleElementIsCollected pins that a <style> in the document is an author
// stylesheet like any other. Nothing else in these tests uses one, so without
// this the whole path from markup to cascade goes unexercised.
func TestStyleElementIsCollected(t *testing.T) {
	got := bodyBoxes(t, "<style>p { display: inline }</style><p>x</p>")
	if !strings.Contains(got, "p inline") {
		t.Errorf("a <style> element did not reach the cascade:\n%s", got)
	}

	// Its own content is not laid out — <style> is display:none in the defaults
	// — so the rule applies and the text of it does not appear.
	if strings.Contains(got, "display: inline") {
		t.Errorf("the stylesheet's own text was laid out:\n%s", got)
	}

	// And it arrives at the *author* origin, not merely later in the
	// user-agent one. As in TestOriginBeatsSpecificity this needs a rule less
	// specific than the default it beats, or order alone would explain it —
	// which the first version of this test could not tell apart.
	full := build(t, "<style>* { font-weight: normal }</style><h1>x</h1>")
	var h1 *Box
	var walk func(*Box)
	walk = func(b *Box) {
		if b == nil || h1 != nil {
			return
		}
		if b.Element != nil && b.Element.Name == "h1" {
			h1 = b
			return
		}
		for _, c := range b.Children {
			walk(c)
		}
	}
	walk(full.Root)
	if h1 == nil {
		t.Fatal("no box for the heading")
	}
	if v := h1.Style["font-weight"]; v != "normal" {
		t.Errorf("font-weight is %q; a <style> element is an author stylesheet "+
			"and must beat the user-agent default", v)
	}
}

// TestDisplayContentsIsReported pins that a value the engine reads and cannot
// honour is named. Treating it as inline is the closest available answer and is
// wrong in a way that shows — the box takes part in layout when the author asked
// for it not to — so it must not be silent.
func TestDisplayContentsIsReported(t *testing.T) {
	got := build(t, `<div id="wrap" style="display: contents"><p>x</p></div>`)

	var found *Finding
	for i := range got.Findings {
		if got.Findings[i].Rule == RuleUnsupportedValue {
			found = &got.Findings[i]
		}
	}
	if found == nil {
		t.Fatalf("display:contents was not reported; findings were %v", got.Findings)
	}
	if found.Property != "display" {
		t.Errorf("the finding names property %q", found.Property)
	}
	// It names which element, since a stylesheet may set it on many.
	if !strings.Contains(found.Path, "div#wrap") {
		t.Errorf("the finding's path is %q, which does not name the element", found.Path)
	}
}

// TestBoxTreeIsBounded is the security property: a document is untrusted, and
// anonymous box generation can produce more boxes than there were elements, so
// the html package's own cap does not bound this.
//
// The cap is lowered for the test rather than the document raised to meet it.
// The first version of this built five thousand boxes against a cap of a
// million and asserted the count was under it, which passed with the cap
// removed entirely — a bound that is never reached is never tested.
func TestBoxTreeIsBounded(t *testing.T) {
	defer func(prev int) { maxBoxes = prev }(maxBoxes)
	maxBoxes = 50

	var b strings.Builder
	for i := 0; i < 500; i++ {
		b.WriteString("<span>x</span>")
	}
	got := build(t, "<div>"+b.String()+"</div>")
	if got.Root == nil {
		t.Fatal("a large document produced no boxes at all")
	}
	n := countBoxes(got.Root)
	if n > maxBoxes+16 {
		t.Errorf("built %d boxes, past the cap of %d", n, maxBoxes)
	}
	// And the trip is reported: a document silently cut off is one that looks
	// finished and is not.
	var said bool
	for _, f := range got.Findings {
		if f.Rule == RuleLimit {
			said = true
		}
	}
	if !said {
		t.Errorf("the box cap fired at %d boxes and said nothing: %v", n, got.Findings)
	}
}

func countBoxes(b *Box) int {
	if b == nil {
		return 0
	}
	n := 1
	for _, c := range b.Children {
		n += countBoxes(c)
	}
	return n
}

// TestParentLinksAreConsistent pins that the tree is a tree after anonymous
// boxes have been inserted — the step most likely to leave a child pointing at
// the parent it used to have.
func TestParentLinksAreConsistent(t *testing.T) {
	got := build(t, `<div>loose<p>block</p>more</div><section><span>a</span> b</section>`)
	var walk func(*Box)
	walk = func(b *Box) {
		for _, c := range b.Children {
			if c.Parent != b {
				name := "anonymous"
				if c.Element != nil {
					name = c.Element.Name
				}
				t.Errorf("the %s box does not point at its parent", name)
			}
			walk(c)
		}
	}
	if got.Root != nil {
		walk(got.Root)
	}
}

// TestBuildIsTotal pins that no document and stylesheet combination panics.
func TestBuildIsTotal(t *testing.T) {
	docs := []string{
		"", "<p>x</p>", "<div><p>a</p>b<p>c</p></div>",
		"<ul><li>a<li>b</ul>", "<table><tr><td>x</table>",
		"<span><div>block in inline</div></span>",
		"<pre>a\n b</pre>", "<div>" + strings.Repeat("<span>x</span>", 200) + "</div>",
	}
	sheets := []string{
		"", "* { display: none }", "* { display: block }", "* { display: inline }",
		"* { display: contents }", "p { display: nonsense }",
		"div { display: table-cell }", "* { display: flex }",
		"html { display: none }",
	}
	for _, d := range docs {
		for _, s := range sheets {
			got := build(t, d, s)
			if got.Root != nil {
				countBoxes(got.Root)
			}
		}
	}
}

// TestDisplayNoneOnTheRootProducesNoBoxes pins the one case where a whole
// document legitimately produces nothing, so a caller can tell it apart from a
// failure.
func TestDisplayNoneOnTheRootProducesNoBoxes(t *testing.T) {
	got := build(t, "<p>x</p>", "html { display: none }")
	if got.Root != nil {
		t.Errorf("html{display:none} still produced boxes:\n%s", sketchBox(got.Root))
	}
	// And it is not reported as a failure, because it is not one.
	if got.Failed {
		t.Error("a document that asks to produce nothing was treated as a failure")
	}
}
