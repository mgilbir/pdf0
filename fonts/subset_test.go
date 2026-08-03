package fonts

import (
	"testing"

	"github.com/mgilbir/pdf0/internal/font"
	"github.com/mgilbir/pdf0/internal/fonttest"
)

// The subsetter checked against this module's own font reader: whatever it
// emits is parsed back, and the questions asked of it are the ones a renderer
// and a validator ask.

func loadTestFace(t *testing.T, glyphs ...fonttest.Glyph) *Face {
	t.Helper()
	f, err := Load(fonttest.SFNT(fonttest.SFNTOptions{Name: "Probe", Glyphs: glyphs}))
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	return f
}

func alphabet() []fonttest.Glyph {
	var gs []fonttest.Glyph
	for r := 'a'; r <= 'z'; r++ {
		gs = append(gs, fonttest.Glyph{Rune: r, Advance: 500 + int(r-'a'), HasShape: true})
	}
	return gs
}

// TestSubsetKeepsWhatWasUsedAndDropsTheRest is the property the whole exercise
// is for. A glyph that was encoded must still have an outline; one that was not
// must not, because its bytes are the saving.
func TestSubsetKeepsWhatWasUsedAndDropsTheRest(t *testing.T) {
	f := loadTestFace(t, alphabet()...)
	if _, missing := f.Encode("abc"); missing != 0 {
		t.Fatalf("%d runes missing from the fixture", missing)
	}

	data, err := f.Subset()
	if err != nil {
		t.Fatalf("subsetting: %v", err)
	}
	sub := font.ParseSFNT(data, 1<<20)
	if sub == nil {
		t.Fatal("this module's own reader rejected the subset font")
	}
	if sub.NumGlyphs != f.prog.NumGlyphs {
		t.Errorf("NumGlyphs = %d, want %d unchanged: indices are retained, not renumbered",
			sub.NumGlyphs, f.prog.NumGlyphs)
	}
	for _, r := range "abc" {
		gid, _ := f.GlyphID(r)
		if !sub.GlyphNonEmpty[gid] {
			t.Errorf("glyph %d (%q) was used but has no outline in the subset", gid, r)
		}
	}
	for _, r := range "xyz" {
		gid, _ := f.GlyphID(r)
		if sub.GlyphNonEmpty[gid] {
			t.Errorf("glyph %d (%q) was never used but kept its outline", gid, r)
		}
	}
}

// TestSubsetIsSmaller pins that the exercise pays. A subsetter that produced a
// correct font no smaller than the original would pass every other test here
// and be pointless.
func TestSubsetIsSmaller(t *testing.T) {
	f := loadTestFace(t, alphabet()...)
	f.Encode("a")
	data, err := f.Subset()
	if err != nil {
		t.Fatalf("subsetting: %v", err)
	}
	if len(data) >= len(f.data) {
		t.Errorf("the subset is %d bytes against the original's %d", len(data), len(f.data))
	}
}

// TestSubsetRetainsGlyphIndices pins the design decision the rest depends on.
// The character codes were written before subsetting ran, and with Identity-H
// they are glyph indices; if the subset renumbered, every stream already drawn
// would point at the wrong glyph.
func TestSubsetRetainsGlyphIndices(t *testing.T) {
	f := loadTestFace(t, alphabet()...)
	f.Encode("q")
	wantGID, _ := f.GlyphID('q')

	data, err := f.Subset()
	if err != nil {
		t.Fatalf("subsetting: %v", err)
	}
	sub := font.ParseSFNT(data, 1<<20)
	if sub == nil {
		t.Fatal("the subset did not parse")
	}
	if got := sub.Cmap['q']; got != wantGID {
		t.Errorf("the subset maps %q to glyph %d; the original mapped it to %d", 'q', got, wantGID)
	}
	if len(sub.WidthByGID) <= wantGID || sub.WidthByGID[wantGID] != f.prog.WidthByGID[wantGID] {
		t.Error("the advance at that index changed, so /W would no longer describe the program")
	}
}

// TestSubsetKeepsCompositeComponents pins the transitive closure. An accented
// letter is a composite referring to a base and a mark; keeping the composite
// and dropping either piece leaves a glyph that renders as nothing, and nothing
// in the file says so.
func TestSubsetKeepsCompositeComponents(t *testing.T) {
	// The fixture builder emits simple glyphs, so the composite is planted
	// directly: glyph 3 is rewritten as a composite referring to glyphs 1 and 2.
	base := fonttest.SFNT(fonttest.SFNTOptions{Name: "Comp", Glyphs: []fonttest.Glyph{
		{Rune: 'a', Advance: 500, HasShape: true},
		{Rune: 'b', Advance: 500, HasShape: true},
		{Rune: 'c', Advance: 500, HasShape: true},
	}})
	data := plantComposite(t, base, 3, 1, 2)

	f, err := Load(data)
	if err != nil {
		t.Fatalf("loading the planted font: %v", err)
	}
	if _, missing := f.Encode("c"); missing != 0 { // 'c' is glyph 3, the composite
		t.Fatal("the fixture does not map 'c'")
	}
	out, err := f.Subset()
	if err != nil {
		t.Fatalf("subsetting: %v", err)
	}
	sub := font.ParseSFNT(out, 1<<20)
	if sub == nil {
		t.Fatal("the subset did not parse")
	}
	for _, gid := range []int{1, 2, 3} {
		if !sub.GlyphNonEmpty[gid] {
			t.Errorf("glyph %d was dropped; it is the composite or one of its components", gid)
		}
	}
}

// TestSubsetAlwaysKeepsNotdef pins that glyph 0 survives. It is what a renderer
// draws for anything the font does not cover, and a font without it is one a
// reader may refuse.
//
// It asserts on the kept set rather than on the emitted program, deliberately.
// Because indices are retained, a dropped glyph still has a zero-length entry
// at its own index, so "is glyph 0 present in the tables" is true either way —
// and the fixture's .notdef has no outline to look for. The decision is the
// only thing that is actually observable, so the decision is what is checked.
func TestSubsetAlwaysKeepsNotdef(t *testing.T) {
	f := loadTestFace(t, alphabet()...)
	f.Encode("a")
	_, kept, err := f.subset()
	if err != nil {
		t.Fatalf("subsetting: %v", err)
	}
	if len(kept) == 0 || kept[0] != 0 {
		t.Errorf("kept glyphs are %v; .notdef (0) must be among them", kept)
	}
}

// TestSubsetTagIsAFunctionOfTheGlyphSet pins what the six-letter prefix is for.
// A reader uses it to tell two subsets of one face apart, so it must differ
// when the glyph sets differ and agree when they do not — and it must be
// deterministic, or the same document would produce different files each run.
func TestSubsetTagIsAFunctionOfTheGlyphSet(t *testing.T) {
	abc := subsetTag([]int{0, 1, 2, 3})
	if abc != subsetTag([]int{0, 1, 2, 3}) {
		t.Error("the tag is not deterministic")
	}
	if abc == subsetTag([]int{0, 1, 2, 4}) {
		t.Error("two different glyph sets produced the same tag")
	}
	if len(abc) != 6 {
		t.Fatalf("tag %q is %d letters, want 6", abc, len(abc))
	}
	for _, c := range abc {
		if c < 'A' || c > 'Z' {
			t.Errorf("tag %q contains %q, which is not an uppercase letter", abc, c)
		}
	}
}

// TestSubsetRefusesATruncatedFont pins that a font that cannot be taken apart
// produces an error rather than a program claiming glyphs it does not carry. A
// font file is untrusted input like any other.
func TestSubsetRefusesATruncatedFont(t *testing.T) {
	f := loadTestFace(t, alphabet()...)
	f.Encode("a")
	// Truncate loca so it can no longer describe every glyph.
	tables := font.SFNTTables(f.data)
	locaStart := indexOf(f.data, tables["loca"])
	if locaStart < 0 {
		t.Fatal("could not locate loca in the fixture")
	}
	broken := append([]byte(nil), f.data...)
	f2 := *f
	f2.data = broken[:locaStart+8] // cut mid-loca
	if _, err := f2.Subset(); err == nil {
		t.Error("a truncated font was subsetted rather than refused")
	}
}

// plantComposite rewrites glyph composite as a composite referring to two
// component glyphs, so the closure can be exercised on a font the fixture
// builder does not itself produce.
func plantComposite(t *testing.T, data []byte, composite, c1, c2 int) []byte {
	t.Helper()
	tables := font.SFNTTables(data)
	glyfStart := indexOf(data, tables["glyf"])
	locaStart := indexOf(data, tables["loca"])
	if glyfStart < 0 || locaStart < 0 {
		t.Fatal("could not locate glyf or loca")
	}
	out := append([]byte(nil), data...)

	// A composite glyph: numberOfContours = -1, then two component records
	// with ARG_1_AND_2_ARE_WORDS and, on the first, MORE_COMPONENTS.
	body := []byte{
		0xFF, 0xFF, 0, 0, 0, 0, 0x03, 0xE8, 0x03, 0xE8, // header: -1 contours, bbox
		0x00, 0x21, byte(c1 >> 8), byte(c1), 0, 0, 0, 0, // flags: WORDS|MORE, glyph c1
		0x00, 0x01, byte(c2 >> 8), byte(c2), 0, 0, 0, 0, // flags: WORDS, glyph c2
	}
	// Overwrite the composite's slot; it must fit, so the fixture's square
	// outline (which is longer) is what makes that safe.
	start := be32(out, locaStart+4*composite)
	end := be32(out, locaStart+4*(composite+1))
	if int(end-start) < len(body) {
		t.Fatalf("glyph %d occupies %d bytes, too few for a composite record", composite, end-start)
	}
	copy(out[glyfStart+int(start):], body)
	for i := glyfStart + int(start) + len(body); i < glyfStart+int(end); i++ {
		out[i] = 0
	}
	return out
}

func be32(b []byte, off int) uint32 {
	return uint32(b[off])<<24 | uint32(b[off+1])<<16 | uint32(b[off+2])<<8 | uint32(b[off+3])
}

// indexOf finds where a sub-slice begins inside its backing array.
func indexOf(outer, inner []byte) int {
	if inner == nil {
		return -1
	}
	for i := 0; i+len(inner) <= len(outer); i++ {
		if &outer[i] == &inner[0] {
			return i
		}
	}
	return -1
}
