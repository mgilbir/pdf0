package fonts

import (
	"testing"
	"time"

	"github.com/mgilbir/pdf0/internal/fonttest"
)

// What a ligature does to the glyphs it stepped over.
//
// A lookup that ignores marks matches "a" and "b" across the accent between
// them — that is what the flag is for, and it is right. What is not right is
// what happens to the accent afterwards: it was never part of the rule, the
// font never said to remove it, and the reader is still owed it. OpenType is
// explicit that a skipped glyph survives the substitution, and HarfBuzz places
// it after the ligature glyph.
//
// A shaper that replaces the whole span from the first component to the last
// silently eats it. The text loses an accent — in Devanagari or Khmer it loses
// a vowel — and nothing reports an error, because from the shaper's point of
// view a rule matched and applied.

// ligatureFace builds a face whose 'calt' feature is one ligature lookup with
// the given flags. The glyph repertoire is contextFace's, so gidA..gidMark mean
// the same here.
func ligatureFace(t *testing.T, flags int, ligs []fonttest.Ligature, gdef map[int]int) *Face {
	t.Helper()
	return contextFace(t, []fonttest.Lookup{{
		Type: 4, Flag: flags, Subtables: [][]byte{fonttest.LigatureSubst(ligs)},
	}}, gdef)
}

// markGDEF classifies the fixture's accent as a mark and its letters as bases,
// which is what makes IgnoreMarks mean anything.
func markGDEF() map[int]int {
	return map[int]int{
		gidA: classBase, gidB: classBase, gidC: classBase, gidD: classBase,
		gidMark: classMark,
	}
}

// TestLigatureKeepsTheMarkItSteppedOver is the defect stated as a test.
func TestLigatureKeepsTheMarkItSteppedOver(t *testing.T) {
	f := ligatureFace(t, flagIgnoreMarks,
		[]fonttest.Ligature{{Components: []int{gidA, gidB}, Glyph: gidBalt}},
		markGDEF())

	got := shapedGIDs(t, f, "áb")
	want := []int{gidBalt, gidMark}
	if !sameGIDs(got, want) {
		t.Errorf("a + acute + b shaped to %v, want %v\n"+
			"The ligature covers a and b; the accent between them was skipped by the "+
			"lookup flag, not consumed by the rule, and must survive it.", got, want)
	}
}

// TestLigatureKeepsSeveralSkippedMarks pins that it is every skipped glyph, in
// order, and not just the first — a syllable can carry several.
func TestLigatureKeepsSeveralSkippedMarks(t *testing.T) {
	f := ligatureFace(t, flagIgnoreMarks,
		[]fonttest.Ligature{{Components: []int{gidA, gidB, gidC}, Glyph: gidBalt}},
		markGDEF())

	got := shapedGIDs(t, f, "áb́c")
	want := []int{gidBalt, gidMark, gidMark}
	if !sameGIDs(got, want) {
		t.Errorf("a + acute + b + acute + c shaped to %v, want %v", got, want)
	}
}

// TestLigatureWithNothingSkippedIsUnchanged is the control. The interesting fix
// is easy to write in a way that also changes the ordinary case, which is by far
// the common one — every Latin "fi" goes through it.
func TestLigatureWithNothingSkippedIsUnchanged(t *testing.T) {
	f := ligatureFace(t, flagIgnoreMarks,
		[]fonttest.Ligature{{Components: []int{gidA, gidB}, Glyph: gidBalt}},
		markGDEF())

	if got, want := shapedGIDs(t, f, "ab"), []int{gidBalt}; !sameGIDs(got, want) {
		t.Errorf("ab shaped to %v, want %v", got, want)
	}
	if got, want := shapedGIDs(t, f, "abc"), []int{gidBalt, gidC}; !sameGIDs(got, want) {
		t.Errorf("abc shaped to %v, want %v", got, want)
	}
	// A run the rule does not match must come through untouched.
	if got, want := shapedGIDs(t, f, "ba"), []int{gidB, gidA}; !sameGIDs(got, want) {
		t.Errorf("ba shaped to %v, want %v", got, want)
	}
}

// TestLigatureKeepsTheSkippedMarkInTheCluster pins the mapping back to the text.
//
// A cluster says which characters a glyph stands for, and it is what a caller
// uses to put a caret between two letters or to select a word. The ligature and
// the mark it stepped over came from one stretch of text that can no longer be
// divided — the accent belongs to a letter that is now half a ligature — so they
// have to report the same cluster, which is where that stretch began.
func TestLigatureKeepsTheSkippedMarkInTheCluster(t *testing.T) {
	f := ligatureFace(t, flagIgnoreMarks,
		[]fonttest.Ligature{{Components: []int{gidA, gidB}, Glyph: gidBalt}},
		markGDEF())

	glyphs, missing := f.ShapeGlyphs("áb")
	if missing != 0 {
		t.Fatalf("%d runes have no glyph", missing)
	}
	if len(glyphs) != 2 {
		t.Fatalf("shaped to %d glyphs, want 2", len(glyphs))
	}
	if glyphs[0].Cluster != glyphs[1].Cluster {
		t.Errorf("the ligature reports cluster %d and the mark it kept reports %d; "+
			"they came from one indivisible stretch of text",
			glyphs[0].Cluster, glyphs[1].Cluster)
	}
	if glyphs[0].Cluster != 0 {
		t.Errorf("the cluster is %d; the stretch begins at the start of the run", glyphs[0].Cluster)
	}
}

// TestLigatureOverAMarkStillAdvancesTheWalk guards against the fix turning the
// substitution loop into an infinite one.
//
// The loop advances by what a lookup reports it consumed. A ligature that keeps
// a glyph produces a longer result than one that does not, and a report that
// does not account for it can leave the walk where it started — looking at a
// glyph the rule still matches. The test is that this returns at all.
func TestLigatureOverAMarkStillAdvancesTheWalk(t *testing.T) {
	f := ligatureFace(t, flagIgnoreMarks,
		[]fonttest.Ligature{
			{Components: []int{gidA, gidB}, Glyph: gidA}, // the product matches again
		},
		markGDEF())

	done := make(chan []int, 1)
	go func() { done <- shapedGIDs(t, f, "áb́áb") }()
	select {
	case got := <-done:
		if len(got) == 0 {
			t.Error("shaped to nothing")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("shaping did not finish: a ligature that keeps a skipped glyph left the walk in place")
	}
}

func sameGIDs(a, b []int) bool {
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
