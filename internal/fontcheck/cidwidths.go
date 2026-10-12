package fontcheck

import (
	"math"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// This file reads a CIDFont's /W array (ISO 32000-1 9.7.4.3) without
// expanding it.

// CIDWidths is a CIDFont's /W array, answered per CID without being expanded
// (audit 2026-09-22 C10).
//
// /W is a list of entries — "c [w0 w1 …]", widths for c, c+1, …, and
// "c_first c_last w", one width for the range — and a later entry overrides an
// earlier one for the CIDs they share. Expanding it into a map is what a
// hostile file asks for: 2,000 entries "k·65536 k·65536+65535 500" are 38 KB
// asking for 131 million inserts, and a sub-array referenced 2,000 times is
// expanded 2,000 times. A per-range span limit bounded one entry and not the
// sum. Here the entries are resolved once into disjoint CID segments
// (core.ResolveSpans, the last entry winning), so the cost is O(n log n) in
// the number of entries whatever they cover, and a lookup is a binary search.
// A sub-array's elements are read only for the CIDs actually shown.
type CIDWidths struct {
	doc     core.View
	entries []cidWidthEntry
	spans   core.Spans
}

// cidWidthEntry is one /W entry: first is its first CID, and either arr (the
// sub-array form) or w (the range form) gives the widths.
type cidWidthEntry struct {
	first int64
	w     float64
	arr   object.Array
}

// Width is the width /W declares for cid, if it declares one. A nil CIDWidths
// declares none.
func (c *CIDWidths) Width(cid int) (float64, bool) {
	if c == nil || cid < 0 || int64(cid) > math.MaxUint32 {
		return 0, false
	}
	c.doc.Charge(1)
	sp, ok := c.spans.Find(uint32(cid))
	if !ok {
		return 0, false
	}
	e := c.entries[sp.Idx]
	if e.arr != nil {
		return numVal(c.doc, e.arr[int64(cid)-e.first]), true
	}
	return e.w, true
}

type cidWidthsSlot struct{}

// ParseCIDWidths reads a CIDFont /W array. The second result reports whether
// the widths could be read at all: a /W that is present and not an array is
// malformed, and the caller's fallback for a CID with no declared width —
// /DW, default 1000 — would then be compared against numbers the document did
// not state, which is how a missing width becomes a false "width information
// ... is inconsistent". An absent /W is complete: every CID takes /DW.
//
// A /W named by reference is read once per run, however many fonts share it.
// The entries are charged to the run's work meter.
func ParseCIDWidths(doc core.View, wObj object.Object) (*CIDWidths, bool) {
	ref, isRef := wObj.(object.IndirectRef)
	type memo struct {
		w  *CIDWidths
		ok bool
	}
	var cache *map[int]memo
	if isRef {
		cache = core.Slot[map[int]memo](doc.Run, cidWidthsSlot{})
		if m, ok := (*cache)[ref.Number]; ok {
			doc.Charge(1)
			return m.w, m.ok
		}
	}
	w, ok := readCIDWidths(doc, wObj)
	if isRef {
		if *cache == nil {
			*cache = map[int]memo{}
		}
		(*cache)[ref.Number] = memo{w, ok}
	}
	return w, ok
}

func readCIDWidths(doc core.View, wObj object.Object) (*CIDWidths, bool) {
	out := &CIDWidths{doc: doc}
	resolved := doc.Resolve(wObj)
	if resolved == nil {
		// No /W at all, which is legal and complete: §9.7.4.3 makes the array
		// optional and every CID then takes /DW. It is also common — a subset
		// whose glyphs are all the default width has nothing to state, and a
		// CJK face is monospaced, so the array is empty for exactly the fonts
		// that would otherwise have the largest one.
		//
		// Reporting it as incomplete said "a /W entry spans more than 65536
		// CIDs" about a font with no entries, and skipped the width-consistency
		// check that could have run perfectly well against /DW.
		return out, true
	}
	arr, ok := resolved.(object.Array)
	if !ok {
		// Present and not an array: malformed, and the widths cannot be read.
		// That is incomplete in the sense the caller means — a check run
		// against /DW here would be checking against numbers the document did
		// not state.
		return out, false
	}
	doc.Charge(len(arr))
	var ranges []core.Span
	add := func(e cidWidthEntry, lo, hi int64) {
		// CIDs below 0 are never looked up, and none is above 2^32-1.
		lo = max(lo, 0)
		hi = min(hi, math.MaxUint32)
		if hi < lo {
			return
		}
		out.entries = append(out.entries, e)
		ranges = append(ranges, core.Span{Lo: uint32(lo), Hi: uint32(hi)})
	}
	i := 0
	for i < len(arr) {
		c := int64(intVal(doc, arr[i]))
		if i+1 < len(arr) {
			if sub, ok := doc.Resolve(arr[i+1]).(object.Array); ok {
				if len(sub) > 0 {
					add(cidWidthEntry{first: c, arr: sub}, c, c+int64(len(sub))-1)
				}
				i += 2
				continue
			}
			if i+2 < len(arr) {
				cLast := int64(intVal(doc, arr[i+1]))
				w := numVal(doc, arr[i+2])
				// An inverted or negative range is malformed and declares
				// nothing, so nothing is missing.
				if c >= 0 && cLast >= c {
					add(cidWidthEntry{first: c, w: w}, c, cLast)
				}
				i += 3
				continue
			}
		}
		i++
	}
	out.spans = core.ResolveSpans(ranges, true)
	return out, true
}
