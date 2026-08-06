package render

import (
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/style"
)

// Inline layout.
//
// The measurements here come from the standard fourteen faces, whose metrics are
// fixed by the PDF specification and are therefore the same everywhere — which is
// what makes an exact assertion about a text width possible at all. Helvetica's
// space is 278 units of 1000, so at 100px it is 27.8px, and every width below is
// derived that way rather than recorded from a run.

// linesOf returns the lines of the fragment for an element.
func linesOf(t *testing.T, root *Fragment, id string) []LineFragment {
	t.Helper()
	return find(t, root, id).Lines
}

// lineTexts renders each line's text, which is what breaking is really about.
func lineTexts(lines []LineFragment) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		var b strings.Builder
		for _, r := range line.Runs {
			b.WriteString(r.Text)
		}
		out = append(out, b.String())
	}
	return out
}

// TestTextIsMeasuredAgainstTheFace pins that a width comes from the font rather
// than from a guess. The number is the specification's own metric for the face.
func TestTextIsMeasuredAgainstTheFace(t *testing.T) {
	face, err := fonts.Standard("Helvetica")
	if err != nil {
		t.Fatalf("loading Helvetica: %v", err)
	}
	// Helvetica's advances are in units of 1000 per em, so a size of 100 makes
	// the arithmetic legible: "Hello world" is 494.5 units.
	want := face.Measure("Hello world", 100)
	if want <= 0 {
		t.Fatal("the face measured nothing")
	}

	root := layoutOf(t, 10000, `<p id="p">Hello world</p>`,
		noDefaults+`p { font-size: 100px; font-family: Helvetica }`)
	lines := linesOf(t, root, "p")
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1: %v", len(lines), lineTexts(lines))
	}

	var total style.Unit
	for _, r := range lines[0].Runs {
		total = total.Add(r.Width)
	}
	expect, _ := style.FromPx(want)
	if total != expect {
		t.Errorf("the line measures %.3f px, want the face's %.3f", total.Px(), want)
	}
}

// TestLinesBreakAtSpaces pins greedy line breaking: a line takes what fits and
// the next one starts after it.
func TestLinesBreakAtSpaces(t *testing.T) {
	// At 100px Helvetica, "aaaa" is about 222px and a space about 28px, so a
	// 500px line holds two of them and not three.
	const words = "aaaa aaaa aaaa aaaa"
	root := layoutOf(t, 500, `<p id="p">`+words+`</p>`,
		noDefaults+`p { font-size: 100px; font-family: Helvetica }`)

	lines := lineTexts(linesOf(t, root, "p"))
	if len(lines) < 2 {
		t.Fatalf("the text did not wrap: %v", lines)
	}
	// Every line's words are whole: breaking inside a word would read as a
	// different word.
	for _, line := range lines {
		for _, word := range strings.Fields(line) {
			if word != "aaaa" {
				t.Errorf("a word was split: %q in %v", word, lines)
			}
		}
	}
	// And nothing was lost.
	if got := strings.Join(lines, " "); got != words {
		t.Errorf("the text came back as %q, want %q", got, words)
	}
}

// TestTrailingSpacesAreTrimmed pins CSS Text's rule for the space a break
// happened at. Keeping it would make a centred line sit off-centre by half a
// space and a right-aligned one hang.
func TestTrailingSpacesAreTrimmed(t *testing.T) {
	root := layoutOf(t, 500, `<p id="p">aaaa aaaa aaaa aaaa</p>`,
		noDefaults+`p { font-size: 100px; font-family: Helvetica }`)

	for i, line := range linesOf(t, root, "p") {
		if len(line.Runs) == 0 {
			continue
		}
		if last := line.Runs[len(line.Runs)-1].Text; strings.TrimSpace(last) == "" {
			t.Errorf("line %d ends with the space it broke at: %q", i, last)
		}
	}

	// A line never starts with one either, or every line after the first would
	// be indented.
	for i, line := range linesOf(t, root, "p") {
		if len(line.Runs) == 0 {
			continue
		}
		if first := line.Runs[0].Text; strings.TrimSpace(first) == "" {
			t.Errorf("line %d begins with a space: %q", i, first)
		}
	}
}

// TestLineHeight pins the three forms the property takes, and that a bare number
// is a multiplier rather than a length — the one place in CSS where that is
// true, and the reason line-height is usually written that way.
func TestLineHeight(t *testing.T) {
	cases := map[string]float64{
		"normal": 24, // 1.2 x 20px
		"1.5":    30, // a multiplier
		"2":      40, //
		"30px":   30, // a length
		"150%":   30, // a percentage of the font size
	}
	for value, want := range cases {
		root := layoutOf(t, 10000, `<p id="p">one line</p>`,
			noDefaults+`p { font-size: 20px; font-family: Helvetica; line-height: `+value+` }`)
		px(t, "line-height:"+value, find(t, root, "p").BorderRect.H, want)
	}
}

// TestManyLinesStack pins that a block's height is the sum of its lines, which
// is what makes text push what follows it down the page.
func TestManyLinesStack(t *testing.T) {
	const words = "aaaa aaaa aaaa aaaa aaaa aaaa"
	root := layoutOf(t, 500, `<p id="p">`+words+`</p>`,
		noDefaults+`p { font-size: 100px; font-family: Helvetica; line-height: 120px }`)

	lines := linesOf(t, root, "p")
	if len(lines) < 2 {
		t.Fatalf("the text did not wrap: %v", lineTexts(lines))
	}
	px(t, "the paragraph's height", find(t, root, "p").BorderRect.H,
		float64(len(lines))*120)

	// The lines are stacked, not overlaid.
	for i, line := range lines {
		px(t, "line "+itoa(i)+"'s top", line.Rect.Y, float64(i)*120)
	}
}

// TestBaselineSplitsTheLeading pins where text sits within a line box. The
// difference between the line height and the text is split above and below,
// which is what makes a paragraph evenly spaced rather than crowded against the
// tops of its lines.
func TestBaselineSplitsTheLeading(t *testing.T) {
	root := layoutOf(t, 10000, `<p id="p">text</p>`,
		noDefaults+`p { font-size: 100px; font-family: Helvetica; line-height: 200px }`)

	line := linesOf(t, root, "p")[0]
	face, _ := fonts.Standard("Helvetica")
	d := face.Descriptor()
	upem := float64(face.UnitsPerEm())
	ascent := 100 * float64(d.Ascent) / upem
	descent := 100 * -float64(d.Descent) / upem
	wantBaseline := (200-ascent-descent)/2 + ascent

	px(t, "the baseline", line.Baseline, wantBaseline)
	// It is inside the line box, which a wrong sign would not be.
	if line.Baseline <= 0 || line.Baseline >= line.Rect.H {
		t.Errorf("the baseline at %.2f is outside the line box of %.2f",
			line.Baseline.Px(), line.Rect.H.Px())
	}
}

// TestInlineElementsShareALine pins that an <em> does not break the line it is
// in — the inline tree is flattened for exactly this reason, since a break can
// fall anywhere including inside one.
func TestInlineElementsShareALine(t *testing.T) {
	root := layoutOf(t, 10000, `<p id="p">one <em>two</em> three</p>`,
		noDefaults+`p { font-size: 20px; font-family: Helvetica }`)

	lines := linesOf(t, root, "p")
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1: %v", len(lines), lineTexts(lines))
	}
	if got := lineTexts(lines)[0]; got != "one two three" {
		t.Errorf("the line reads %q", got)
	}
	// The emphasised run kept its own box, which is how painting will know its
	// style.
	var sawEm bool
	for _, r := range lines[0].Runs {
		if r.Box != nil && r.Box.Parent != nil && r.Box.Parent.Element != nil &&
			r.Box.Parent.Element.Name == "em" {
			sawEm = true
		}
	}
	if !sawEm {
		t.Error("the emphasised text lost the box it came from")
	}
}

// TestBoldAndItalicPickDifferentFaces pins that the weight and style reach the
// font set. A bold run set in the regular face is narrower than it should be,
// and every line break after it is wrong.
func TestBoldAndItalicPickDifferentFaces(t *testing.T) {
	root := layoutOf(t, 10000, `<p id="p">text</p><p id="q"><strong>text</strong></p>`,
		noDefaults+`p { font-size: 100px; font-family: Helvetica }`)

	plain := linesOf(t, root, "p")[0].Runs[0]
	bold := linesOf(t, root, "q")[0].Runs[0]

	if plain.Face == bold.Face {
		t.Fatal("bold text was set in the same face as plain")
	}
	if bold.Width <= plain.Width {
		t.Errorf("the bold run measures %.2f and the plain one %.2f; bold is wider",
			bold.Width.Px(), plain.Width.Px())
	}
}

// TestCJKBreaksBetweenIdeographs pins the one break rule that is not about
// spaces. Chinese writes none, so a breaker that only knew about spaces would
// run a whole paragraph together as one unbreakable word.
func TestCJKBreaksBetweenIdeographs(t *testing.T) {
	pieces, _ := splitAtBreaks("日本語のテキスト")
	if len(pieces) < 4 {
		t.Fatalf("ideographic text was cut into %d pieces, want one per character", len(pieces))
	}
	for _, p := range pieces {
		if len([]rune(p.text)) != 1 {
			t.Errorf("a piece holds more than one ideograph: %q", p.text)
		}
	}

	// And Latin is not cut up that way: a word stays whole.
	pieces, _ = splitAtBreaks("hello world")
	var words []string
	for _, p := range pieces {
		if !p.space {
			words = append(words, p.text)
		}
	}
	if len(words) != 2 || words[0] != "hello" || words[1] != "world" {
		t.Errorf("Latin text was cut into %v", words)
	}
}

// TestBreakOpportunities pins the subset of UAX #14 that is implemented, by what
// each rule does rather than by its class letters.
func TestBreakOpportunities(t *testing.T) {
	texts := func(text string) []string {
		var out []string
		pieces, _ := splitAtBreaks(text)
		for _, p := range pieces {
			if !p.space {
				out = append(out, p.text)
			}
		}
		return out
	}

	// A hyphen ends a piece, so a compound may break where it is written.
	if got := texts("well-known"); len(got) != 2 || got[0] != "well-" || got[1] != "known" {
		t.Errorf("a hyphenated compound was cut into %v", got)
	}
	// A trailing hyphen does not, since there is nothing after it to move.
	if got := texts("end-"); len(got) != 1 {
		t.Errorf("a trailing hyphen was cut into %v", got)
	}
	// A zero-width space is a break and contributes no text, which is the whole
	// reason an author writes one.
	if got := texts("one​two"); len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Errorf("a zero-width space gave %v", got)
	}
	if strings.Contains(strings.Join(texts("one​two"), ""), "​") {
		t.Error("the zero-width space was kept in the text")
	}
	// A run with no opportunities at all is one piece.
	if got := texts("unbreakable"); len(got) != 1 {
		t.Errorf("an unbroken word was cut into %v", got)
	}
}

// TestNoBreakBetweenAdjacentRuns pins that a line breaks only where there is an
// opportunity, not merely wherever the text happens to be cut into pieces.
//
// "aaaa<em>aaaa</em>" is two runs with nothing between them — no space, no
// hyphen — so it is one unbreakable word however narrow the line. A breaker that
// wrapped at every piece boundary would split it, and the split would look like
// a line break the author asked for.
func TestNoBreakBetweenAdjacentRuns(t *testing.T) {
	root := layoutOf(t, 100, `<p id="p">aaaa<em>aaaa</em></p>`,
		noDefaults+`p { font-size: 100px; font-family: Helvetica }`)

	lines := linesOf(t, root, "p")
	if len(lines) != 1 {
		t.Fatalf("two adjacent runs were split across %d lines: %v",
			len(lines), lineTexts(lines))
	}
	if got := lineTexts(lines)[0]; got != "aaaaaaaa" {
		t.Errorf("the line reads %q, want the two runs together", got)
	}

	// With a space between them it does break, so the assertion above is about
	// the missing opportunity and not about runs never breaking.
	root = layoutOf(t, 100, `<p id="p">aaaa <em>aaaa</em></p>`,
		noDefaults+`p { font-size: 100px; font-family: Helvetica }`)
	if n := len(linesOf(t, root, "p")); n != 2 {
		t.Errorf("a space between the runs gave %d lines, want 2", n)
	}
}

// TestBreakOpportunityPassesThroughAnEmptyBox pins the case where the
// opportunity and the text it applies to are separated by a box that produces no
// text of its own.
//
// A <span> holding nothing but a zero-width space is exactly that: it yields no
// pieces to put on a line, and it must hand the break opportunity on rather than
// swallow it. Swallowing it would set the two words either side as one
// unbreakable run, and nothing about the markup suggests that.
func TestBreakOpportunityPassesThroughAnEmptyBox(t *testing.T) {
	root := layoutOf(t, 100, `<p id="p">aaaa<span>​</span>aaaa</p>`,
		noDefaults+`p { font-size: 100px; font-family: Helvetica }`)

	lines := lineTexts(linesOf(t, root, "p"))
	if len(lines) != 2 {
		t.Fatalf("the opportunity was lost across the empty box: %v", lines)
	}
	if lines[0] != "aaaa" || lines[1] != "aaaa" {
		t.Errorf("the lines read %v, want each word on its own", lines)
	}
}

// TestLeadingSpaceIsDropped pins the other half of CSS Text's line-edge rule. A
// space at the start of a line is the one the break happened at, or the one the
// author left after a tag, and keeping it indents the line by a space nobody
// wrote.
func TestLeadingSpaceIsDropped(t *testing.T) {
	root := layoutOf(t, 10000, `<p id="p"> hello</p>`,
		noDefaults+`p { font-size: 100px; font-family: Helvetica }`)

	runs := linesOf(t, root, "p")[0].Runs
	if len(runs) == 0 {
		t.Fatal("the line has no runs")
	}
	if strings.TrimSpace(runs[0].Text) == "" {
		t.Errorf("the line begins with the space %q", runs[0].Text)
	}
	if runs[0].X != 0 {
		t.Errorf("the first run starts at %.2f px, want the left edge", runs[0].X.Px())
	}
}

// TestUnbreakableTextOverflowsRatherThanSplitting pins the choice made when a
// word is wider than its line. Splitting it would read as a different word, so
// it is placed and overflows — which the geometry guardrails will report once
// they can see it.
func TestUnbreakableTextOverflowsRatherThanSplitting(t *testing.T) {
	root := layoutOf(t, 100, `<p id="p">supercalifragilistic</p>`,
		noDefaults+`p { font-size: 100px; font-family: Helvetica }`)

	lines := linesOf(t, root, "p")
	if len(lines) != 1 {
		t.Fatalf("an unbreakable word was split across %d lines: %v",
			len(lines), lineTexts(lines))
	}
	if got := lineTexts(lines)[0]; got != "supercalifragilistic" {
		t.Errorf("the word came out as %q", got)
	}
	if lines[0].Runs[0].Width <= find(t, root, "p").ContentRect().W {
		t.Error("the test's word is not actually wider than its line")
	}
}

// TestUnsupportedScriptIsAnError pins the guardrail of §6.3 that matters most,
// and why it is an error rather than a warning: unbroken or unordered text still
// looks like text, so the failure mode looks like success.
func TestUnsupportedScriptIsAnError(t *testing.T) {
	fired[RuleUnsupportedScript] = true

	cases := map[string]string{
		"מה שלומך":   "right-to-left",
		"مرحبا":      "right-to-left",
		"สวัสดีครับ": "no spaces",
	}
	for text, why := range cases {
		got := Build(Input{HTML: "<p>" + text + "</p>"})
		rec := NewRecorder(nil)
		w, _ := style.FromPx(1000)
		Layout(got.Root, Size{W: w, H: w}, nil, rec)

		var found *Finding
		for i := range rec.Findings() {
			if rec.Findings()[i].Rule == RuleUnsupportedScript {
				f := rec.Findings()[i]
				found = &f
			}
		}
		if found == nil {
			t.Errorf("%q (%s) was laid out with no complaint", text, why)
			continue
		}
		if found.Severity != Error {
			t.Errorf("%q was reported as %v; §6.3 makes this an error because "+
				"the failure looks like success", text, found.Severity)
		}
	}

	// Latin text says nothing, so the check is about the script and not about
	// text in general.
	got := Build(Input{HTML: "<p>ordinary text</p>"})
	rec := NewRecorder(nil)
	w, _ := style.FromPx(1000)
	Layout(got.Root, Size{W: w, H: w}, nil, rec)
	for _, f := range rec.Findings() {
		if f.Rule == RuleUnsupportedScript {
			t.Errorf("Latin text was reported as an unsupported script: %v", f)
		}
	}
}

// TestMissingGlyphIsAnError pins the other §6.3 error. Tofu is the purest form
// of silent garbage: a reader seeing boxes where letters should be blames their
// viewer, and the author never hears about it.
func TestMissingGlyphIsAnError(t *testing.T) {
	fired[RuleGlyphMissing] = true

	// The standard faces cover Latin and nothing else, so a CJK character has no
	// glyph in them. It is not in the unsupported-script list, so the complaint
	// can only be about the glyph.
	got := Build(Input{HTML: "<p>日本</p>"})
	rec := NewRecorder(nil)
	w, _ := style.FromPx(1000)
	Layout(got.Root, Size{W: w, H: w}, nil, rec)

	var found *Finding
	for i := range rec.Findings() {
		if rec.Findings()[i].Rule == RuleGlyphMissing {
			f := rec.Findings()[i]
			found = &f
		}
	}
	if found == nil {
		t.Fatalf("a character with no glyph was set silently: %v", rec.Findings())
	}
	if found.Severity != Error {
		t.Errorf("a missing glyph was reported as %v, want an error", found.Severity)
	}
	// The message names the character by code point, which is what an author can
	// search for — the character itself may not be showable in whatever reads
	// the report.
	if !strings.Contains(found.Message, "U+") {
		t.Errorf("the message %q does not name the character by code point", found.Message)
	}

	// Latin says nothing.
	got = Build(Input{HTML: "<p>ordinary</p>"})
	rec = NewRecorder(nil)
	Layout(got.Root, Size{W: w, H: w}, nil, rec)
	for _, f := range rec.Findings() {
		if f.Rule == RuleGlyphMissing {
			t.Errorf("Latin text was reported as missing a glyph: %v", f)
		}
	}
}

// TestFontFallbackIsReported pins that a substitution is never silent. A
// document set in a face its author did not choose has different metrics and
// different line breaks, and nothing about the page says so.
func TestFontFallbackIsReported(t *testing.T) {
	fired[RuleFontFallback] = true

	got := Build(Input{HTML: "<p>text</p>",
		CSS: []Stylesheet{{Source: `p { font-family: "Nonesuch Display" }`}}})
	rec := NewRecorder(nil)
	w, _ := style.FromPx(1000)
	Layout(got.Root, Size{W: w, H: w}, nil, rec)

	var found bool
	for _, f := range rec.Findings() {
		if f.Rule == RuleFontFallback {
			found = true
			if !strings.Contains(f.Message, "Nonesuch") {
				t.Errorf("the message %q does not name the family that was asked for", f.Message)
			}
		}
	}
	if !found {
		t.Fatalf("a substituted face was not reported: %v", rec.Findings())
	}

	// A family the set has says nothing.
	got = Build(Input{HTML: "<p>text</p>",
		CSS: []Stylesheet{{Source: `p { font-family: Helvetica }`}}})
	rec = NewRecorder(nil)
	Layout(got.Root, Size{W: w, H: w}, nil, rec)
	for _, f := range rec.Findings() {
		if f.Rule == RuleFontFallback {
			t.Errorf("an available family was reported as a fallback: %v", f)
		}
	}
}

// TestFontFamilyListIsTriedInOrder pins that a stack is a preference list: the
// first family the set has wins, and one it does not have is passed over rather
// than ending the search.
func TestFontFamilyListIsTriedInOrder(t *testing.T) {
	root := layoutOf(t, 10000, `<p id="p">text</p>`,
		noDefaults+`p { font-family: "Nonesuch", Courier, Helvetica; font-size: 100px }`)
	face := linesOf(t, root, "p")[0].Runs[0].Face
	if !strings.Contains(face.Name(), "Courier") {
		t.Errorf("the face is %q; Courier was the first available family", face.Name())
	}
}

// TestGenericFamiliesResolve pins the three families CSS guarantees, which is
// what makes "font-family: sans-serif" work with no caller-supplied fonts.
func TestGenericFamiliesResolve(t *testing.T) {
	cases := map[string]string{
		"serif":      "Times",
		"sans-serif": "Helvetica",
		"monospace":  "Courier",
	}
	for family, want := range cases {
		root := layoutOf(t, 10000, `<p id="p">text</p>`,
			noDefaults+`p { font-family: `+family+`; font-size: 100px }`)
		face := linesOf(t, root, "p")[0].Runs[0].Face
		if !strings.Contains(face.Name(), want) {
			t.Errorf("%s resolved to %q, want a %s face", family, face.Name(), want)
		}
	}
}

// TestWeightBoundary pins where the numeric scale becomes bold. 400 is normal
// and 700 is bold; the boundary is at 600, which is where every renderer puts it.
func TestWeightBoundary(t *testing.T) {
	cases := map[string]bool{
		"normal": false, "bold": true, "bolder": true, "lighter": false,
		"100": false, "400": false, "500": false,
		"600": true, "700": true, "900": true,
		"nonsense": false,
	}
	for value, want := range cases {
		if got := isBold(value); got != want {
			t.Errorf("font-weight:%s is bold=%v, want %v", value, got, want)
		}
	}
}

// TestInlineLayoutIsTotal pins that no text panics it.
func TestInlineLayoutIsTotal(t *testing.T) {
	texts := []string{
		"", " ", "a", "a b", strings.Repeat("a ", 500),
		strings.Repeat("a", 5000), "日本語", "​", "---", "a-b-c",
		"  ", "tab\there", "mixed 日本 text",
		strings.Repeat("​", 500),
	}
	for _, text := range texts {
		got := Build(Input{HTML: "<p>" + text + "</p>"})
		rec := NewRecorder(nil)
		for _, width := range []float64{0, 1, 100, 10000} {
			w, _ := style.FromPx(width)
			Layout(got.Root, Size{W: w, H: w}, nil, rec)
		}
	}
}
