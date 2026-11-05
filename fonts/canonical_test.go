package fonts

import (
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/mgilbir/forme/fonts/notosans"
	"github.com/mgilbir/forme/fonttest"
	"github.com/mgilbir/forme/shape"
)

// The reverse cmap a glyph's ToUnicode entry falls back on is the font's, not
// a document's: built once, shared by every wrapper of the font, and let go
// when none uses it (#427).

func notoShapingFace(t testing.TB) *shape.Face {
	t.Helper()
	s, err := notosans.Face()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// labelGlyphs is text set one glyph a character, each its own cluster: every
// glyph of it is named through canonical.
func labelGlyphs(t testing.TB, f *Face, text string) []Glyph {
	t.Helper()
	var glyphs []Glyph
	for i, r := range text {
		g, ok := f.GlyphID(r)
		if !ok {
			t.Fatalf("no glyph for %q", r)
		}
		glyphs = append(glyphs, Glyph{GID: g, Cluster: i, XAdvance: f.NominalAdvance(Glyph{GID: g})})
	}
	return glyphs
}

func recordLabel(t testing.TB, f *Face, text string) {
	t.Helper()
	if _, err := f.Record(text, labelGlyphs(t, f, text)); err != nil {
		t.Fatal(err)
	}
}

// TestTheReverseCmapIsTheFonts holds the wrappers of two clones of one font,
// as two documents have them, to one reverse map; a different font to its
// own; and the shared map to what each wrapper used to build for itself.
func TestTheReverseCmapIsTheFonts(t *testing.T) {
	s := notoShapingFace(t)
	a, b := Adopt(s.Clone()), Adopt(s.Clone())
	const text = "Measurement 0123456789"
	recordLabel(t, a, text)
	recordLabel(t, b, text)
	if a.rec.canon == nil || a.rec.canon != b.rec.canon {
		t.Fatalf("two wrappers of one font hold reverse maps %p and %p; want one, shared", a.rec.canon, b.rec.canon)
	}

	other := loadTestFace(t, alphabet()...)
	recordLabel(t, other, "abc")
	if other.rec.canon == a.rec.canon {
		t.Fatal("a different font shares Noto Sans's reverse map")
	}

	// What a wrapper built for itself before the map was shared.
	want := map[int]rune{}
	for r, g := range s.Cmap() {
		if g == 0 || forbiddenInToUnicode(r) {
			continue
		}
		if prev, ok := want[g]; !ok || betterForToUnicode(r, prev) {
			want[g] = r
		}
	}
	for g := range s.NumGlyphs() {
		got, ok := b.canonical(g)
		w, wok := want[g]
		if got != w || ok != wok {
			t.Fatalf("glyph %d is named %q (%v); the font's cmap names it %q (%v)", g, got, ok, w, wok)
		}
	}
}

// TestTheReverseCmapIsLetGo holds the shared maps to the wrappers using them:
// once none is reachable, the font's entry goes, so a process that loads
// fonts without end does not keep a map for each.
func TestTheReverseCmapIsLetGo(t *testing.T) {
	// A font of this test's own, which no other test's wrapper can be holding.
	glyphs := alphabet()
	glyphs = append(glyphs, fonttest.Glyph{Rune: 'Ω', Advance: 777, HasShape: true})
	id := func() shape.FaceID {
		f := loadTestFace(t, glyphs...)
		recordLabel(t, f, "letgoΩ")
		if _, ok := canonicalMaps.Load(f.ID()); !ok {
			t.Fatal("recording a run made no reverse map for the font")
		}
		return f.ID()
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		runtime.GC()
		if _, ok := canonicalMaps.Load(id); !ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the reverse map of a font no wrapper uses is still held")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestTheReverseCmapIsSharedAcrossGoroutines records into wrappers of one
// font from many goroutines at once, as a server writing documents in
// parallel does, and holds each to the same ToUnicode entries. Run with -race
// it is what says the shared map is only read once built.
func TestTheReverseCmapIsSharedAcrossGoroutines(t *testing.T) {
	s := notoShapingFace(t)
	const text = "Concurrent 0123456789 ÆØÅ"
	var wg sync.WaitGroup
	results := make([]map[int]string, 16)
	for i := range results {
		wg.Go(func() {
			f := Adopt(s.Clone())
			recordLabel(t, f, text)
			results[i] = f.rec.byGID
		})
	}
	wg.Wait()
	for i, r := range results[1:] {
		if len(r) != len(results[0]) {
			t.Fatalf("goroutine %d recorded %d glyphs, goroutine 0 %d", i+1, len(r), len(results[0]))
		}
		for g, s := range results[0] {
			if r[g] != s {
				t.Fatalf("glyph %d means %q in goroutine %d and %q in goroutine 0", g, r[g], i+1, s)
			}
		}
	}
}

// BenchmarkRecordPerDocument is a label recorded through a fresh wrapper of a
// fresh clone, as a caller writing one document after another records it,
// with the font's reverse map kept alive by a wrapper still in use. Before the
// map was shared it cost 243 µs, 153 KB and 86 allocations a document; with
// it, 32 µs, 5 KB and 67.
func BenchmarkRecordPerDocument(b *testing.B) {
	s := notoShapingFace(b)
	const text = "Measurement 0123456789"
	keep := Adopt(s.Clone())
	recordLabel(b, keep, text)
	glyphs := labelGlyphs(b, keep, text)
	b.ReportAllocs()
	for b.Loop() {
		f := Adopt(s.Clone())
		if _, err := f.Record(text, glyphs); err != nil {
			b.Fatal(err)
		}
	}
	runtime.KeepAlive(keep)
}
