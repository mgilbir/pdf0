package fonts

import "github.com/mgilbir/pdf0/internal/font"

// Contextual positioning: GPOS lookup types 7 and 8.
//
// These are to positioning what types 5 and 6 are to substitution, and they have
// exactly the same subtable formats. A rule says "where this sequence occurs,
// apply positioning lookup number six at position two", and lookup six is
// another GPOS lookup with its own type. So the same matching serves both, and
// the difference is only what a matched rule then does.
//
// # Why they are read separately from everything else here
//
// The rest of GPOS is read into flat tables at load — a kern pair is a pair, a
// mark's anchor is an anchor — and applied in passes. That cannot work for these:
// the lookup a rule names has to still exist as something applicable, at a
// position, on demand. So the positioning lookups are also kept whole, and these
// rules reach them by index.
//
// # A lookup named twice is applied twice, and that is correct
//
// A lookup both named by a feature and reached from a rule is applied by each,
// and this was written down as a limitation before it was measured. It is not
// one: HarfBuzz does the same, on a font built to ask it — a nudge of 100 named
// both ways moves the glyph by 200 in both implementations. The format says a
// feature's lookups run over the text and a rule's run where it matches, and
// nothing says the two may not be the same lookup. Noto Serif Tibetan has one.
//
// It is harmless for the attachments because those *set* an offset rather than
// adding to one, so placing the same mark twice puts it in the same place.
//
// What is left unmodelled is the order between the flat positioning passes and
// these: the passes run first rather than interleaving by lookup index. No font
// examined settles it — removing every contextual positioning subtable from all
// six leaves all 11,439 corpus answers unchanged — so it stays as it is rather
// than being changed on a guess.

// readContextualPositioning collects the type 7 and 8 lookups a selected feature
// names, and — only if there are any — the whole positioning lookup list they
// address.
//
// The list is not read otherwise. It is a second parse of a table that is
// already in flat form, and no font without a contextual rule has any use for
// it.
func (l *layout) readContextualPositioning(gpos []byte, feats tableFeatures) {
	byTag := featureLookupIndices(gpos, feats)
	if len(byTag) == 0 {
		return
	}
	all := gposLookups(gpos)
	seen := map[int]bool{}
	for _, tag := range featureTags(gpos, feats.sel) {
		for _, idx := range byTag[tag] {
			if idx < 0 || idx >= len(all) || seen[idx] {
				continue
			}
			if k := all[idx].kind; k == 7 || k == 8 {
				seen[idx] = true
				l.contextualPos = append(l.contextualPos, idx)
			}
		}
	}
	if len(l.contextualPos) > 0 {
		l.gpos = all
	}
}

// applyContextualPositioning runs the contextual positioning rules over a
// buffer, left to right.
//
// Unlike substitution, positioning never changes the buffer's length, so the
// walk is a plain scan: a rule that applies moves the position past what it
// matched, and one that does not moves on by one.
func (sh shaper) applyContextualPositioning(buf []Glyph) {
	for _, idx := range sh.l.contextualPos {
		for i := 0; i < len(buf); {
			n := sh.applyGPOSAt(idx, buf, i, 0)
			if n > 0 {
				i += n
				continue
			}
			i++
		}
	}
}

// applyGPOSAt applies one positioning lookup at a position, reporting how many
// glyphs it matched — zero when it did not apply.
func (sh shaper) applyGPOSAt(idx int, buf []Glyph, at, depth int) int {
	if depth > maxLookupRecursion || idx < 0 || idx >= len(sh.l.gpos) || at >= len(buf) {
		return 0
	}
	lk := sh.l.gpos[idx]
	sh.markSet = lk.markSet
	if sh.ignores(lk.flags, buf[at]) {
		return 0
	}
	for _, sub := range lk.subs {
		switch lk.kind {
		case 1:
			if n := sh.singlePosAt(sub, buf, at); n > 0 {
				return n
			}
		case 2:
			if n := sh.pairPosAt(sub, buf, at, lk.flags); n > 0 {
				return n
			}
		case 4, 6:
			if n := sh.markAttachAt(sub, buf, at, lk.flags, lk.kind == 6); n > 0 {
				return n
			}
		case 7:
			if n, ok := sh.positioningContext(sub, buf, at, lk.flags, depth); ok {
				return n
			}
		case 8:
			if n, ok := sh.chainedPositioningContext(sub, buf, at, lk.flags, depth); ok {
				return n
			}
		}
		// Type 3, cursive attachment, is absent: it joins a *run* of letters
		// onto one another, so applying it at a single position says nothing.
		// The flat pass in position.go is where it belongs.
	}
	return 0
}

// markAttachAt applies a mark-to-base or mark-to-mark subtable at a position.
//
// A lookup reached from a rule is usually named by no feature, so its anchors
// are in none of the flat tables and this is the only way to them. The subtable
// is read on the spot rather than at load: a font states a handful of these and
// reaches them rarely, and reading every mark subtable of every lookup to serve
// the few that a rule names would be most of the cost of loading a font.
//
// Applying it twice is harmless — placeMark sets the offsets rather than adding
// to them — which is what lets this coexist with the flat pass for a lookup that
// is both named by a feature and reached from a rule. Noto Serif Tibetan has one.
func (sh shaper) markAttachAt(sub []byte, buf []Glyph, at, flags int, mkmk bool) int {
	st, ok := readMarkSubtable(sub, 0, false)
	if !ok {
		return 0
	}
	mark, covered := st.marks[buf[at].GID]
	if !covered {
		return 0
	}
	// Back to what this mark attaches to: for mark-to-mark the nearest mark,
	// for mark-to-base the nearest thing that is not one.
	for j := at - 1; j >= 0; j-- {
		if sh.l.isMark(buf[j]) != mkmk {
			if mkmk {
				continue
			}
			break
		}
		base, has := st.bases[key2{buf[j].GID, mark.class}]
		if !has {
			if mkmk {
				continue
			}
			break
		}
		sh.placeMark(buf, at, j, mark.anchor, base)
		return 1
	}
	return 0
}

// positioningContext and chainedPositioningContext match a type 7 or type 8
// subtable and apply what it names.
//
// The matching is sequenceContext's and chainedContext's, unchanged: the two
// tables state a context the same way, down to the byte. Only the flag saying
// which lookup list a matched rule reaches into is different.
func (sh shaper) positioningContext(sub []byte, buf []Glyph, at, flags, depth int) (int, bool) {
	sh.positioning = true
	n, _, ok := sh.sequenceContext(sub, buf, at, flags, depth)
	return n, ok
}

func (sh shaper) chainedPositioningContext(sub []byte, buf []Glyph, at, flags, depth int) (int, bool) {
	sh.positioning = true
	n, _, ok := sh.chainedContext(sub, buf, at, flags, depth)
	return n, ok
}

// singlePosAt applies a type 1 subtable — one adjustment for every covered
// glyph — at a position, reading the subtable rather than the flat table, since
// a lookup reached from a rule is usually named by no feature and so is not in
// it.
func (sh shaper) singlePosAt(sub []byte, buf []Glyph, at int) int {
	if len(sub) < 6 {
		return 0
	}
	covered, ok := coverageIndex(sub, font.Be16(sub, 2), buf[at].GID)
	if !ok {
		return 0
	}
	format := font.Be16(sub, 4)
	var adj singleAdjust
	switch font.Be16(sub, 0) {
	case 1:
		adj = readValueRecord(sub[6:], format)
	case 2:
		size := valueSize(format)
		off := 8 + covered*size
		if covered >= font.Be16(sub, 6) || off+size > len(sub) {
			return 0
		}
		adj = readValueRecord(sub[off:], format)
	default:
		return 0
	}
	buf[at].XOffset += sh.f.scale(adj.xPlacement)
	buf[at].YOffset += sh.f.scale(adj.yPlacement)
	buf[at].XAdvance += sh.f.scale(adj.xAdvance)
	return 1
}

// pairPosAt applies a type 2 subtable to the pair beginning at a position.
//
// It reads the subtable rather than the flat kern table for the same reason
// singlePosAt does, and because the flat table is keyed by glyph pair with no
// record of which lookup stated it — which is exactly what a rule is naming.
func (sh shaper) pairPosAt(sub []byte, buf []Glyph, at, flags int) int {
	if len(sub) < 8 || at+1 >= len(buf) {
		return 0
	}
	next := sh.nextNotIgnored(buf, at+1, flags, buf[at+1].GID)
	if next >= sh.end(buf) {
		return 0
	}
	covered, ok := coverageIndex(sub, font.Be16(sub, 2), buf[at].GID)
	if !ok {
		return 0
	}
	format1, format2 := font.Be16(sub, 4), font.Be16(sub, 6)
	var adj pairAdjust
	switch font.Be16(sub, 0) {
	case 1:
		if len(sub) < 10 || covered >= font.Be16(sub, 8) || 10+2*covered+2 > len(sub) {
			return 0
		}
		off := font.Be16(sub, 10+2*covered)
		if off <= 0 || off+2 > len(sub) {
			return 0
		}
		set := sub[off:]
		size := 2 + valueSize(format1) + valueSize(format2)
		found := false
		for j, rec := 0, 2; j < font.Be16(set, 0); j, rec = j+1, rec+size {
			if rec+size > len(set) {
				break
			}
			if font.Be16(set, rec) == buf[next].GID {
				adj, found = pairAdjustFrom(set[rec+2:], format1, format2), true
				break
			}
		}
		if !found {
			return 0
		}
	case 2:
		if len(sub) < 16 {
			return 0
		}
		c1 := classDef(sub, font.Be16(sub, 8))[buf[at].GID]
		c2 := classDef(sub, font.Be16(sub, 10))[buf[next].GID]
		n1, n2 := font.Be16(sub, 12), font.Be16(sub, 14)
		size := valueSize(format1) + valueSize(format2)
		off := 16 + (c1*n2+c2)*size
		if c1 >= n1 || c2 >= n2 || size == 0 || off+size > len(sub) {
			return 0
		}
		adj = pairAdjustFrom(sub[off:], format1, format2)
	default:
		return 0
	}
	if adj.zero() {
		return 0
	}
	buf[at].XOffset += sh.f.scale(int(adj.firstX))
	buf[at].YOffset += sh.f.scale(int(adj.firstY))
	buf[at].XAdvance += sh.f.scale(int(adj.firstAdvance))
	buf[next].XOffset += sh.f.scale(int(adj.secondX))
	buf[next].YOffset += sh.f.scale(int(adj.secondY))
	buf[next].XAdvance += sh.f.scale(int(adj.secondAdvance))
	return next - at + 1
}
