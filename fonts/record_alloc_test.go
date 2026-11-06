package fonts

import (
	"reflect"
	"testing"
)

// TestRecordAllocatesOnlyItsResult: recording a run again on a face that has
// already seen its glyphs allocates the []Segment it returns and nothing
// else, however long the run (#434). plan used to build and drop a dozen
// slices, closures and builders per call: 36 allocations for a 22-glyph label.
func TestRecordAllocatesOnlyItsResult(t *testing.T) {
	s := notoShapingFace(t)
	for _, text := range []string{"0", "label", "Measurement 0123456789"} {
		f := Adopt(s.Clone())
		glyphs := labelGlyphs(t, f, text)
		recordLabel(t, f, text)
		if n := testing.AllocsPerRun(200, func() {
			if _, err := f.Record(text, glyphs); err != nil {
				t.Fatal(err)
			}
		}); n > 1 {
			t.Errorf("recording %q again allocates %v times; want 1, the result", text, n)
		}
	}
}

// TestPlanFastPathIsTheGeneralPath plans every string of the shaping corpus,
// as Noto Sans shapes it, once with the fast path for text in its own order
// and once without, on fresh faces, and holds the two to the same stretches
// and the same ToUnicode entries.
func TestPlanFastPathIsTheGeneralPath(t *testing.T) {
	corpus := shapingCorpus(t)
	fast, general := mustNotoSans(t), mustNotoSans(t)
	ascending := 0
	for _, text := range corpus {
		glyphs, _ := fast.ShapeGlyphs(text)
		if isAscending(glyphs) {
			ascending++
		}
		got := append([]segment(nil), fast.plan(glyphs, text)...)
		ascendingFastPath = false
		want := append([]segment(nil), general.plan(glyphs, text)...)
		ascendingFastPath = true
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%q: the fast path plans %+v; the general path %+v", text, got, want)
		}
	}
	if !reflect.DeepEqual(fast.rec.byGID, general.rec.byGID) {
		t.Fatal("the two paths recorded different ToUnicode entries")
	}
	if ascending < len(corpus)/2 {
		t.Fatalf("only %d of %d corpus strings shape in their own order; the fast path is barely tested", ascending, len(corpus))
	}
}

func isAscending(glyphs []Glyph) bool {
	for i := 1; i < len(glyphs); i++ {
		if glyphs[i].Cluster < glyphs[i-1].Cluster {
			return false
		}
	}
	return true
}

func mustNotoSans(t *testing.T) *Face {
	t.Helper()
	f, err := NotoSans()
	if err != nil {
		t.Fatal(err)
	}
	return f
}
