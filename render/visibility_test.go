package render

import (
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/style"
)

// visibility, CSS 2.1 §11.2.
//
// "nothing was painted" is true for a great many wrong reasons — a box that was
// never laid out, a colour that did not parse, a document that produced no boxes
// at all — so none of these counts marks. Each one names the specific op it
// expects to be absent and, in the same document, a specific op it expects to
// still be there.

// filled reports whether a rectangle of exactly this colour was painted.
func filled(ops []Op, want style.RGBA) int {
	n := 0
	for _, op := range ops {
		if r, ok := op.(FillRect); ok && r.Color == want && !r.Rect.Empty() {
			n++
		}
	}
	return n
}

// drew reports how many runs of text were drawn.
func drew(ops []Op, text string) int {
	n := 0
	for _, op := range ops {
		if t, ok := op.(DrawText); ok && t.Text == text {
			n++
		}
	}
	return n
}

var (
	red   = style.RGBA{R: 255, A: 1}
	green = style.RGBA{G: 128, A: 1}
)

const visCSS = `#h { background-color: #ff0000 } #v { background-color: #008000 }`

func TestHiddenBoxPaintsNothingOfItsOwn(t *testing.T) {
	// The hidden box has a background and a border and text, and none of the
	// three reaches the page. The visible sibling is in the same document and
	// must, or the test would pass on a document that laid nothing out at all.
	ops := paintOf(t,
		`<div id="h">gone</div><div id="v">here</div>`,
		noDefaults+visCSS+` #h { visibility: hidden; height: 10px;
		   border-top-style: solid; border-top-width: 4px; border-top-color: #0000ff }`)

	if n := filled(ops, red); n != 0 {
		t.Errorf("a hidden box painted its background %d times", n)
	}
	if n := filled(ops, style.RGBA{B: 255, A: 1}); n != 0 {
		t.Errorf("a hidden box painted its border %d times", n)
	}
	if n := drew(ops, "gone"); n != 0 {
		t.Errorf("a hidden box drew its text %d times", n)
	}
	if n := filled(ops, green); n != 1 {
		t.Errorf("the visible sibling painted its background %d times, want 1", n)
	}
	if n := drew(ops, "here"); n != 1 {
		t.Errorf("the visible sibling drew its text %d times, want 1", n)
	}
}

func TestHiddenBoxStillOccupiesItsSpace(t *testing.T) {
	// The difference between "visibility: hidden" and "display: none", asserted
	// as a position rather than as a presence. The hidden box is 40px tall, so
	// its sibling starts at 40 — at 0 it would mean the box had been pruned.
	root := layoutOf(t, 600,
		`<div id="h">x</div><div id="v">y</div>`,
		noDefaults+` #h { visibility: hidden; height: 40px }`)
	body := find(t, root, "v")
	px(t, "the box after a hidden one", body.BorderRect.Y, 40)
}

func TestVisibleDescendantReappearsInsideAHiddenAncestor(t *testing.T) {
	// visibility inherits, and a descendant may set it back. This is why the
	// property cannot be implemented by pruning the box tree: the subtree is
	// hidden and one box inside it is not.
	ops := paintOf(t,
		`<div id="h"><div id="v">here</div></div>`,
		noDefaults+visCSS+` #h { visibility: hidden; height: 30px }
		 #v { visibility: visible; height: 10px }`)

	if n := filled(ops, red); n != 0 {
		t.Errorf("the hidden ancestor painted its background %d times", n)
	}
	if n := filled(ops, green); n != 1 {
		t.Errorf("the visible descendant painted its background %d times, want 1", n)
	}
	if n := drew(ops, "here"); n != 1 {
		t.Errorf("the visible descendant drew its text %d times, want 1", n)
	}
}

func TestVisibilityIsAskedPerRunWithinALine(t *testing.T) {
	// A line box holds runs from several inline boxes, each with its own
	// visibility. A version that asked the block once would hide the whole line
	// or none of it.
	ops := paintOf(t,
		`<div id="p">shown<span id="s">gone</span></div>`,
		noDefaults+` #s { visibility: hidden }`)

	if n := drew(ops, "shown"); n != 1 {
		t.Errorf("the visible run was drawn %d times, want 1", n)
	}
	if n := drew(ops, "gone"); n != 0 {
		t.Errorf("the hidden run inside the same line was drawn %d times", n)
	}
}

func TestHiddenListItemDrawsNoMarker(t *testing.T) {
	// The marker is generated rather than written, so it is painted by a
	// different path from the text and needs its own answer.
	ops := paintOf(t, `<ul><li id="i">one</li></ul>`,
		noDefaults+` #i { visibility: hidden }`)
	for _, op := range ops {
		if t2, ok := op.(DrawText); ok && strings.Contains(t2.Text, "•") {
			t.Fatalf("a hidden list item drew its bullet")
		}
	}
}

func TestCollapseOnATableRowIsReported(t *testing.T) {
	// "collapse" removes a row and closes the table up. This engine draws it as
	// "hidden", so the space stays — a table with a gap in it, which looks
	// deliberate. It is reported rather than left to be discovered.
	rec := NewRecorder(nil)
	built := Build(Input{
		HTML: `<table><tr id="r"><td>a</td></tr></table>`,
		CSS:  []Stylesheet{{Source: `#r { visibility: collapse }`}},
	})
	Layout(built.Root, Size{W: picPx(600), H: picPx(10000)}, nil, rec)

	found := 0
	for _, f := range rec.Findings() {
		if f.Property == "visibility" {
			found++
		}
	}
	if found != 1 {
		t.Errorf("\"visibility: collapse\" on a row was reported %d times, want once", found)
	}
}
