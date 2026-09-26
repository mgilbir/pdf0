package core

import (
	"container/heap"
	"sort"
)

// Code ranges, looked up without being expanded.
//
// Three structures a file supplies map ranges of codes to values: a CIDFont's
// /W array (CID ranges to widths), a CMap's cidrange sections (code ranges to
// CIDs) and a ToUnicode CMap's bfrange sections (code ranges to text). Each
// used to be answered by expanding every range into a map, or by scanning the
// list of ranges for every code looked up, and each was a denial of service
// the same way: 2,000 overlapping copies of <0000> <FFFF> in a kilobyte asked
// for 131 million map writes (audit 2026-09-22 C53), 65,000 one-code ranges
// times a million shown codes asked for 6.5e10 comparisons (C52), and 2,000
// /W ranges of 65,536 CIDs each ran out of memory (C10).
//
// A Spans value is those ranges resolved, once, into disjoint segments that
// each name the one range that answers for it — the first defined or the last
// defined, as the structure's semantics say — so that a lookup is a binary
// search and the cost of building it is O(n log n) in the number of ranges,
// whatever they cover.

// Span is a closed range of codes. In a resolved Spans, Idx is the position,
// in the ranges given to ResolveSpans, of the range that answers for it; on
// input it is ignored.
type Span struct {
	Lo, Hi uint32
	Idx    int
}

// Spans is a set of disjoint Spans, sorted by Lo.
type Spans []Span

// ResolveSpans turns ranges, in definition order, into disjoint segments.
// Where ranges overlap, the segment is answered by the last-defined range when
// lastWins, else by the first-defined. Ranges with Hi < Lo are ignored.
//
// It is a sweep over the ranges' endpoints with a heap of the ranges open at
// each point, so it is O(n log n) and its output has at most 2n segments.
func ResolveSpans(ranges []Span, lastWins bool) Spans {
	type event struct {
		at   uint64
		open bool
		idx  int
	}
	events := make([]event, 0, 2*len(ranges))
	for i, r := range ranges {
		if r.Hi < r.Lo {
			continue
		}
		events = append(events, event{uint64(r.Lo), true, i}, event{uint64(r.Hi) + 1, false, i})
	}
	sort.Slice(events, func(i, j int) bool { return events[i].at < events[j].at })
	h := &spanHeap{lastWins: lastWins}
	closed := make([]bool, len(ranges))
	var out Spans
	for i := 0; i < len(events); {
		at := events[i].at
		for ; i < len(events) && events[i].at == at; i++ {
			if events[i].open {
				heap.Push(h, events[i].idx)
			} else {
				closed[events[i].idx] = true
			}
		}
		for h.Len() > 0 && closed[h.items[0]] {
			heap.Pop(h)
		}
		if h.Len() == 0 || i == len(events) {
			continue
		}
		next := events[i].at
		idx := h.items[0]
		lo, hi := uint32(at), uint32(next-1)
		if n := len(out); n > 0 && out[n-1].Idx == idx && uint64(out[n-1].Hi)+1 == at {
			out[n-1].Hi = hi
			continue
		}
		out = append(out, Span{Lo: lo, Hi: hi, Idx: idx})
	}
	return out
}

// Find returns the segment containing code.
func (s Spans) Find(code uint32) (Span, bool) {
	i := sort.Search(len(s), func(i int) bool { return s[i].Hi >= code })
	if i < len(s) && s[i].Lo <= code {
		return s[i], true
	}
	return Span{}, false
}

// spanHeap orders the positions of the open ranges so that the one that
// answers is on top: the highest position when lastWins, else the lowest.
type spanHeap struct {
	items    []int
	lastWins bool
}

func (h *spanHeap) Len() int { return len(h.items) }
func (h *spanHeap) Less(i, j int) bool {
	if h.lastWins {
		return h.items[i] > h.items[j]
	}
	return h.items[i] < h.items[j]
}
func (h *spanHeap) Swap(i, j int) { h.items[i], h.items[j] = h.items[j], h.items[i] }
func (h *spanHeap) Push(x any)    { h.items = append(h.items, x.(int)) }
func (h *spanHeap) Pop() any {
	x := h.items[len(h.items)-1]
	h.items = h.items[:len(h.items)-1]
	return x
}
