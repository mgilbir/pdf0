package fonts

import (
	"encoding/binary"
	"testing"

	"github.com/mgilbir/pdf0/internal/fonttest"
)

// How many lookups a font may declare.
//
// A lookup is named by its *index*: a contextual rule says "apply lookup number
// 1139 at position two". So a reader that keeps only the first few hundred does
// not lose the tail of a list — it silently breaks every reference into it, and
// the rule does nothing at all.
//
// The cap was 512, described in the source as far above any real face. Noto
// Serif Tibetan declares 1190, and a third of that font's Tibetan was set
// wrongly with nothing reporting it. The bound is now the format's own: the
// count is a uint16, so no valid font can exceed it and none is ever truncated.
//
// What stops a crafted font is not that number. It is that reading a lookup
// needs two bytes of offset present in the table, so the work a font can ask
// for is bounded by its own size — which is the bound that means something.

// TestALongLookupListIsReadWhole builds a table declaring more lookups than the
// old cap and checks that the last one is still reachable.
func TestALongLookupListIsReadWhole(t *testing.T) {
	const count = 900 // more than the 512 that used to be kept

	// A lookup list of `count` single substitutions. Only the last does
	// anything: it turns glyph 1 into glyph 2. The feature names that one, so
	// nothing but a whole list makes it reachable.
	lookups := make([]fonttest.Lookup, count)
	for i := range lookups {
		from, to := 3, 4
		if i == count-1 {
			from, to = 1, 2
		}
		lookups[i] = fonttest.Lookup{
			Type: 1, Subtables: [][]byte{fonttest.SingleSubst([]int{from}, []int{to})},
		}
	}
	f := longLookupFace(t, fonttest.GSUBLookups(lookups, map[string][]int{"calt": {count - 1}}))
	l := f.layout
	if len(l.gsub) != count {
		t.Errorf("%d lookups were read and the font declares %d; a lookup is named "+
			"by index, so dropping any breaks every reference past it", len(l.gsub), count)
	}
	got, _ := f.ShapeGlyphs("a")
	if len(got) != 1 || got[0].GID != 2 {
		t.Errorf("shaping used lookup %d and got glyph %v, want 2 — the last lookup "+
			"in a long list must still apply", count-1, gidsOfGlyphs(got))
	}
}

// TestADeclaredLookupCountCostsNothingWithoutTheData is the other half: the
// bound that protects against a crafted font is the table's own size, not the
// cap, and this is what says so.
func TestADeclaredLookupCountCostsNothingWithoutTheData(t *testing.T) {
	// A lookup list claiming the format's maximum, with no offsets behind it.
	list := make([]byte, 2)
	binary.BigEndian.PutUint16(list, 0xFFFF)
	gsub := make([]byte, 10)
	binary.BigEndian.PutUint16(gsub[0:], 1)  // major version
	binary.BigEndian.PutUint16(gsub[2:], 0)  // minor
	binary.BigEndian.PutUint16(gsub[4:], 0)  // no ScriptList
	binary.BigEndian.PutUint16(gsub[6:], 0)  // no FeatureList
	binary.BigEndian.PutUint16(gsub[8:], 10) // LookupList at 10
	gsub = append(gsub, list...)

	f := longLookupFace(t, gsub)
	// Sixty-five thousand were claimed and there is room for none: what is read
	// is what the bytes could hold.
	if got := len(f.layout.gsub); got > 1 {
		t.Errorf("a lookup list claiming 65535 lookups in two bytes produced %d of "+
			"them; the walk must stop when the offsets run out", got)
	}
}

// longLookupFace builds a face carrying the given GSUB table over two glyphs.
func longLookupFace(t *testing.T, gsub []byte) *Face {
	t.Helper()
	f, err := Load(fonttest.SFNT(fonttest.SFNTOptions{
		Name: "LongLookupList",
		Glyphs: []fonttest.Glyph{
			{Rune: 'a', Advance: 500, HasShape: true},
			{Rune: 'b', Advance: 500, HasShape: true},
			{Rune: 'c', Advance: 500, HasShape: true},
			{Rune: 'd', Advance: 500, HasShape: true},
		},
		Extra: map[string][]byte{"GSUB": gsub},
	}))
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	return f
}

func gidsOfGlyphs(gs []Glyph) []int {
	out := make([]int, len(gs))
	for i, g := range gs {
		out[i] = g.GID
	}
	return out
}
