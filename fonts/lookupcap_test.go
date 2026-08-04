package fonts

import (
	"encoding/binary"
	"testing"
	"time"

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

// TestALookupSeesOnlyTheMarksItsSetNames pins what a mark glyph set is for.
//
// A lookup that names one sees the marks in it and steps over every other. It is
// a finer thing than the mark attachment class in the flags — a class partitions
// the marks, a set is any collection of them — and a font needing two
// overlapping groups can only say so this way. Noto Serif Tibetan states twenty
// sets and uses them to block a ligature in one context and allow it in another.
func TestALookupSeesOnlyTheMarksItsSetNames(t *testing.T) {
	const (
		base   = 1
		inSet  = 7 // a mark the set names
		outSet = 8 // a mark it does not
	)
	l := &layout{
		glyphClass: map[int]int{base: classBase, inSet: classMark, outSet: classMark},
		markSets:   []map[int]bool{{inSet: true}},
	}
	const flags = flagUseMarkFilteringSet
	for _, tc := range []struct {
		gid  int
		want bool
		why  string
	}{
		{inSet, false, "a mark the set names is looked at"},
		{outSet, true, "a mark it does not name is stepped over"},
		{base, false, "a letter is not a mark and the set says nothing about it"},
	} {
		if got := l.ignoresIn(flags, 0, Glyph{GID: tc.gid}); got != tc.want {
			t.Errorf("%s: ignoresIn gave %v, want %v", tc.why, got, tc.want)
		}
	}

	// A set index that names nothing is the dangerous case, and it is what a
	// merged flag word would produce: there is no room in one word to say which
	// of a feature's lookups named which set. Answering "filter by a set nobody
	// named" as "look at every mark" would apply rules meant for a few; this
	// answers it the other way, by looking at none — which is why the bit is
	// dropped when flags are merged rather than carried.
	if !l.ignoresIn(flags, -1, Glyph{GID: inSet}) {
		t.Error("a lookup naming no set looked at a mark anyway")
	}
	if !l.ignoresIn(flags, 9, Glyph{GID: inSet}) {
		t.Error("a lookup naming a set that does not exist looked at a mark anyway")
	}
	if got := l.ignoresIn(mergedFlags(flags), -1, Glyph{GID: inSet}); got {
		t.Error("a merged flag word still filtered by a set, so every mark of the " +
			"feature would be stepped over; mergedFlags must drop the bit")
	}
	if got := l.ignoresIn(mergedFlags(flags|flagIgnoreMarks), -1, Glyph{GID: inSet}); !got {
		t.Error("mergedFlags dropped more than the set bit: IgnoreMarks must survive it")
	}
}

// TestADenseLookupListIsBoundedByTheTable is the other side of lifting the
// caps, and the reason a budget exists at all.
//
// A lookup is a slice that runs to the *end* of the table, because nothing in
// the format says where one stops. So bounding a lookup's subtable count by
// "the bytes available" bounds it by the bytes available in the whole table —
// and every lookup in a large font then appears to have room for tens of
// thousands. A font declaring the maximum in each of the maximum number of
// lookups asks for their product.
//
// That is not hypothetical. The fuzzer found it in twenty-four seconds, in the
// shape of a 533 KB file that took half a minute to read, almost all of it
// spent appending subtable slices and collecting them again.
//
// The bound is a budget shared across the table, so the work is proportional to
// its size rather than to its square. The assertion is deliberately loose: it
// runs on shared machines and is here to catch a change in the *shape* of the
// cost, not to measure it.
func TestADenseLookupListIsBoundedByTheTable(t *testing.T) {
	if testing.Short() {
		t.Skip("timing-sensitive")
	}
	// Every lookup claims the format's maximum number of subtables while
	// occupying six bytes itself. Its offset array is therefore not there — and
	// a lookup is a slice to the end of the table, so the walk reads those
	// "offsets" out of whatever follows, which is exactly what a crafted font
	// does and what makes the count and the table's size multiply.
	const (
		lookups = 400
		padding = 1 << 19 // half a megabyte, about the size the fuzzer found
	)
	body := make([]byte, 2+2*lookups)
	binary.BigEndian.PutUint16(body, uint16(lookups))
	for i := 0; i < lookups; i++ {
		binary.BigEndian.PutUint16(body[2+2*i:], uint16(len(body)))
		lk := make([]byte, 6)
		binary.BigEndian.PutUint16(lk[0:], 1)      // a single substitution
		binary.BigEndian.PutUint16(lk[2:], 0)      // no flags
		binary.BigEndian.PutUint16(lk[4:], 0xFFFF) // ... in 65535 subtables
		body = append(body, lk...)
	}
	// The padding is not zeros. A zero offset is rejected and costs nothing, so
	// a table of zeros would exercise the loop and never the work; these bytes
	// read as small valid offsets, which is what the fuzzer's input had and what
	// makes every one of those declared subtables actually collected.
	pad := make([]byte, padding)
	for i := 1; i < len(pad); i += 2 {
		pad[i] = 0x20
	}
	body = append(body, pad...)
	gsub := make([]byte, 10)
	binary.BigEndian.PutUint16(gsub[0:], 1)
	binary.BigEndian.PutUint16(gsub[8:], 10)
	gsub = append(gsub, body...)

	done := make(chan int, 1)
	go func() {
		f, err := Load(fonttest.SFNT(fonttest.SFNTOptions{
			Name:   "DenseLookups",
			Glyphs: []fonttest.Glyph{{Rune: 'a', Advance: 500, HasShape: true}},
			Extra:  map[string][]byte{"GSUB": gsub},
		}))
		if err != nil {
			// Not a pass: a font that will not load proves nothing about how
			// much work a font that does load can ask for.
			done <- -1
			return
		}
		f.ShapeGlyphs("a")
		total := 0
		for _, lk := range f.layout.gsub {
			total += len(lk.subs)
		}
		done <- total
	}()
	select {
	case total := <-done:
		if total < 0 {
			t.Fatal("the crafted font did not load, so this proves nothing about " +
				"how much work one that does load can ask for")
		}
		// The budget is about half the table's bytes. What matters is that it is
		// nothing like 400 x 65535, which is what the counts asked for.
		if want := lookups * 0xFFFF / 4; total > want {
			t.Errorf("%d subtables were read from a table declaring %d x %d; the work "+
				"must be bounded by the table's size, not by its square",
				total, lookups, 0xFFFF)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("reading a font that declares the maximum subtables in every lookup " +
			"did not finish: the budget is not bounding the work")
	}
}
