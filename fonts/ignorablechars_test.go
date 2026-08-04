package fonts

import (
	"strings"
	"testing"
)

// The characters nothing is drawn for.
//
// Unicode's Default_Ignorable_Code_Point property says which they are. Before
// this was handled, a face that happened to map one got a glyph on the page for
// it — and the worst of those is the soft hyphen, which is what HTML's &shy; is
// and which WinAnsi gives a code of its own: a word marked as breakable came out
// with a hyphen through the middle of it whether it broke there or not.
//
// The expected glyph counts below were measured against HarfBuzz over the same
// face, with HB_BUFFER_FLAG_REMOVE_DEFAULT_IGNORABLES set — the flag that asks
// it for the policy this package has, dropping the characters rather than
// mapping them to an invisible glyph. All twelve cases agree.

// TestNothingIsDrawnForAnIgnorableCharacter is the defect stated as a test.
func TestNothingIsDrawnForAnIgnorableCharacter(t *testing.T) {
	f, err := NotoSans()
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	plain, _ := f.ShapeGlyphs("xy")
	if len(plain) != 2 {
		t.Fatalf("the two-letter control shaped to %d glyphs", len(plain))
	}

	for _, tc := range []struct {
		r    rune
		why  string
		what string
	}{
		{0x00AD, "soft hyphen", "marks where a word may break; HTML writes it as &shy;"},
		{0x200B, "zero width space", "an opportunity to break, not a space"},
		{0x200E, "left-to-right mark", "a direction, not a letter"},
		{0x202D, "left-to-right override", "a direction, not a letter"},
		{0x2060, "word joiner", "a refusal to break"},
		{0xFEFF, "zero width no-break space", "a byte order mark that reached the text"},
		{0xFE0F, "variation selector 16", "a request for a variant this face does not state"},
		{0x034F, "combining grapheme joiner", "a barrier between two characters"},
		{0x1D173, "musical begin beam", "notation, not text"},
		{0xE0041, "tag letter A", "a language tag"},
	} {
		s := "x" + string(tc.r) + "y"
		glyphs, _ := f.ShapeGlyphs(s)
		if len(glyphs) != len(plain) {
			t.Errorf("%s (U+%04X) — %s — shaped to %d glyphs; \"xy\" shapes to %d, "+
				"and nothing may be drawn for it", tc.why, tc.r, tc.what, len(glyphs), len(plain))
			continue
		}
		for i := range glyphs {
			if glyphs[i].GID != plain[i].GID || glyphs[i].XAdvance != plain[i].XAdvance {
				t.Errorf("%s (U+%04X): glyph %d is %d at %v, want %d at %v",
					tc.why, tc.r, i, glyphs[i].GID, glyphs[i].XAdvance,
					plain[i].GID, plain[i].XAdvance)
			}
		}
	}
}

// TestASimpleFaceDrawsNothingForThemEither pins the other path.
//
// It is the more visible of the two: WinAnsi gives the soft hyphen a code, so a
// simple face maps it to a real hyphen — and a simple face has no shaping pass
// afterwards that could take it back out again.
func TestASimpleFaceDrawsNothingForThemEither(t *testing.T) {
	f, err := NotoSansSimple()
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	plain, _ := f.ShapeGlyphs("xy")
	got, _ := f.ShapeGlyphs("x\u00ADy")
	if len(got) != len(plain) {
		t.Fatalf("a soft hyphen in a simple face shaped to %d glyphs, want %d", len(got), len(plain))
	}
	for i := range got {
		if got[i].GID != plain[i].GID {
			t.Errorf("glyph %d is %d, want %d", i, got[i].GID, plain[i].GID)
		}
	}
	// And the width a caller lays out with must match.
	if w, plainW := f.Measure("x\u00ADy", 12), f.Measure("xy", 12); w != plainW {
		t.Errorf("a soft hyphen measured %v against %v; it takes no width", w, plainW)
	}
}

// TestTheHangulFillersAreNotHidden pins the one part of the property this
// package deliberately does not act on.
//
// U+115F, U+1160, U+3164 and U+FFA0 are default-ignorable and are also letters,
// used to write a syllable with a slot left empty. They take width on the page,
// and hiding them collapses the syllable into a different one. HarfBuzz excludes
// them for the same reason.
func TestTheHangulFillersAreNotHidden(t *testing.T) {
	f, err := NotoSans()
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	for _, tc := range []struct {
		s    string
		want int
		why  string
	}{
		{"\u1100\u1160\u11A8", 3, "a jamo syllable whose vowel slot is a filler"},
		{"\u3164", 1, "the filler on its own"},
		{"x\u115Fy", 3, "a choseong filler between letters"},
		{"x\uFFA0y", 3, "a halfwidth filler between letters"},
	} {
		glyphs, _ := f.ShapeGlyphs(tc.s)
		if len(glyphs) != tc.want {
			t.Errorf("%s shaped to %d glyphs, want %d — the fillers occupy width",
				tc.why, len(glyphs), tc.want)
		}
	}
}

// TestAnIgnorableCharacterStillDoesItsWork is the other half, and the reason the
// drop happens where it does rather than on the way in.
//
// A combining grapheme joiner exists to stand between two characters and stop
// them composing. Removing it before normalisation would remove the barrier
// along with the character, and "a" + CGJ + acute would come out as the one
// letter it was written to prevent.
func TestAnIgnorableCharacterStillDoesItsWork(t *testing.T) {
	f, err := NotoSans()
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	composed, _ := f.ShapeGlyphs("á")
	if len(composed) != 1 {
		t.Fatalf("a + acute shaped to %d glyphs; the fixture assumption is gone", len(composed))
	}
	blocked, _ := f.ShapeGlyphs("a\u034F\u0301")
	if len(blocked) != 2 {
		t.Errorf("a + CGJ + acute shaped to %d glyphs, want 2: the joiner was removed "+
			"before it could stop the composition", len(blocked))
	}

	// The same for the two join controls, which are removed later still — after
	// the rules that are about them have run.
	joined, _ := f.ShapeGlyphs("क्ष")
	explicit, _ := f.ShapeGlyphs("\u0915\u094D\u200D\u0937")
	if len(joined) == 0 || len(explicit) == 0 {
		t.Fatal("the Devanagari fixture shaped to nothing")
	}
	if explicit[len(explicit)-1].GID == joined[len(joined)-1].GID &&
		len(explicit) == len(joined) && explicit[0].GID == joined[0].GID {
		t.Error("a zero width joiner asking for a half form changed nothing; " +
			"it was dropped before the shaper could read it")
	}
	for _, g := range explicit {
		if g.GID == 0 {
			t.Error("the joiner reached the page as an undefined glyph")
		}
	}
}

// TestTheIgnorableTableIsUnicodesOwn guards the generated table against being
// edited by hand into something narrower, which is exactly how this defect
// would come back.
func TestTheIgnorableTableIsUnicodesOwn(t *testing.T) {
	// Sorted, non-overlapping, non-adjacent: what the binary search needs and
	// what the generator's merge produces.
	for i := range defaultIgnorableRanges {
		r := defaultIgnorableRanges[i]
		if r.lo > r.hi {
			t.Errorf("range %d is %04X..%04X, which is empty", i, r.lo, r.hi)
		}
		if i > 0 && r.lo <= defaultIgnorableRanges[i-1].hi+1 {
			t.Errorf("range %d (%04X..%04X) touches or overlaps the one before it (%04X..%04X)",
				i, r.lo, r.hi, defaultIgnorableRanges[i-1].lo, defaultIgnorableRanges[i-1].hi)
		}
	}
	// Spot checks in and out, so a table regenerated from the wrong property
	// fails here rather than on the page.
	for _, r := range []rune{0x00AD, 0x034F, 0x061C, 0x180F, 0x200B, 0x200D, 0x202E,
		0x2064, 0xFE0F, 0xFEFF, 0xFFF4, 0x1BCA0, 0x1D17A, 0xE0100} {
		if !isDefaultIgnorable(r) {
			t.Errorf("U+%04X is Default_Ignorable_Code_Point and the table says otherwise", r)
		}
	}
	for _, r := range []rune{'a', ' ', 0x00AC, 0x00AE, 0x0350, 0x2010, 0x200A, 0x2070,
		0xFE10, 0xFFF9, 0x1D17B, 0xE1000} {
		if isDefaultIgnorable(r) {
			t.Errorf("U+%04X is not Default_Ignorable_Code_Point and the table says it is", r)
		}
	}
}

// TestHidingCostsNothingForOrdinaryText pins the shortcut. Every string the
// pipeline sets is asked this question, and almost every answer is no; a pass
// that copied every run to remove nothing would cost more than the property is
// worth.
func TestHidingCostsNothingForOrdinaryText(t *testing.T) {
	runes := []rune("The quick brown fox jumps over the lazy dog.")
	offsets := make([]int, len(runes))
	for i := range offsets {
		offsets[i] = i
	}
	gotR, gotO := dropHiddenCharacters(runes, offsets)
	if &gotR[0] != &runes[0] || &gotO[0] != &offsets[0] {
		t.Error("a run with nothing to drop was copied")
	}

	// And it does copy when it must, without disturbing what is left.
	withOne := []rune("x­y")
	offs := []int{0, 1, 3}
	gotR, gotO = dropHiddenCharacters(withOne, offs)
	if string(gotR) != "xy" {
		t.Errorf("dropping gave %q, want %q", string(gotR), "xy")
	}
	if len(gotO) != 2 || gotO[0] != 0 || gotO[1] != 3 {
		t.Errorf("the offsets came out %v, want [0 3] — what is left must still "+
			"point at where it came from", gotO)
	}
}

// TestARunOfNothingButIgnorablesIsEmpty is the boundary. A string that is
// entirely instructions has nothing to draw, and must come back as nothing
// rather than as an undefined glyph or a panic.
func TestARunOfNothingButIgnorablesIsEmpty(t *testing.T) {
	f, err := NotoSans()
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	for _, s := range []string{
		"\u00AD",       // a soft hyphen alone
		"\u200B\u200B", // two zero width spaces
		"\uFEFF",       // a byte order mark
		"\u202A\u202C", // an embedding opened and closed
		strings.Repeat("\u00AD", 64),
	} {
		glyphs, missing := f.ShapeGlyphs(s)
		if len(glyphs) != 0 {
			t.Errorf("%q shaped to %d glyphs, want none", s, len(glyphs))
		}
		if missing != 0 {
			t.Errorf("%q reported %d characters missing; nothing was asked of the font", s, missing)
		}
		if w := f.Measure(s, 12); w != 0 {
			t.Errorf("%q measured %v, want 0", s, w)
		}
	}
}

// TestMeasuringAndDrawingAgreeAboutIgnorables is the invariant the fix is
// really about, and the one a caller depends on.
//
// A layout engine measures a word to decide whether it fits on the line, then
// draws it. Those are different calls on different paths — Measure walks
// characters, MeasureShaped walks the flattened tables, ShapeGlyphs walks the
// lookup list — and if they disagree about a character, the line is filled to
// one width and painted at another. Text overruns the column or stops short of
// it, and neither is visible in any single call's output.
//
// A soft hyphen is the case that broke all three at once, and every path had to
// be fixed separately, so this asserts them together.
func TestMeasuringAndDrawingAgreeAboutIgnorables(t *testing.T) {
	composite, err := NotoSans()
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	simple, err := NotoSansSimple()
	if err != nil {
		t.Fatalf("loading: %v", err)
	}

	const size = 11
	for _, tc := range []struct {
		with, without string
		why           string
	}{
		{"un\u00ADbreak\u00ADable", "unbreakable", "soft hyphens, as HTML writes &shy;"},
		{"a\u200Bb", "ab", "a zero width space"},
		{"a\uFEFFb", "ab", "a byte order mark in the middle of the text"},
		{"\u202Dabc\u202C", "abc", "an override around a word"},
	} {
		for _, f := range []struct {
			face *Face
			kind string
		}{{composite, "composite"}, {simple, "simple"}} {
			// Every way of asking must give the same answer for both strings.
			plain, marked := f.face.Measure(tc.without, size), f.face.Measure(tc.with, size)
			if plain != marked {
				t.Errorf("%s, %s face: Measure says %v with %s and %v without",
					tc.why, f.kind, marked, tc.why, plain)
			}
			plain, marked = f.face.MeasureShaped(tc.without, size), f.face.MeasureShaped(tc.with, size)
			if plain != marked {
				t.Errorf("%s, %s face: MeasureShaped says %v against %v", tc.why, f.kind, marked, plain)
			}

			// And what is drawn must occupy what was measured.
			glyphs, _ := f.face.ShapeGlyphs(tc.with)
			if drawn := MeasureGlyphs(glyphs, size); drawn != f.face.MeasureShaped(tc.with, size) {
				t.Errorf("%s, %s face: the glyphs occupy %v and MeasureShaped said %v",
					tc.why, f.kind, drawn, f.face.MeasureShaped(tc.with, size))
			}

			// Encoding is the other half of drawing on the span path: a code
			// emitted for a character nothing is drawn for is a glyph on the
			// page that no measurement accounted for.
			withCodes, _ := f.face.Encode(tc.with)
			withoutCodes, _ := f.face.Encode(tc.without)
			if len(withCodes) != len(withoutCodes) {
				t.Errorf("%s, %s face: Encode emitted %d bytes with and %d without",
					tc.why, f.kind, len(withCodes), len(withoutCodes))
			}
		}
	}
}
