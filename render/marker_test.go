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

// TestLowerGreekSkipsFinalSigma pins the one thing about §12.6.2's Greek
// alphabet that is not "the letters in order".
//
// U+03C2, final sigma, is the same letter as U+03C3 written at the end of a
// word. It is not a numeral, so the sequence steps over it — and an
// implementation that walked the code points from alpha to omega would number
// the eighteenth item ς and every item after it one letter out, which is far
// enough into a list that only a document with one would show it.
func TestLowerGreekSkipsFinalSigma(t *testing.T) {
	cases := map[int]string{
		1: "α", 17: "ρ",
		// The eighteenth is sigma proper, not final sigma.
		18: "σ", 19: "τ", 24: "ω",
		// Bijective, like the Latin alphabets: after the last letter comes the
		// first doubled.
		25: "αα", 26: "αβ", 48: "αω", 49: "βα",
	}
	for index, want := range cases {
		if got := alphabeticIn(index, lowerGreek); got != want {
			t.Errorf("item %d is %q, want %q", index, got, want)
		}
	}
	if strings.ContainsRune(string(lowerGreek), 'ς') {
		t.Error("final sigma is in the alphabet; it is not a numeral")
	}
	if n := len(lowerGreek); n != 24 {
		t.Errorf("the alphabet has %d letters, want 24", n)
	}
}

// TestArmenianAndGeorgianAreAdditive pins the two systems §12.6.2 names.
//
// Additive, not positional: the number is the sum of the largest numerals that
// fit, so each figure of a decimal number is a separate mark and 1979 is four of
// them for a coincidental reason rather than because it has four digits. The
// cases below are chosen so that a carry crosses each order of magnitude.
func TestArmenianAndGeorgianAreAdditive(t *testing.T) {
	armenian := map[int]string{
		1: "Ա", 9: "Թ", 10: "Ժ", 11: "ԺԱ", 99: "ՂԹ", 100: "Ճ",
		// 1000 + 900 + 70 + 9.
		1979: "ՌՋՀԹ",
		// The largest the system expresses: 9000 + 900 + 90 + 9.
		9999: "ՔՋՂԹ",
	}
	for index, want := range armenian {
		if got := additive(index, armenianNumerals, 1, 9999); got != want {
			t.Errorf("armenian %d is %q, want %q", index, got, want)
		}
	}
	georgian := map[int]string{
		1: "ა", 9: "თ", 10: "ი", 11: "ია", 100: "რ", 1000: "ჩ",
		10000: "ჵ",
		// The largest: 10000 + 9000 + 900 + 90 + 9.
		19999: "ჵჰშჟთ",
	}
	for index, want := range georgian {
		if got := additive(index, georgianNumerals, 1, 19999); got != want {
			t.Errorf("georgian %d is %q, want %q", index, got, want)
		}
	}

	// Past the range the decimal stands, which is §12.6.2's instruction for a
	// marker a style cannot represent. Silence there would be a list that loses
	// its numbering on the ten-thousandth item and says nothing.
	if got := additive(10000, armenianNumerals, 1, 9999); got != "10000" {
		t.Errorf("armenian 10000 is %q, want the decimal", got)
	}
	if got := additive(20000, georgianNumerals, 1, 19999); got != "20000" {
		t.Errorf("georgian 20000 is %q, want the decimal", got)
	}
	if got := additive(0, armenianNumerals, 1, 9999); got != "0" {
		t.Errorf("armenian 0 is %q, want the decimal", got)
	}
}

// TestEveryListStyleTypeIsDistinct pins that §12.6.2's list is complete.
//
// The fallback for an unrecognised type is a bullet, which is the right answer
// for a type CSS does not define and the wrong one for a type it does: a
// document asking for Armenian numerals got a disc, on every item, with nothing
// reported. Two of the three that were missing needed a whole numbering system,
// so the gap could not be seen by reading markerText — only by listing what the
// specification names and checking each answers differently.
func TestEveryListStyleTypeIsDistinct(t *testing.T) {
	// §12.6.2's complete list, less "none" and the three that are bullets.
	// lower-latin and lower-alpha are one style under two names, and so are the
	// upper pair, so they are grouped rather than expected to differ.
	numbering := [][]string{
		{"decimal"}, {"decimal-leading-zero"}, {"lower-roman"}, {"upper-roman"},
		{"lower-greek"}, {"lower-latin", "lower-alpha"}, {"upper-latin", "upper-alpha"},
		{"armenian"}, {"georgian"},
	}
	seen := map[string]string{}
	for _, names := range numbering {
		got := markerText(names[0], 4)
		if got == "•" {
			t.Errorf("%q produced a bullet, so it is not implemented and nothing said so",
				names[0])
			continue
		}
		for _, alias := range names[1:] {
			if other := markerText(alias, 4); other != got {
				t.Errorf("%q and %q are the same style but produced %q and %q",
					names[0], alias, got, other)
			}
		}
		if was, dup := seen[got]; dup {
			t.Errorf("%q and %q both number the fourth item %q", was, names[0], got)
		}
		seen[got] = names[0]
	}
	// The three that are marks rather than numbers, and "none", which is the one
	// value that produces nothing at all.
	for _, style := range []string{"disc", "circle", "square"} {
		if got := markerText(style, 4); got == "" {
			t.Errorf("%q produced nothing", style)
		}
	}
	if got := markerText("none", 4); got != "" {
		t.Errorf("none produced %q", got)
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

	// Inside is a different mechanism and not a different x: §12.5.1 makes the
	// marker the first inline box in the item, so it is a run on the first line
	// and there is no Marker on the fragment at all. A test that only looked for
	// a Marker would read the change as the feature disappearing.
	root = layoutOf(t, 600, "<ul><li>a</li></ul>", "li { list-style-position: inside }")
	li = nil
	walk(root)
	if li != nil {
		t.Errorf("an inside marker was drawn beside the box at x=%v; it belongs on the line",
			li.Marker.At.X.Px())
	}
	first := firstItemLine(t, root)
	if len(first.Runs) < 2 {
		t.Fatalf("the first line has %d runs, want the marker and the text", len(first.Runs))
	}
	if first.Runs[0].Text != "•" {
		t.Errorf("the first run is %q, want the bullet", first.Runs[0].Text)
	}
	if first.Runs[0].X != 0 {
		t.Errorf("the marker run is at x=%v, want the start of the line", first.Runs[0].X.Px())
	}
	if got := first.Runs[1].Text; got != "a" {
		t.Errorf("the run after the marker is %q, want the item's text", got)
	}
	if first.Runs[1].X <= first.Runs[0].X {
		t.Error("the text was not pushed along by the marker")
	}
}

// firstItemLine is the first line box of the first list item in a tree.
func firstItemLine(t *testing.T, root *Fragment) LineFragment {
	t.Helper()
	var found *LineFragment
	var walk func(*Fragment)
	walk = func(f *Fragment) {
		if found == nil && f.Box != nil && f.Box.ListItem && len(f.Lines) > 0 {
			found = &f.Lines[0]
			return
		}
		for _, c := range f.Children {
			walk(c)
		}
	}
	walk(root)
	if found == nil {
		t.Fatal("no list item with a line box")
	}
	return *found
}

// TestInsideMarkerTakesRoomOnTheLine pins §12.5.1's arithmetic exactly, which
// needs a face whose advances are known: Ahem's every glyph is an em square.
//
// "1." at 20px is two squares, so 40px, and the gap this engine leaves between a
// marker and its text is half an em, 10px. The item's own text therefore begins
// at 50px and not at 40 and not at 0.
func TestInsideMarkerTakesRoomOnTheLine(t *testing.T) {
	set := loadAhem(t)
	built := Build(Input{HTML: "<ol><li>ab</li></ol>", CSS: []Stylesheet{{Source: `
		ol { list-style-type: decimal; list-style-position: inside; margin: 0; padding: 0 }
		li { font-family: Ahem; font-size: 20px }`}}})
	w, _ := style.FromPx(600)
	h, _ := style.FromPx(600)
	root := Layout(built.Root, Size{W: w, H: h}, set, NewRecorder(nil))

	line := firstItemLine(t, root)
	if len(line.Runs) != 2 {
		t.Fatalf("the line has %d runs, want the marker and the text", len(line.Runs))
	}
	if got := line.Runs[0].Text; got != "1." {
		t.Fatalf("the marker run is %q, want %q", got, "1.")
	}
	if got := line.Runs[1].X.Px(); got != 50 {
		t.Errorf("the item's text begins at %vpx; §12.5.1 puts it after the marker's "+
			"40px and the half-em gap, so 50", got)
	}
}

// TestEmptyInsideListItemHasALine pins the case a marker drawn beside the box
// cannot produce: an item with no content of its own is still one line tall,
// because the marker is content.
//
// It is the shape a dozen of the suite's list tests are built on — an empty
// "display: list-item" with a background, asserted to be a coloured strip with a
// dot in it — and with the marker drawn beside the box the strip had no height
// and nothing was painted at all.
func TestEmptyInsideListItemHasALine(t *testing.T) {
	root := layoutOf(t, 600, `<div id="i"></div>`,
		"#i { display: list-item; list-style-position: inside }")
	var item *Fragment
	var walk func(*Fragment)
	walk = func(f *Fragment) {
		if f.Box != nil && f.Box.ListItem {
			item = f
		}
		for _, c := range f.Children {
			walk(c)
		}
	}
	walk(root)
	if item == nil {
		t.Fatal("no list item")
	}
	if len(item.Lines) != 1 {
		t.Fatalf("an empty item with an inside marker has %d line boxes, want 1", len(item.Lines))
	}
	if item.BorderRect.H <= 0 {
		t.Error("the item has no height, so its background would paint nothing")
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
