package render

import (
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/style"
)

// List markers, and the two overflow guardrails of §6.2.
//
// A marker is nothing the document contains: no part of "<li>one</li>" says a
// bullet, and the number in an ordered list is the item's position among its
// siblings, which an item does not know about itself. So this is a test of
// something generated rather than of something transformed, and the numbering
// cases are where that shows.

// markersOf returns the marker text of every list item in a document, in order.
func markersOf(t *testing.T, htmlSrc string, cssSrc ...string) []string {
	t.Helper()
	root := layoutOf(t, 600, htmlSrc, cssSrc...)
	var out []string
	var walk func(*Fragment)
	walk = func(f *Fragment) {
		if f.Marker != nil {
			out = append(out, f.Marker.Text)
		}
		for _, c := range f.Children {
			walk(c)
		}
	}
	walk(root)
	return out
}

// TestBulletsAndNumbers pins what each list-style-type generates.
func TestBulletsAndNumbers(t *testing.T) {
	cases := map[string][]string{
		"disc":                 {"•", "•", "•"},
		"circle":               {"◦", "◦", "◦"},
		"square":               {"▪", "▪", "▪"},
		"decimal":              {"1.", "2.", "3."},
		"decimal-leading-zero": {"01.", "02.", "03."},
		"lower-alpha":          {"a.", "b.", "c."},
		"upper-alpha":          {"A.", "B.", "C."},
		"lower-roman":          {"i.", "ii.", "iii."},
		"upper-roman":          {"I.", "II.", "III."},
		"none":                 nil,
	}
	for kind, want := range cases {
		got := markersOf(t, "<ul><li>a</li><li>b</li><li>c</li></ul>",
			"ul { list-style-type: "+kind+" }")
		if len(got) != len(want) {
			t.Errorf("%s gave %d markers (%v), want %d", kind, len(got), got, len(want))
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%s item %d is %q, want %q", kind, i+1, got[i], want[i])
			}
		}
	}
}

// TestDefaultListStyles pins that the user-agent stylesheet gives <ul> a bullet
// and <ol> a number, which is what makes a list look like a list with no author
// CSS at all.
func TestDefaultListStyles(t *testing.T) {
	if got := markersOf(t, "<ul><li>a</li><li>b</li></ul>"); len(got) != 2 || got[0] != "•" {
		t.Errorf("an unstyled <ul> gave %v, want bullets", got)
	}
	if got := markersOf(t, "<ol><li>a</li><li>b</li></ol>"); len(got) != 2 || got[0] != "1." {
		t.Errorf("an unstyled <ol> gave %v, want numbers", got)
	}
}

// TestNumberingCountsOnlyListItems pins that the counter advances for list items
// and nothing else, so a heading between two items does not skip a number.
func TestNumberingCountsOnlyListItems(t *testing.T) {
	got := markersOf(t,
		`<ol><li>a</li><p>not an item</p><li>b</li></ol>`, "ol { list-style-type: decimal }")
	want := []string{"1.", "2."}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("item %d is %q, want %q", i+1, got[i], want[i])
		}
	}
}

// TestNumberingRestartsPerList pins that each list counts from one. A single
// counter shared across the document would number the second list from where the
// first left off.
func TestNumberingRestartsPerList(t *testing.T) {
	got := markersOf(t,
		`<ol><li>a</li><li>b</li></ol><ol><li>c</li><li>d</li></ol>`,
		"ol { list-style-type: decimal }")
	want := []string{"1.", "2.", "1.", "2."}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestAlphabeticIsBijective pins the numbering that ordinary base-26 arithmetic
// gets wrong, and gets wrong at exactly the twenty-sixth item — far enough into
// a list that nobody notices until a document has one.
//
// There is no zero digit, so after "z" comes "aa" rather than "ba".
func TestAlphabeticIsBijective(t *testing.T) {
	cases := map[int]string{
		1: "a", 2: "b", 25: "y", 26: "z",
		27: "aa", 28: "ab", 52: "az", 53: "ba",
		702: "zz", 703: "aaa",
	}
	for index, want := range cases {
		if got := alphabetic(index, 'a'); got != want {
			t.Errorf("item %d is %q, want %q", index, got, want)
		}
	}
	// Upper case is the same sequence.
	if got := alphabetic(27, 'A'); got != "AA" {
		t.Errorf("item 27 upper is %q, want AA", got)
	}
}

// TestRomanNumerals pins the other numbering, including the subtractive forms
// that a naive greedy renderer writes as IIII.
func TestRomanNumerals(t *testing.T) {
	cases := map[int]string{
		1: "I", 2: "II", 3: "III", 4: "IV", 5: "V", 9: "IX",
		10: "X", 14: "XIV", 40: "XL", 49: "XLIX", 50: "L",
		90: "XC", 100: "C", 400: "CD", 500: "D", 900: "CM",
		1000: "M", 1987: "MCMLXXXVII", 3999: "MMMCMXCIX",
	}
	for index, want := range cases {
		if got := roman(index); got != want {
			t.Errorf("%d is %q, want %q", index, got, want)
		}
	}
	// Past what the numerals express, the decimal stands: MMMM is not a
	// numeral, and the specification says to fall back.
	if got := roman(4000); got != "4000" {
		t.Errorf("4000 is %q, want the decimal", got)
	}
	if got := roman(0); got != "0" {
		t.Errorf("0 is %q, want the decimal", got)
	}
}

// TestMarkerPositionIsOutsideByDefault pins where the marker sits. Outside puts
// it clear of the content box, which is what a list looks like; inside puts it
// at the start of the first line, where it pushes the text along.
func TestMarkerPositionIsOutsideByDefault(t *testing.T) {
	root := layoutOf(t, 600, "<ul><li>a</li></ul>")
	var li *Fragment
	var walk func(*Fragment)
	walk = func(f *Fragment) {
		if f.Marker != nil && li == nil {
			li = f
		}
		for _, c := range f.Children {
			walk(c)
		}
	}
	walk(root)
	if li == nil {
		t.Fatal("no marker")
	}
	// Outside: to the left of the content box, so a negative offset from the
	// item's own border box.
	if li.Marker.At.X >= 0 {
		t.Errorf("the marker is at x=%v; outside puts it left of the content",
			li.Marker.At.X.Px())
	}

	root = layoutOf(t, 600, "<ul><li>a</li></ul>", "li { list-style-position: inside }")
	li = nil
	walk(root)
	if li == nil {
		t.Fatal("no marker")
	}
	if li.Marker.At.X < 0 {
		t.Errorf("an inside marker is at x=%v; it belongs at the content's start",
			li.Marker.At.X.Px())
	}
}

// TestMarkersPaint pins that the marker reaches the display list, which is the
// whole point — a marker computed and never drawn is a list with no bullets.
func TestMarkersPaint(t *testing.T) {
	ops := paintOf(t, "<ol><li>one</li><li>two</li></ol>",
		"ol { list-style-type: decimal } li { font-family: Helvetica }")

	var markers []string
	for _, op := range ops {
		if d, ok := op.(DrawText); ok && (d.Text == "1." || d.Text == "2.") {
			markers = append(markers, d.Text)
		}
	}
	if len(markers) != 2 {
		t.Errorf("%d markers painted (%v), want 2", len(markers), markers)
	}
}

// TestUnbreakableOverflowIsAnError pins the guardrail §6.2 calls the classic
// silent clip. The text is there, the box is there, and the part past the edge
// is simply not drawn — nothing else about the page says so.
func TestUnbreakableOverflowIsAnError(t *testing.T) {
	fired[RuleUnbreakableOverflow] = true

	built := Build(Input{HTML: `<p>supercalifragilisticexpialidocious</p>`,
		CSS: []Stylesheet{{Source: noDefaults + "p { font-size: 100px; font-family: Helvetica }"}}})
	rec := NewRecorder(nil)
	w, _ := style.FromPx(100)
	h, _ := style.FromPx(1000)
	Layout(built.Root, Size{W: w, H: h}, nil, rec)

	var found *Finding
	for i := range rec.Findings() {
		if rec.Findings()[i].Rule == RuleUnbreakableOverflow {
			f := rec.Findings()[i]
			found = &f
		}
	}
	if found == nil {
		t.Fatalf("a word far wider than its box did not report: %v", rec.Findings())
	}
	if found.Severity != Error {
		t.Errorf("the overflow was reported as %v, want an error", found.Severity)
	}
	// The message gives both widths, so an author can see how far out it is.
	if !strings.Contains(found.Message, "px") {
		t.Errorf("the message %q does not give the widths", found.Message)
	}

	// Text that fits says nothing.
	built = Build(Input{HTML: `<p>ok</p>`,
		CSS: []Stylesheet{{Source: noDefaults + "p { font-size: 10px; font-family: Helvetica }"}}})
	rec = NewRecorder(nil)
	Layout(built.Root, Size{W: w, H: h}, nil, rec)
	for _, f := range rec.Findings() {
		if f.Rule == RuleUnbreakableOverflow {
			t.Errorf("text that fits reported an overflow: %v", f)
		}
	}
}

// TestOverflowPageIsASelfCheck pins the guardrail that should never fire.
//
// §5's scale is computed so that everything fits, so this firing means that
// calculation was wrong — which is a class of fault no amount of checking the
// document can reach, and the reason a threshold verifying an earlier
// calculation is worth having.
func TestOverflowPageIsASelfCheck(t *testing.T) {
	fired[RuleOverflowPage] = true

	// An ordinary document does not trip it, which is the claim.
	got := renderOf(t, `<h1>Title</h1><p>Some text.</p><ul><li>a<li>b</ul>`,
		Options{Page: A4})
	for _, f := range got.Findings {
		if f.Rule == RuleOverflowPage {
			t.Errorf("an ordinary document reported page overflow: %v", f)
		}
	}

	// Nor does one that had to be scaled, which is the case the scale exists
	// for and the one where a wrong calculation would show.
	avail := A4.Content()
	got = renderOf(t, `<div id="a"></div>`, Options{Page: A4, MinScale: 0.05},
		noDefaults+"#a { height: "+ftoa(avail.H.Px()*4)+"px; background-color: red }")
	for _, f := range got.Findings {
		if f.Rule == RuleOverflowPage {
			t.Errorf("a scaled document reported page overflow, so the scale is "+
				"not doing its job: %v", f)
		}
	}
	if got.Scale >= 1 {
		t.Fatalf("the test document was not scaled: %v", got.Scale)
	}

	// And the check itself sees an overflow when there is one, which is what
	// makes the two assertions above worth anything.
	u := func(v float64) style.Unit { r, _ := style.FromPx(v); return r }
	rec := NewRecorder(nil)
	checkPageOverflow(rec, []Op{
		FillRect{Rect: Rect{u(0), u(0), u(10000), u(10)}, Color: style.RGBA{R: 255, A: 1}},
	}, Size{W: u(100), H: u(100)}, 1)
	if len(rec.Findings()) == 0 {
		t.Error("a box ten thousand pixels wide on a hundred-pixel page was not reported")
	}
}
