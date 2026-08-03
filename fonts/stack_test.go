package fonts

import (
	"testing"

	"github.com/mgilbir/pdf0/internal/fonttest"
)

// Falling back from one face to the next.

// latinFace covers a-z plus a space, and carries a ligature and a kern pair so
// that a test can tell whether a run was shaped as a run.
func latinFace(t *testing.T) *Face {
	t.Helper()
	glyphs := []fonttest.Glyph{
		{Rune: 'f', Advance: 300, HasShape: true},    // 1
		{Rune: 'i', Advance: 200, HasShape: true},    // 2
		{Rune: 'a', Advance: 500, HasShape: true},    // 3
		{Rune: 'b', Advance: 500, HasShape: true},    // 4
		{Rune: ' ', Advance: 250},                    // 5
		{Rune: 0xFB01, Advance: 450, HasShape: true}, // 6, the fi ligature
	}
	f, err := Load(fonttest.SFNT(fonttest.SFNTOptions{
		Name:   "Latin",
		Glyphs: glyphs,
		Extra: map[string][]byte{
			"GSUB": fonttest.GSUB([]fonttest.Ligature{{Components: []int{1, 2}, Glyph: 6}}),
			"GPOS": fonttest.GPOS([]fonttest.KernPair{{Left: 3, Right: 4, Adjust: -80}}),
		},
	}))
	if err != nil {
		t.Fatalf("loading the Latin face: %v", err)
	}
	return f
}

// cjkFace covers three characters the Latin face does not.
func cjkFace(t *testing.T) *Face {
	t.Helper()
	f, err := Load(fonttest.SFNT(fonttest.SFNTOptions{
		Name: "CJK",
		Glyphs: []fonttest.Glyph{
			{Rune: '日', Advance: 1000, HasShape: true},
			{Rune: '本', Advance: 1000, HasShape: true},
			{Rune: '語', Advance: 1000, HasShape: true},
		},
	}))
	if err != nil {
		t.Fatalf("loading the CJK face: %v", err)
	}
	return f
}

// TestTextIsSplitByWhatEachFaceHas is the basic claim.
func TestTextIsSplitByWhatEachFaceHas(t *testing.T) {
	s := NewStack(latinFace(t), cjkFace(t))
	runs, missing := s.ShapeRuns("ab日本語ab")
	if missing != 0 {
		t.Fatalf("%d characters could not be set", missing)
	}
	if len(runs) != 3 {
		t.Fatalf("got %d runs, want 3", len(runs))
	}
	want := []struct {
		face  string
		start int
		count int
	}{
		{"Latin", 0, 2},
		{"CJK", 2, 3},
		{"Latin", 11, 2},
	}
	for i, w := range want {
		if runs[i].Face.Name() != w.face {
			t.Errorf("run %d is set in %q, want %q", i, runs[i].Face.Name(), w.face)
		}
		if runs[i].Start != w.start {
			t.Errorf("run %d starts at byte %d, want %d", i, runs[i].Start, w.start)
		}
		if len(runs[i].Glyphs) != w.count {
			t.Errorf("run %d has %d glyphs, want %d", i, len(runs[i].Glyphs), w.count)
		}
	}
}

// TestRunsAreShapedAsRuns is the reason this is not a per-character loop.
//
// Ligatures and kerning are things a font says about a sequence of its own
// glyphs. Choosing a face per character and shaping each one alone loses every
// one of them, and the result looks almost right — which is why it needs a test
// that would notice.
func TestRunsAreShapedAsRuns(t *testing.T) {
	s := NewStack(latinFace(t), cjkFace(t))

	runs, _ := s.ShapeRuns("fi")
	if len(runs) != 1 || len(runs[0].Glyphs) != 1 {
		t.Fatalf("fi produced %d runs; the ligature did not apply", len(runs))
	}
	if runs[0].Glyphs[0].GID != 6 {
		t.Errorf("fi became glyph %d, want the ligature (6)", runs[0].Glyphs[0].GID)
	}

	runs, _ = s.ShapeRuns("ab")
	if len(runs) != 1 || len(runs[0].Glyphs) != 2 {
		t.Fatalf("ab produced %d runs", len(runs))
	}
	if got := runs[0].Glyphs[0].XAdvance; got != 500-80 {
		t.Errorf("the kern was not applied: advance %v, want %v", got, 500-80)
	}
}

// TestACombiningMarkStaysWithItsBase is the correctness point a per-character
// choice gets wrong.
//
// A mark is positioned by anchors the font states relative to its own letters.
// Taking the letter from one face and the accent from another positions the
// accent with anchors from a font that never saw the letter — so it lands at
// the origin, on top of whatever is there. The unit of choice is therefore the
// base together with its marks.
func TestACombiningMarkStaysWithItsBase(t *testing.T) {
	// The first face has the letter but not the accent; the second has both.
	withoutAccent := latinFace(t)
	withAccent, err := Load(fonttest.SFNT(fonttest.SFNTOptions{
		Name: "Accented",
		Glyphs: []fonttest.Glyph{
			{Rune: 'a', Advance: 520, HasShape: true},
			{Rune: 0x0301, Advance: 0, HasShape: true}, // combining acute
		},
	}))
	if err != nil {
		t.Fatalf("loading: %v", err)
	}

	s := NewStack(withoutAccent, withAccent)
	runs, missing := s.ShapeRuns("á")
	if missing != 0 {
		t.Fatalf("%d characters could not be set", missing)
	}
	if len(runs) != 1 {
		t.Fatalf("the letter and its accent were split across %d runs", len(runs))
	}
	if runs[0].Face.Name() != "Accented" {
		t.Errorf("the pair was set in %q, want the face that has both", runs[0].Face.Name())
	}
}

// TestABaseWithoutAMarkStillPrefersTheFirstFace pins that the whole-unit
// preference does not reorder the list for ordinary text: the first face that
// can set something is still the one that does.
func TestABaseWithoutAMarkStillPrefersTheFirstFace(t *testing.T) {
	first := latinFace(t)
	second, err := Load(fonttest.SFNT(fonttest.SFNTOptions{
		Name:   "Second",
		Glyphs: []fonttest.Glyph{{Rune: 'a', Advance: 999, HasShape: true}},
	}))
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	runs, _ := NewStack(first, second).ShapeRuns("a")
	if runs[0].Face.Name() != "Latin" {
		t.Errorf("set in %q, want the first face that has the character", runs[0].Face.Name())
	}
}

// TestClustersAreOffsetsIntoTheWholeString pins what a caller needs to map a
// glyph back to the text. A cluster relative to its run would make every run
// after the first point at the wrong characters.
func TestClustersAreOffsetsIntoTheWholeString(t *testing.T) {
	s := NewStack(latinFace(t), cjkFace(t))
	const text = "ab日本語ab"
	runs, _ := s.ShapeRuns(text)

	var seen []int
	for _, r := range runs {
		for _, g := range r.Glyphs {
			seen = append(seen, g.Cluster)
		}
	}
	want := []int{0, 1, 2, 5, 8, 11, 12}
	if len(seen) != len(want) {
		t.Fatalf("got %d glyphs, want %d", len(seen), len(want))
	}
	for i := range seen {
		if seen[i] != want[i] {
			t.Errorf("glyph %d has cluster %d, want %d (all: %v)", i, seen[i], want[i], seen)
		}
		if seen[i] >= len(text) {
			t.Errorf("glyph %d has cluster %d, past the end of the text", i, seen[i])
		}
	}
}

// TestACharacterNoFaceHasIsReportedNotDropped pins that a character nothing can
// set becomes a visible box and a count, rather than disappearing. Silently
// dropping it produces text that reads correctly to the program and wrongly to
// a person.
func TestACharacterNoFaceHasIsReportedNotDropped(t *testing.T) {
	s := NewStack(latinFace(t), cjkFace(t))
	runs, missing := s.ShapeRuns("a☃b") // a snowman neither face has
	if missing != 1 {
		t.Errorf("%d characters reported missing, want 1", missing)
	}
	var glyphs int
	for _, r := range runs {
		glyphs += len(r.Glyphs)
	}
	if glyphs != 3 {
		t.Errorf("got %d glyphs for 3 characters; one was dropped rather than set as .notdef", glyphs)
	}
}

// TestRunsCoverTheInputExactly is the property that makes drawing them in order
// correct: no gap, no overlap, nothing out of order.
func TestRunsCoverTheInputExactly(t *testing.T) {
	s := NewStack(latinFace(t), cjkFace(t))
	for _, text := range []string{"", "a", "日", "ab日本語ab", "日ab語", "áb", "☃"} {
		runs, _ := s.ShapeRuns(text)
		at := 0
		for i, r := range runs {
			if r.Start != at {
				t.Errorf("%q: run %d starts at %d, want %d", text, i, r.Start, at)
			}
			// The run reaches to the start of the next, or to the end.
			end := len(text)
			if i+1 < len(runs) {
				end = runs[i+1].Start
			}
			at = end
		}
		if at != len(text) {
			t.Errorf("%q: the runs cover %d bytes of %d", text, at, len(text))
		}
	}
}

// TestEmptyStackAndEmptyTextAreSafe pins the degenerate cases, which a layout
// engine hits constantly on empty elements.
func TestEmptyStackAndEmptyTextAreSafe(t *testing.T) {
	if runs, missing := NewStack().ShapeRuns("abc"); runs != nil || missing != 0 {
		t.Errorf("a stack with no faces gave %d runs", len(runs))
	}
	if runs, _ := NewStack(latinFace(t)).ShapeRuns(""); runs != nil {
		t.Errorf("empty text gave %d runs", len(runs))
	}
	// A nil face in the list is ignored rather than panicking on use.
	s := NewStack(nil, latinFace(t), nil)
	if len(s.Faces()) != 1 {
		t.Errorf("the stack kept %d faces, want 1", len(s.Faces()))
	}
	if runs, _ := s.ShapeRuns("ab"); len(runs) != 1 {
		t.Errorf("got %d runs from a stack built with nil entries", len(runs))
	}
}

// TestMeasureAcrossFacesSumsTheRuns pins that measuring text with fallback is
// not measuring it in any one font.
func TestMeasureAcrossFacesSumsTheRuns(t *testing.T) {
	s := NewStack(latinFace(t), cjkFace(t))
	// "ab" kerns to 500-80 + 500; "日" is 1000. At size 1000 the units are
	// thousandths of an em, so the width is the sum of the advances.
	const size = 1000
	got := s.Measure("ab日", size)
	want := float64(500-80+500+1000) * size / 1000
	if got != want {
		t.Errorf("measured %v, want %v", got, want)
	}
	if empty := s.Measure("", size); empty != 0 {
		t.Errorf("empty text measured %v", empty)
	}
}

// TestCoversAsksTheWholeStack pins the question a caller asks before adding
// another fallback.
func TestCoversAsksTheWholeStack(t *testing.T) {
	s := NewStack(latinFace(t), cjkFace(t))
	for _, r := range []rune{'a', 'f', '日', '語'} {
		if !s.Covers(r) {
			t.Errorf("the stack does not cover %q, but one of its faces has it", r)
		}
	}
	for _, r := range []rune{'☃', 'ю'} {
		if s.Covers(r) {
			t.Errorf("the stack claims to cover %q", r)
		}
	}
}

// TestEachFaceRecordsOnlyItsOwnGlyphs pins that subsetting still works across a
// fallback: a face must be subset to what *it* set, and a face that set nothing
// must not be dragged into the file.
func TestEachFaceRecordsOnlyItsOwnGlyphs(t *testing.T) {
	latin, cjk := latinFace(t), cjkFace(t)
	unused, err := Load(fonttest.SFNT(fonttest.SFNTOptions{
		Name:   "Unused",
		Glyphs: []fonttest.Glyph{{Rune: 'z', Advance: 500, HasShape: true}},
	}))
	if err != nil {
		t.Fatalf("loading: %v", err)
	}

	s := NewStack(latin, cjk, unused)
	if _, missing := s.ShapeRuns("ab日"); missing != 0 {
		t.Fatalf("%d characters could not be set", missing)
	}
	if got := len(latin.Used()); got != 2 {
		t.Errorf("the Latin face recorded %d glyphs, want 2", got)
	}
	if got := len(cjk.Used()); got != 1 {
		t.Errorf("the CJK face recorded %d glyphs, want 1", got)
	}
	if got := len(unused.Used()); got != 0 {
		t.Errorf("a face that set nothing recorded %d glyphs", got)
	}
}
