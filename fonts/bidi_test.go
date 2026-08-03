package fonts

import (
	"testing"

	"github.com/mgilbir/pdf0/internal/fonttest"
)

// The bidirectional algorithm as the shaping pipeline uses it.
//
// bidi_conformance_test.go proves the algorithm against Unicode's own six
// hundred thousand cases; this file proves the wiring — that a shaped run comes
// back in the order it is drawn, that shaping still saw the text in the order it
// is written, and that the positioning a font states survives the reversal.
//
// Hebrew is used wherever the point is direction alone, because Hebrew does not
// join and so nothing else is going on; Arabic wherever the point is that
// joining and direction are separate stages.

const (
	alefHeb = 0x05D0 // HEBREW LETTER ALEF
	betHeb  = 0x05D1
	gimel   = 0x05D2
)

// TestBidiClassTableNamesEveryClass guards the generated table against a Unicode
// release that renames or withdraws a property value.
//
// cmd/genbidi fails if the data does not use a value the algorithm names, but
// the generator is only run by hand. This is the same check on the committed
// file, so that a table regenerated from the wrong version of the UCD — or by
// hand, which the header forbids and nothing enforces — cannot leave a branch of
// the algorithm silently unreachable.
func TestBidiClassTableNamesEveryClass(t *testing.T) {
	named := map[bidiClass]string{
		bidiR: "R", bidiAL: "AL", bidiEN: "EN", bidiES: "ES", bidiET: "ET",
		bidiAN: "AN", bidiCS: "CS", bidiNSM: "NSM", bidiBN: "BN", bidiB: "B",
		bidiS: "S", bidiWS: "WS", bidiON: "ON", bidiLRE: "LRE", bidiRLE: "RLE",
		bidiLRO: "LRO", bidiRLO: "RLO", bidiPDF: "PDF", bidiLRI: "LRI",
		bidiRLI: "RLI", bidiFSI: "FSI", bidiPDI: "PDI",
	}
	// bidiL is not in the list: it is the default, and the table says it by
	// leaving a character out.
	for _, r := range bidiClassRanges {
		delete(named, r.class)
	}
	for _, name := range named {
		t.Errorf("no character in the table has Bidi_Class %s", name)
	}
	if len(bidiBrackets) == 0 {
		t.Error("the bracket table is empty; rule N0 has nothing to work with")
	}
	if len(bidiMirrors) == 0 {
		t.Error("the mirroring table is empty; rule L4 has nothing to work with")
	}
}

// TestBidiClassesAreUnicodes pins the generated table against characters the
// rest of this rests on, including one that is not a character at all.
func TestBidiClassesAreUnicodes(t *testing.T) {
	cases := map[rune]bidiClass{
		'a':     bidiL,
		alefHeb: bidiR,
		beh:     bidiAL,
		'5':     bidiEN,
		0x0663:  bidiAN, // ARABIC-INDIC DIGIT THREE
		' ':     bidiWS,
		'(':     bidiON,
		0x0301:  bidiNSM, // COMBINING ACUTE ACCENT
		0x200D:  bidiBN,  // ZERO WIDTH JOINER
		0x202B:  bidiRLE,
		0x2067:  bidiRLI,
		0x2069:  bidiPDI,
		// U+0590 is unassigned and always has been. It is right-to-left all the
		// same, because it sits in the Hebrew block — the block defaults are in
		// the table, and a character Unicode has not defined yet still has to be
		// laid out the way its neighbours will be.
		0x0590: bidiR,
	}
	for r, want := range cases {
		if got := bidiClassOf(r); got != want {
			t.Errorf("bidiClassOf(U+%04X) = %d, want %d", r, got, want)
		}
	}
}

// TestBidiBracketAndMirrorTables pins the two tables rules N0 and L4 read.
func TestBidiBracketAndMirrorTables(t *testing.T) {
	paired, open, ok := bidiBracketOf('[')
	if !ok || !open || paired != ']' {
		t.Errorf("bidiBracketOf('[') = (%q, open=%v, ok=%v), want (']', true, true)", paired, open, ok)
	}
	paired, open, ok = bidiBracketOf('}')
	if !ok || open || paired != '{' {
		t.Errorf("bidiBracketOf('}') = (%q, open=%v, ok=%v), want ('{', false, true)", paired, open, ok)
	}
	if _, _, ok := bidiBracketOf('a'); ok {
		t.Error("a letter was reported as a bracket")
	}
	if m, ok := bidiMirrorOf('('); !ok || m != ')' {
		t.Errorf("bidiMirrorOf('(') = (%q, %v), want (')', true)", m, ok)
	}
	if _, ok := bidiMirrorOf('a'); ok {
		t.Error("a letter was reported as mirrored")
	}
}

// TestBidiRunsSplitAndReorder is the algorithm's output in the form the shaping
// pipeline consumes it: stretches of one direction, in the order they are drawn.
func TestBidiRunsSplitAndReorder(t *testing.T) {
	// A right-to-left sentence with a left-to-right phrase inside it. The
	// phrase is one level deeper, and the whole line reverses around it.
	text := string([]rune{alefHeb, betHeb, gimel}) + " abc " + string([]rune{gimel, betHeb, alefHeb})
	runs := bidiLogicalRuns(text)
	if len(runs) != 3 {
		t.Fatalf("got %d runs, want 3: %v", len(runs), runs)
	}
	wantLevels := []int{1, 2, 1}
	for i, r := range runs {
		if r.level != wantLevels[i] {
			t.Errorf("run %d is at level %d, want %d", i, r.level, wantLevels[i])
		}
	}
	levels := []int{runs[0].level, runs[1].level, runs[2].level}
	order := bidiVisualOrder(levels)
	// The line is right-to-left, so the last-written run is drawn first; the
	// Latin phrase inside it keeps its own direction.
	want := []int{2, 1, 0}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("visual order %v, want %v", order, want)
		}
	}
}

// TestBidiRunsOfPlainTextAreOne pins the cost of all this for text that needs
// none of it: one run, level zero, no reordering.
func TestBidiRunsOfPlainTextAreOne(t *testing.T) {
	runs := bidiLogicalRuns("Hello, world!")
	if len(runs) != 1 || runs[0].level != 0 || runs[0].rtl() {
		t.Fatalf("plain Latin gave %v, want one left-to-right run", runs)
	}
	if runs := bidiLogicalRuns(""); runs != nil {
		t.Errorf("the empty string gave %v, want nothing", runs)
	}
}

// TestBidiShortcutAgreesWithTheAlgorithm is the only thing that makes the
// shortcut in bidiLogicalRuns safe to have.
//
// Text with nothing right-to-left in it is answered without running the
// algorithm at all, because the pipeline asks the question of every string it
// sets and most of them are Latin. A shortcut that disagreed with the algorithm
// would be a silent wrong answer on exactly the text nobody thinks to check —
// so every case the shortcut claims is checked against the algorithm itself.
func TestBidiShortcutAgreesWithTheAlgorithm(t *testing.T) {
	cases := []string{
		"", "a", "Hello, world!", "café — naïve",
		"1,234.56", "+42%", "$1.5m", "  leading and trailing  ",
		"line\nbreak", "tab\tseparated", "áb", // a combining mark
		"​zero width space", "emoji \U0001F600 too", // neither is directional
		"日本語のテキスト", "Ελληνικά", "Кириллица",
		"a‍b", // a zero-width joiner: boundary neutral, still not directional
	}
	for _, s := range cases {
		short := bidiLogicalRuns(s)
		if s != "" && bidiNeedsAlgorithm(s) {
			t.Errorf("%q was not taken by the shortcut; the case proves nothing", s)
			continue
		}
		var full []bidiRun
		if s != "" {
			full = bidiResolveRuns(s)
		}
		if len(short) != len(full) {
			t.Errorf("%q: the shortcut gives %v and the algorithm %v", s, short, full)
			continue
		}
		for i := range full {
			if short[i] != full[i] {
				t.Errorf("%q: the shortcut gives %v and the algorithm %v", s, short, full)
				break
			}
		}
	}

	// And the other half: text that does need the algorithm must not be taken by
	// the shortcut.
	for _, s := range []string{
		string(rune(alefHeb)), "a" + string(rune(beh)), "‏", // right-to-left mark
		"a‫b‬", // an explicit embedding
		"a⁧b⁩", // an isolate
		"١٢",   // Arabic-Indic digits, which are Arabic numbers
	} {
		if !bidiNeedsAlgorithm(s) {
			t.Errorf("%q was taken by the shortcut, and it must not be", s)
		}
	}
}

// hebrewFace covers three Hebrew letters, the Latin alphabet and the brackets,
// with no layout tables at all: direction is decided before a font is consulted,
// and this fixture has nothing else to offer.
func hebrewFace(t *testing.T) *Face {
	t.Helper()
	glyphs := []fonttest.Glyph{
		{Rune: alefHeb, Advance: 500, HasShape: true},
		{Rune: betHeb, Advance: 500, HasShape: true},
		{Rune: gimel, Advance: 500, HasShape: true},
		{Rune: '(', Advance: 300, HasShape: true},
		{Rune: ')', Advance: 300, HasShape: true},
		{Rune: ' ', Advance: 250, HasShape: true},
	}
	for r := 'a'; r <= 'z'; r++ {
		glyphs = append(glyphs, fonttest.Glyph{Rune: r, Advance: 400, HasShape: true})
	}
	for r := '0'; r <= '9'; r++ {
		glyphs = append(glyphs, fonttest.Glyph{Rune: r, Advance: 350, HasShape: true})
	}
	f, err := Load(fonttest.SFNT(fonttest.SFNTOptions{Name: "Hebrew", Glyphs: glyphs}))
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	return f
}

// clustersOf is the byte offset each drawn glyph came from, which is the whole
// visible consequence of reordering.
func clustersOf(glyphs []Glyph) []int {
	out := make([]int, len(glyphs))
	for i, g := range glyphs {
		out[i] = g.Cluster
	}
	return out
}

func sameInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestHebrewIsEmittedInVisualOrder is the defect this all exists to fix. A
// text-showing operator moves the pen left to right, so a right-to-left word has
// to arrive with its last letter first — otherwise the word is drawn backwards,
// which a reader of the script notices immediately and nobody else does.
func TestHebrewIsEmittedInVisualOrder(t *testing.T) {
	f := hebrewFace(t)
	word := string([]rune{alefHeb, betHeb, gimel})
	glyphs, missing := f.ShapeGlyphs(word)
	if missing != 0 {
		t.Fatalf("%d characters have no glyph", missing)
	}
	// Each letter is two bytes, so alef is at 0 and gimel at 4; gimel is drawn
	// first.
	if want := []int{4, 2, 0}; !sameInts(clustersOf(glyphs), want) {
		t.Errorf("drawn from bytes %v, want %v — the word is drawn backwards", clustersOf(glyphs), want)
	}
	gimelGID, _ := f.GlyphID(gimel)
	if glyphs[0].GID != gimelGID {
		t.Errorf("the first glyph drawn is %d, want %d (gimel, the last letter)", glyphs[0].GID, gimelGID)
	}
}

// TestLatinIsUntouchedByDirection pins that text that runs one way, the way the
// pen already does, is passed through exactly as it was.
func TestLatinIsUntouchedByDirection(t *testing.T) {
	f := hebrewFace(t)
	glyphs, _ := f.ShapeGlyphs("abc")
	if want := []int{0, 1, 2}; !sameInts(clustersOf(glyphs), want) {
		t.Errorf("drawn from bytes %v, want %v", clustersOf(glyphs), want)
	}
}

// TestNumbersInRightToLeftTextKeepTheirOwnDirection is the case that makes this
// a reordering rather than a reversal. A number written inside Hebrew is still
// read left to right, so reversing the line has to leave its digits alone —
// "‏אב 123 גד‏" with the number backwards is a different number.
func TestNumbersInRightToLeftTextKeepTheirOwnDirection(t *testing.T) {
	f := hebrewFace(t)
	// alef bet space 1 2 3: the letters reverse, the digits do not.
	text := string([]rune{alefHeb, betHeb}) + " 123"
	shaped, missing := f.ShapeGlyphs(text)
	if missing != 0 {
		t.Fatalf("%d characters have no glyph", missing)
	}
	// Bytes: alef 0, bet 2, space 4, '1' 5, '2' 6, '3' 7.
	if want := []int{5, 6, 7, 4, 2, 0}; !sameInts(clustersOf(shaped), want) {
		t.Errorf("drawn from bytes %v, want %v: the digits must stay in order while the letters reverse",
			clustersOf(shaped), want)
	}
}

// TestStackCutsARunWhereTheLevelChanges pins that direction cuts a run in its
// own right, and not only where it happens to coincide with a change of script.
//
// A number written inside Hebrew is the case that separates the two: the digits
// are of no script of their own and take Hebrew from the letters around them, so
// nothing but the embedding level distinguishes them — and they are at a deeper
// level, because a number reads left to right inside text that does not. Left in
// one run with the letters, they would be shaped and reversed as though they
// were letters, and "123" would be drawn as "321".
func TestStackCutsARunWhereTheLevelChanges(t *testing.T) {
	s := NewStack(hebrewFace(t))
	// alef bet 1 2 3 gimel: two-byte letters, so the digits are at bytes 4..6
	// and gimel at 7.
	text := string([]rune{alefHeb, betHeb}) + "123" + string(rune(gimel))
	runs, missing := s.ShapeRuns(text)
	if missing != 0 {
		t.Fatalf("%d characters have no glyph", missing)
	}
	if len(runs) != 3 {
		t.Fatalf("got %d runs, want 3 — the level boundary did not cut a run: %v", len(runs), runs)
	}
	wantStart := []int{7, 4, 0}
	wantLevel := []int{1, 2, 1}
	for i, r := range runs {
		if r.Start != wantStart[i] || r.Level != wantLevel[i] {
			t.Errorf("run %d begins at byte %d at level %d, want %d and %d",
				i, r.Start, r.Level, wantStart[i], wantLevel[i])
		}
	}
	if c := clustersOf(runs[1].Glyphs); !sameInts(c, []int{4, 5, 6}) {
		t.Errorf("the digits are drawn from bytes %v, want [4 5 6] — the number came out backwards", c)
	}
}

// TestBracketsAreMirroredInRightToLeftText is rule L4. A parenthesis is drawn as
// the one that mirrors it, and the mirroring is on the character, before the
// font is asked for a glyph — a font has no way to know which way the text runs.
func TestBracketsAreMirroredInRightToLeftText(t *testing.T) {
	f := hebrewFace(t)
	openGID, _ := f.GlyphID('(')
	closeGID, _ := f.GlyphID(')')
	if openGID == closeGID {
		t.Fatal("the fixture gives both brackets the same glyph and can prove nothing")
	}

	// "(א)" in a right-to-left paragraph. Reversed, the closing parenthesis is
	// drawn first — and drawn as an *opening* one, so the reader still sees a
	// bracket that opens on the left.
	glyphs, missing := f.ShapeGlyphs("(" + string(rune(alefHeb)) + ")")
	if missing != 0 {
		t.Fatalf("%d characters have no glyph", missing)
	}
	if len(glyphs) != 3 {
		t.Fatalf("got %d glyphs, want 3", len(glyphs))
	}
	if glyphs[0].GID != openGID {
		t.Errorf("the leftmost glyph is %d, want %d — the closing bracket was not mirrored", glyphs[0].GID, openGID)
	}
	if glyphs[2].GID != closeGID {
		t.Errorf("the rightmost glyph is %d, want %d", glyphs[2].GID, closeGID)
	}
	// It still came from the character the caller wrote: mirroring changes what
	// is drawn, not what the text says.
	if want := []int{3, 1, 0}; !sameInts(clustersOf(glyphs), want) {
		t.Errorf("drawn from bytes %v, want %v", clustersOf(glyphs), want)
	}

	// The same characters in a left-to-right paragraph are drawn as written.
	glyphs, _ = f.ShapeGlyphs("(a)")
	if glyphs[0].GID != openGID || glyphs[2].GID != closeGID {
		t.Error("a left-to-right run had its brackets mirrored")
	}
}

// TestJoiningSeesTheTextAsWritten is the ordering constraint between the two
// passes, and it is the one that is easy to get backwards.
//
// Joining asks what a letter's neighbours are, and the answer is a fact about
// the text, not about the page. Reversing before shaping gives every letter the
// wrong neighbours: the word is then joined wrongly *and* drawn backwards, which
// looks close enough to right that only a reader of the script will say so.
func TestJoiningSeesTheTextAsWritten(t *testing.T) {
	f := arabicFace(t)
	// beh beh alef: the first beh begins the word and takes the initial shape,
	// the second is medial, and alef — which joins only backwards — ends it.
	// Only beh has positional forms in this fixture, so alef stays glyph 5.
	glyphs, missing := f.ShapeGlyphs(string([]rune{beh, beh, alef}))
	if missing != 0 {
		t.Fatalf("%d characters have no glyph", missing)
	}
	// Drawn left to right: alef, the medial beh, the initial beh.
	want := []int{5, 3, 2}
	got := make([]int, len(glyphs))
	for i, g := range glyphs {
		got[i] = g.GID
	}
	if !sameInts(got, want) {
		t.Errorf("glyphs %v, want %v.\n"+
			"[5 2 4] means the text was reversed before it was joined, which is both wrong at once.", got, want)
	}
}

// TestStackReturnsRunsInVisualOrder is the same guarantee at the level a caller
// actually draws at: hand back runs in the order the pen meets them, so that
// drawing them in order at a continuing pen sets the line.
func TestStackReturnsRunsInVisualOrder(t *testing.T) {
	s := NewStack(hebrewFace(t))
	// A Hebrew sentence with a Latin word inside it, run together so that the
	// only thing cutting the runs is the direction. Each Hebrew letter is two
	// bytes: the first word is at 0..5, the Latin at 6..8, the second at 9..14.
	text := string([]rune{alefHeb, betHeb, gimel}) + "abc" + string([]rune{gimel, betHeb, alefHeb})
	runs, missing := s.ShapeRuns(text)
	if missing != 0 {
		t.Fatalf("%d characters have no glyph", missing)
	}
	if len(runs) != 3 {
		t.Fatalf("got %d runs, want 3", len(runs))
	}
	// Written: [Hebrew at 0] [Latin at 6] [Hebrew at 9]. Drawn: the other way
	// round, because the line as a whole runs right to left. The Latin is one
	// level deeper, which is what keeps it from reversing with the rest.
	wantStart := []int{9, 6, 0}
	wantLevel := []int{1, 2, 1}
	for i, r := range runs {
		if r.Start != wantStart[i] || r.Level != wantLevel[i] {
			t.Errorf("run %d begins at byte %d at level %d, want %d and %d",
				i, r.Start, r.Level, wantStart[i], wantLevel[i])
		}
	}
	// And within each run: the Hebrew reversed, the Latin not.
	if c := clustersOf(runs[1].Glyphs); !sameInts(c, []int{6, 7, 8}) {
		t.Errorf("the Latin run is drawn from bytes %v, want [6 7 8] — it was reversed with the line", c)
	}
	if c := clustersOf(runs[2].Glyphs); !sameInts(c, []int{4, 2, 0}) {
		t.Errorf("the first Hebrew run is drawn from bytes %v, want [4 2 0]", c)
	}
}

// drawnAt is where each glyph of a shaped run is actually painted. It is what
// Face.Draw does: the pen accumulates advances, and an offset displaces the
// glyph without moving the pen.
func drawnAt(glyphs []Glyph) []float64 {
	out := make([]float64, len(glyphs))
	pen := 0.0
	for i, g := range glyphs {
		out[i] = pen + g.XOffset
		pen += g.XAdvance
	}
	return out
}

// Anchors for a right-to-left cursive fixture. They are the mirror image of the
// Latin ones in cursive_test.go, and deliberately so: in a script written right
// to left the stroke *leaves* a letter at its left edge and *arrives* at the
// next one's right edge, so an exit is at a small x and an entry at a large one.
const (
	rtlExitX  = 60
	rtlEntryX = 440
	rtlWidth  = 500
)

// TestCursiveJointsSurviveTheReversal is the positioning half of direction, and
// the part a reversal quietly breaks.
//
// Cursive attachment says where two letters meet. The rule is stated over the
// text as written, but the arithmetic that carries it out is about where the pen
// will be — and the pen meets a right-to-left run from the other end. Applying
// the left-to-right form of it and then reversing leaves every letter displaced
// by the width of its neighbour: the strokes no longer meet, which is what makes
// joined script look joined.
func TestCursiveJointsSurviveTheReversal(t *testing.T) {
	// Three Arabic letters, each with the anchors of a middle letter.
	const (
		gA = 1
		gB = 2
		gC = 3
	)
	anchors := []fonttest.CursiveAnchor{
		{Glyph: gA, HasExit: true, Exit: fonttest.Anchor{X: rtlExitX}},
		{
			Glyph:    gB,
			HasEntry: true, Entry: fonttest.Anchor{X: rtlEntryX},
			HasExit: true, Exit: fonttest.Anchor{X: rtlExitX},
		},
		{Glyph: gC, HasEntry: true, Entry: fonttest.Anchor{X: rtlEntryX}},
	}
	data := fonttest.SFNT(fonttest.SFNTOptions{
		Name: "ArabicCursive",
		Glyphs: []fonttest.Glyph{
			{Rune: beh, Advance: rtlWidth, HasShape: true},
			{Rune: 0x062A, Advance: rtlWidth, HasShape: true}, // TEH
			{Rune: 0x062C, Advance: rtlWidth, HasShape: true}, // JEEM
		},
		Extra: map[string][]byte{"GPOS": fonttest.GPOSCursive(anchors, 0)},
	})
	f, err := Load(data)
	if err != nil {
		t.Fatalf("loading: %v", err)
	}

	word := string([]rune{beh, 0x062A, 0x062C})
	glyphs, missing := f.ShapeGlyphs(word)
	if missing != 0 {
		t.Fatalf("%d characters have no glyph", missing)
	}
	if len(glyphs) != 3 {
		t.Fatalf("got %d glyphs, want 3", len(glyphs))
	}
	at := drawnAt(glyphs)

	// The run comes back in the order it is drawn, so the letter written first
	// is the one drawn last. Each joint is between a letter's exit and the next
	// letter's entry, and they have to land on the same point.
	for logical := 0; logical < 2; logical++ {
		leaving := len(glyphs) - 1 - logical
		arriving := leaving - 1
		exit := at[leaving] + rtlExitX
		entry := at[arriving] + rtlEntryX
		if exit != entry {
			t.Errorf("joint %d: the stroke leaves at %v and arrives at %v — a gap of %v",
				logical, exit, entry, entry-exit)
		}
	}
	// And the word occupies a sane amount of space: joined, so narrower than
	// three separate letters, and not negative, which is what an off-by-one in
	// the direction produces.
	width := MeasureGlyphs(glyphs, 1000)
	if width <= 0 || width >= 3*rtlWidth {
		t.Errorf("the joined word measures %v, want something between 0 and %d", width, 3*rtlWidth)
	}
}

// TestMarkStaysOnItsBaseAfterReversal is the same question for accents.
//
// A mark carries no advance, so it is placed by an offset from the pen — and
// after reversal the pen reaches the mark *before* its base rather than after,
// so the offset that was right is now wrong by the base's whole width. The
// symptom is an accent sitting one letter away from the letter it belongs to,
// consistently, in every vocalised Arabic or Hebrew word.
func TestMarkStaysOnItsBaseAfterReversal(t *testing.T) {
	const (
		gBase     = 1
		gMark     = 2
		baseAnchX = 150
		baseAnchY = 600
		markAnchX = 20
		markAnchY = 0
	)
	gpos := fonttest.GPOSMarkToBase(4,
		[]fonttest.MarkAttachment{{Glyph: gMark, Class: 0, Anchor: fonttest.Anchor{X: markAnchX, Y: markAnchY}}},
		[]fonttest.BaseAttachment{{Glyph: gBase, Anchors: map[int]fonttest.Anchor{
			0: {X: baseAnchX, Y: baseAnchY},
		}}},
	)
	data := fonttest.SFNT(fonttest.SFNTOptions{
		Name: "ArabicMark",
		Glyphs: []fonttest.Glyph{
			{Rune: beh, Advance: 500, HasShape: true},
			{Rune: fatha, Advance: 0, HasShape: true},
		},
		Extra: map[string][]byte{
			"GPOS": gpos,
			"GDEF": fonttest.GDEF(map[int]int{gBase: classBase, gMark: classMark}),
		},
	})
	f, err := Load(data)
	if err != nil {
		t.Fatalf("loading: %v", err)
	}

	// Two letters, the second of them vowelled: beh, beh, fatha. The mark
	// belongs to the second beh, which — the word being Arabic — is drawn to
	// the left of the first.
	glyphs, missing := f.ShapeGlyphs(string([]rune{beh, beh, fatha}))
	if missing != 0 {
		t.Fatalf("%d characters have no glyph", missing)
	}
	if len(glyphs) != 3 {
		t.Fatalf("got %d glyphs, want 3", len(glyphs))
	}
	at := drawnAt(glyphs)

	// Find the mark and its base by where they came from: the mark is the last
	// character written, its base the one before it.
	var markAt, baseAt float64
	found := 0
	for i, g := range glyphs {
		switch g.Cluster {
		case 4: // the fatha, two two-byte letters in
			markAt = at[i]
			found++
		case 2: // the beh it is written on
			baseAt = at[i]
			found++
		}
	}
	if found != 2 {
		t.Fatalf("the mark or its base is not in the output: clusters %v", clustersOf(glyphs))
	}
	if want := baseAt + baseAnchX - markAnchX; markAt != want {
		t.Errorf("the mark is drawn at %v and its base at %v, so the anchors are %v apart;\n"+
			"they should coincide, putting the mark at %v", markAt, baseAt, markAt-want, want)
	}
	if glyphs[0].YOffset == 0 && glyphs[1].YOffset == 0 && glyphs[2].YOffset == 0 {
		t.Error("nothing was lifted vertically, so the fixture's attachment never happened")
	}
}

// TestSpanShapingIsAlsoInVisualOrder pins the older, span-shaped API to the same
// promise. It cannot express a mark's offset, which is why ShapeGlyphs exists —
// but it can and must still put the letters in the order they are drawn.
func TestSpanShapingIsAlsoInVisualOrder(t *testing.T) {
	f := hebrewFace(t)
	spans, missing := f.Shape(string([]rune{alefHeb, betHeb, gimel}))
	if missing != 0 {
		t.Fatalf("%d characters have no glyph", missing)
	}
	var codes []byte
	for _, s := range spans {
		codes = append(codes, s.Codes...)
	}
	if len(codes) != 6 {
		t.Fatalf("got %d bytes of codes, want 6", len(codes))
	}
	gimelGID, _ := f.GlyphID(gimel)
	if first := int(codes[0])<<8 | int(codes[1]); first != gimelGID {
		t.Errorf("the first glyph shown is %d, want %d (gimel, the last letter)", first, gimelGID)
	}
	// Shape and MeasureShaped have to agree, or a caller lays out to one width
	// and draws another.
	if got, want := f.MeasureShaped(string([]rune{alefHeb, betHeb, gimel}), 1000), 3*500.0; got != want {
		t.Errorf("MeasureShaped gives %v, want %v", got, want)
	}
}

// TestKerningUsesThePairAsTheFontStatesIt is the one thing reversal changes
// about kerning. The pair a font declares is the pair as the text is written; a
// reversed run meets those two glyphs the other way round, and looking the kern
// up in the order the pen sees finds either nothing or the wrong pair.
func TestKerningUsesThePairAsTheFontStatesIt(t *testing.T) {
	const kern = -80
	data := fonttest.SFNT(fonttest.SFNTOptions{
		Name: "HebrewKern",
		Glyphs: []fonttest.Glyph{
			{Rune: alefHeb, Advance: 500, HasShape: true}, // 1
			{Rune: betHeb, Advance: 500, HasShape: true},  // 2
		},
		// alef then bet, as the text is written.
		Extra: map[string][]byte{"GPOS": fonttest.GPOS([]fonttest.KernPair{{Left: 1, Right: 2, Adjust: kern}})},
	})
	f, err := Load(data)
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	word := string([]rune{alefHeb, betHeb})

	glyphs, _ := f.ShapeGlyphs(word)
	if want := 2*500.0 + kern; MeasureGlyphs(glyphs, 1000) != want {
		t.Errorf("the pair measures %v, want %v — the kern was not found in the reversed run",
			MeasureGlyphs(glyphs, 1000), want)
	}
	// The span path reads the same table by a different route and must agree.
	if got, want := f.MeasureShaped(word, 1000), 2*500.0+float64(kern); got != want {
		t.Errorf("MeasureShaped gives %v, want %v", got, want)
	}
	spans, _ := f.Shape(word)
	var adjust float64
	for _, s := range spans {
		adjust += s.Adjust
	}
	// A negative kern closes the gap, and TJ subtracts its number, so it shows
	// up here with the sign flipped.
	if adjust != -kern {
		t.Errorf("the spans carry an adjustment of %v, want %v", adjust, -kern)
	}
}
