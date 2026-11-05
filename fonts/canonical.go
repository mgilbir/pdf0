package fonts

import (
	"runtime"
	"sync"
	"weak"

	"github.com/mgilbir/forme/shape"
)

// canonicalMap is a font's cmap read backwards: for each glyph the cmap draws
// some character with, the one character the ToUnicode CMap names it by,
// chosen by betterForToUnicode where several map to it. See Face.canonical.
//
// It is a fact about the font, not about a document, so every wrapper of the
// same font shares one (#427). It used to belong to a wrapper's textRecord,
// and a caller that draws each document through a fresh wrapper of a fresh
// clone — Adopt(face.Clone()), as a server does — copied the font's whole cmap
// and built the map again for every document: 153 KB and 240 µs for one label
// in Noto Sans, and a quarter of the CPU of writing an ordinary chart.
//
// It is built once, on first use, and only read afterwards, so wrappers in
// different goroutines can share it.
type canonicalMap struct {
	once  sync.Once
	byGID map[int]rune
}

// canonicalMaps holds the canonicalMap of each font some wrapper is using,
// keyed by forme's FaceID: two faces with one ID draw alike, and so map the
// same characters to the same glyphs.
//
// The maps are held weakly. A cache that kept every font it saw would grow
// for as long as the process loads fonts, and a process rendering documents
// that bring their own fonts loads them without end. A map lives while a
// wrapper whose drawing reached it does, and a cleanup removes its entry once
// none does; the next wrapper of the font builds it again.
var canonicalMaps sync.Map // shape.FaceID -> weak.Pointer[canonicalMap]

// canonicalKey is what a map's cleanup needs to remove its own entry, and no
// other: the weak pointer it was stored under, which a map built after it was
// collected replaces.
type canonicalKey struct {
	id shape.FaceID
	wp weak.Pointer[canonicalMap]
}

func dropCanonicalMap(k canonicalKey) { canonicalMaps.CompareAndDelete(k.id, k.wp) }

// canonicalMapFor is the canonicalMap shared by every face with f's ID,
// unbuilt if no face has asked it anything yet.
func canonicalMapFor(f *shape.Face) *canonicalMap {
	id := f.ID()
	for {
		old, found := canonicalMaps.Load(id)
		if found {
			if m := old.(weak.Pointer[canonicalMap]).Value(); m != nil {
				return m
			}
		}
		// None, or one collected whose cleanup has not yet run: store a new
		// one, unless another goroutine stores its own first.
		m := &canonicalMap{}
		wp := weak.Make(m)
		var stored bool
		if found {
			stored = canonicalMaps.CompareAndSwap(id, old, wp)
		} else {
			_, loaded := canonicalMaps.LoadOrStore(id, wp)
			stored = !loaded
		}
		if stored {
			runtime.AddCleanup(m, dropCanonicalMap, canonicalKey{id: id, wp: wp})
			return m
		}
	}
}

// lookup is the character the font's cmap names gid by, building the map from
// f's cmap the first time any sharer asks.
func (m *canonicalMap) lookup(f *shape.Face, gid int) (rune, bool) {
	m.once.Do(func() {
		cmap := f.Cmap()
		m.byGID = make(map[int]rune, len(cmap))
		for r, g := range cmap {
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
