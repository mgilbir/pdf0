package fonts

import (
	"runtime"
	"sync"
	"weak"

	"github.com/mgilbir/forme/shape"
)

// fontFacts is what this package works out from a font program that does not
// depend on any document: worked out once, on first use, and shared by every
// wrapper of the font, through every clone of it, for as long as one of them
// holds it.
//
// It exists because a caller that writes each document through a fresh
// wrapper of a fresh clone — Adopt(face.Clone()), as a server does — used to
// redo this work for every document:
//
//   - the font's cmap read backwards, which the ToUnicode CMap falls back on
//     (#427): 153 KB and 240 µs a document for one label in Noto Sans;
//   - the advance most of the font's glyphs share, which /DW states, and the
//     length of its width table (#439): a copy of every glyph's advance,
//     counted in a map, on every embedding — 39 KB of the 274 KB embedding a
//     label in Liberation Sans allocated.
//
// Each part is computed under its own sync.Once and only read afterwards, so
// wrappers in different goroutines can share one.
type fontFacts struct {
	canonOnce sync.Once
	// byGID is the character the font's cmap names each glyph by. See
	// Face.canonical.
	byGID map[int]rune

	widthsOnce sync.Once
	widths     widthTable
}

// widthTable is the font program's advances, read glyph by glyph from forme
// rather than copied: n is how many the program has (forme's GlyphAdvances is
// that long), and common the one most of them share, exactly — /DW states it
// rounded.
type widthTable struct {
	face   *shape.Face
	n      int
	common float64
}

// advance is the program's advance for gid, and whether the program has one.
func (w widthTable) advance(gid int) (float64, bool) {
	if gid < 0 || gid >= w.n {
		return 0, false
	}
	return w.face.GlyphAdvance(gid), true
}

// allFacts holds the fontFacts of each font some wrapper is using, keyed by
// forme's FaceID: two faces with one ID draw alike, so they map the same
// characters to the same glyphs and give them the same advances.
//
// They are held weakly. A cache that kept every font it saw would grow for as
// long as the process loads fonts, and a process rendering documents that
// bring their own fonts loads them without end. A fontFacts lives while a
// wrapper that asked it something does, and a cleanup removes its entry once
// none does; the next wrapper of the font works the facts out again.
var allFacts sync.Map // shape.FaceID -> weak.Pointer[fontFacts]

// factsKey is what a fontFacts' cleanup needs to remove its own entry, and no
// other: the weak pointer it was stored under, which one made after it was
// collected replaces.
type factsKey struct {
	id shape.FaceID
	wp weak.Pointer[fontFacts]
}

func dropFacts(k factsKey) { allFacts.CompareAndDelete(k.id, k.wp) }

// factsFor is the fontFacts shared by every face with f's ID, with nothing
// worked out yet if no face has asked it anything.
func factsFor(f *shape.Face) *fontFacts {
	id := f.ID()
	for {
		old, found := allFacts.Load(id)
		if found {
			if m := old.(weak.Pointer[fontFacts]).Value(); m != nil {
				return m
			}
		}
		// None, or one collected whose cleanup has not yet run: store a new
		// one, unless another goroutine stores its own first.
		m := &fontFacts{}
		wp := weak.Make(m)
		var stored bool
		if found {
			stored = allFacts.CompareAndSwap(id, old, wp)
		} else {
			_, loaded := allFacts.LoadOrStore(id, wp)
			stored = !loaded
		}
		if stored {
			runtime.AddCleanup(m, dropFacts, factsKey{id: id, wp: wp})
			return m
		}
	}
}

// canonical is the character the font's cmap names gid by, building the
// reverse map from f's cmap the first time any sharer asks.
func (m *fontFacts) canonical(f *shape.Face, gid int) (rune, bool) {
	m.canonOnce.Do(func() {
		// Keyed by glyph, so it holds at most one entry a glyph.
		m.byGID = make(map[int]rune, f.NumGlyphs())
		for r, g := range f.CmapAll() {
			if g == 0 || forbiddenInToUnicode(r) {
				// U+0000, U+FEFF and U+FFFE are not text: mapping a glyph to
				// one says the character it represents is a byte-order mark or
				// nothing at all, and PDF/A reports it.
				continue
			}
			// betterForToUnicode is a strict order, so the choice does not
			// depend on the order the map is ranged in, and every sharer
			// would have built the same map.
			if prev, ok := m.byGID[g]; !ok || betterForToUnicode(r, prev) {
				m.byGID[g] = r
			}
		}
	})
	r, ok := m.byGID[gid]
	return r, ok
}

// widthsOf is the font's width table, working out its length and its most
// common advance the first time any sharer asks. That one time is the only
// copy of the font's advances this package makes.
func (m *fontFacts) widthsOf(f *shape.Face) widthTable {
	m.widthsOnce.Do(func() {
		advances := f.GlyphAdvances()
		// No face is kept here: the facts outlive any one document, and a
		// face is a document's (its record of use). The caller's is set on
		// the copy returned, and it reads the same program.
		m.widths = widthTable{n: len(advances), common: mostCommonWidth(advances)}
	})
	t := m.widths
	t.face = f
	return t
}
