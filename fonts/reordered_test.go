package fonts

import (
	"fmt"
	"testing"

	"github.com/mgilbir/forme/fonttest"
)

// Which stretches of a run drawn out of its text's order are marked with an
// /ActualText: by default only what a reader putting the line in reading
// order cannot read right (#435), and with SetMarkReordered(true) the
// reordered stretch, and only it (#426).

// bidiFace has the Latin and Hebrew letters the runs below are drawn in.
func bidiFace(t *testing.T) *Face {
	t.Helper()
	var glyphs []fonttest.Glyph
	for _, r := range "abcd שלוםע" {
		glyphs = append(glyphs, fonttest.Glyph{Rune: r, Advance: 500, HasShape: r != ' '})
	}
	return loadTestFace(t, glyphs...)
}

// visualRun is a run as HarfBuzz returns it for text: the glyphs in the order
// they stand on the line, each with the byte offset of its cluster. order
// lists the clusters in that order.
func visualRun(t *testing.T, f *Face, text string, order ...int) []Glyph {
	t.Helper()
	var glyphs []Glyph
	for _, c := range order {
		r := []rune(text[c:])[0]
		g, ok := f.GlyphID(r)
		if !ok {
			t.Fatalf("the face has no glyph for %q", r)
		}
		glyphs = append(glyphs, Glyph{GID: g, Cluster: c})
	}
	return glyphs
}

// mixed is "ab שלום cd" in visual order: the Hebrew word reversed between two
// Latin ones, as in the issue.
const mixed = "ab שלום cd" // a0 b1 ' '2 ש3 ל5 ו7 ם9 ' '11 c12 d13

func mixedRun(t *testing.T, f *Face) []Glyph {
	return visualRun(t, f, mixed, 0, 1, 2, 9, 7, 5, 3, 11, 12, 13)
}

func describe(segs []Segment) string {
	s := ""
	for _, seg := range segs {
		s += fmt.Sprintf("[%d,%d)", seg.Start, seg.End)
		if seg.Marked {
			s += fmt.Sprintf("%q", seg.ActualText)
		}
	}
	return s
}

// TestMarkedOnlyTheReorderedStretchIsMarked is SetMarkReordered(true), as a
// tagged page has it.
func TestMarkedOnlyTheReorderedStretchIsMarked(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		order      []int
		want       string
	}{
		// The Hebrew word alone is marked, and the spaces either side of it
		// are drawn as they read; marking the whole run lost poppler the
		// break before "cd".
		{"mixed", mixed, []int{0, 1, 2, 9, 7, 5, 3, 11, 12, 13}, `[0,3)[3,7)"שלום"[7,10)`},
		// A right-to-left line is reordered from end to end: one stretch.
		{"rtl", "שלום עולם", []int{15, 13, 11, 9, 8, 6, 4, 2, 0}, `[0,9)"שלום עולם"`},
		// Two reordered words with Latin between them: each is its own.
		{"two", "שלום ab עולם", []int{6, 4, 2, 0, 8, 9, 10, 11, 18, 16, 14, 12}, `[0,4)"שלום"[4,8)[8,12)"עולם"`},
		// Text in its own order is not marked at all.
		{"ascending", "ab cd", []int{0, 1, 2, 3, 4}, `[0,5)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := bidiFace(t)
			f.SetMarkReordered(true)
			segs, err := f.Record(tc.text, visualRun(t, f, tc.text, tc.order...))
			if err != nil {
				t.Fatal(err)
			}
			if got := describe(segs); got != tc.want {
				t.Errorf("segments %s; want %s", got, tc.want)
			}
		})
	}
}

// TestByDefaultTheReaderOrdersReorderedText: with no SetMarkReordered, a
// reordered stretch a bidirectional reader reads right is left to it.
func TestByDefaultTheReaderOrdersReorderedText(t *testing.T) {
	t.Run("whole clusters", func(t *testing.T) {
		f := bidiFace(t)
		segs, err := f.Record(mixed, mixedRun(t, f))
		if err != nil {
			t.Fatal(err)
		}
		if got := describe(segs); got != "[0,10)" {
			t.Errorf("segments %s; want the run unmarked, [0,10)", got)
		}
		// The ToUnicode entries are what they are with the marking.
		g, _ := f.GlyphID('ש')
		if got := f.extracted(Glyph{GID: g}); got != "ש" {
			t.Errorf("ש's glyph extracts as %q", got)
		}
	})
	t.Run("a cluster its glyph does not spell", func(t *testing.T) {
		f := bidiFace(t)
		// The glyph of ל was drawn first for other text, which fixed its
		// ToUnicode entry: in the Hebrew word it cannot say ל, and that
		// cluster alone is marked.
		lamed, _ := f.GlyphID('ל')
		if _, err := f.Record("x", []Glyph{{GID: lamed, Cluster: 0}}); err != nil {
			t.Fatal(err)
		}
		segs, err := f.Record(mixed, mixedRun(t, f))
		if err != nil {
			t.Fatal(err)
		}
		if got, want := describe(segs), `[0,5)[5,6)"ל"[6,10)`; got != want {
			t.Errorf("segments %s; want %s", got, want)
		}
	})
	t.Run("a cluster drawn in pieces apart", func(t *testing.T) {
		f := bidiFace(t)
		// The cluster at 3 is drawn twice, apart: no glyph's entry can say
		// what it was, so its stretch is still marked.
		segs, err := f.Record(mixed, visualRun(t, f, mixed, 0, 1, 2, 3, 9, 7, 5, 3, 11, 12, 13))
		if err != nil {
			t.Fatal(err)
		}
		if got, want := describe(segs), `[0,3)[3,8)"שלום"[8,11)`; got != want {
			t.Errorf("segments %s; want %s", got, want)
		}
	})
	t.Run("an order the algorithm cannot see", func(t *testing.T) {
		// Latin drawn backwards, as under a bidi override: a reader that
		// reorders by the algorithm leaves it as drawn, so it is marked.
		f := bidiFace(t)
		segs, err := f.Record("abc", visualRun(t, f, "abc", 2, 1, 0))
		if err != nil {
			t.Fatal(err)
		}
		if got, want := describe(segs), `[0,3)"abc"`; got != want {
			t.Errorf("segments %s; want %s", got, want)
		}
	})
	t.Run("set and unset", func(t *testing.T) {
		f := bidiFace(t)
		f.SetMarkReordered(true)
		if !f.Horizontal().markReordered {
			t.Fatal("the setting is not the horizontal form's")
		}
		f.SetMarkReordered(false)
		f.SetMarkReordered(true)
		segs, err := f.Record(mixed, mixedRun(t, f))
		if err != nil {
			t.Fatal(err)
		}
		if got, want := describe(segs), `[0,3)[3,7)"שלום"[7,10)`; got != want {
			t.Errorf("after SetMarkReordered(true), segments %s; want %s", got, want)
		}
	})
}
