package fonts

import "github.com/mgilbir/pdf0/internal/font"

// Positioning: kerning, single adjustments, joining glyphs to each other, and
// attaching marks to what they belong to.
//
// Mark attachment is the piece the span model could not express, and the reason
// this package now shapes into glyphs. A font states, for each mark and each
// base, an *anchor* — a point in the glyph's own space — and attaching them
// means placing the mark so that its anchor coincides with the base's. That is
// two coordinates the font supplies and one subtraction, and without it an
// accent is drawn at its nominal advance: for most fonts, at the origin, so
// every accent in a run piles up in the same place.

// applyPositioning runs the GPOS lookups over a shaped buffer.
func (sh shaper) position(buf []Glyph) {
	l := sh.l
	// Pair kerning, which the buffer expresses as a change to the left glyph's
	// advance. Glyphs the lookup ignores do not break a pair.
	prev := -1
	for i := range buf {
		if l.ignores(l.kernFlags, buf[i].GID) {
			continue
		}
		if prev >= 0 {
			if k, ok := l.kern[[2]int{buf[prev].GID, buf[i].GID}]; ok && k != 0 {
				buf[prev].XAdvance += sh.f.scale(k)
			}
		}
		prev = i
	}
	// Single adjustments: a glyph nudged wherever it appears.
	for i := range buf {
		if adj, ok := l.singlePos[buf[i].GID]; ok {
			buf[i].XOffset += sh.f.scale(adj.xPlacement)
			buf[i].YOffset += sh.f.scale(adj.yPlacement)
			buf[i].XAdvance += sh.f.scale(adj.xAdvance)
		}
	}
	// Cursive attachment before marks: a mark is placed relative to a base that
	// has already been moved onto the joining stroke, and doing it the other way
	// round leaves the accent where the letter used to be.
	sh.attachCursive(buf)
	sh.attachMarks(buf)
}

// cursiveAnchors is where a glyph's connecting stroke leaves and arrives.
type cursiveAnchors struct {
	entry, exit       anchor
	hasEntry, hasExit bool
}

// attachCursive joins glyphs whose strokes are meant to connect.
//
// This is the other half of what a cursive script needs. Joining (arabic.go)
// picks the right *shape* for each position; this makes the shapes actually
// meet. The font gives each glyph an exit point and an entry point, and
// attaching them means placing the second so its entry lands exactly on the
// first's exit — horizontally by shortening the advance between them, and
// vertically by lifting one off the baseline, because a joining stroke does not
// generally leave a letter at the height it enters the next.
//
// Which one is lifted is what the RightToLeft lookup flag decides, and it is the
// only thing that flag means. Set, the last glyph of a run stays put and the
// earlier ones climb to meet it — which is what an Arabic font wants, since the
// word is read from the end this pass reaches last. Clear, the first stays and
// the rest follow. Getting it backwards keeps every joint correct relative to
// its neighbour and leaves the whole word sitting off the baseline.
func (sh shaper) attachCursive(buf []Glyph) {
	l := sh.l
	if len(l.cursive) == 0 {
		return
	}
	// A link is one joint: the two positions it connects and the height the
	// second sits at relative to the first.
	type link struct {
		from, to int
		dy       float64
	}
	var links []link

	prev := -1
	for i := range buf {
		if l.ignores(l.cursFlags, buf[i].GID) {
			continue
		}
		if prev >= 0 {
			a, okA := l.cursive[buf[prev].GID]
			b, okB := l.cursive[buf[i].GID]
			if okA && a.hasExit && okB && b.hasEntry {
				// The first glyph now advances exactly to its exit point, and
				// the second is pulled back so its entry point lands there. The
				// offsets already in place are carried through: a glyph moved by
				// a single adjustment joins from where it now is.
				buf[prev].XAdvance = sh.f.scale(a.exit.x) + buf[prev].XOffset
				d := sh.f.scale(b.entry.x) + buf[i].XOffset
				buf[i].XAdvance -= d
				buf[i].XOffset -= d
				links = append(links, link{from: prev, to: i, dy: sh.f.scale(a.exit.y - b.entry.y)})
			}
		}
		prev = i
	}

	// The heights are a chain, so they propagate from whichever end is anchored:
	// forwards from the first glyph, or backwards from the last.
	if l.cursFlags&flagRightToLeft != 0 {
		for k := len(links) - 1; k >= 0; k-- {
			buf[links[k].from].YOffset = buf[links[k].to].YOffset - links[k].dy
		}
		return
	}
	for _, ln := range links {
		buf[ln.to].YOffset = buf[ln.from].YOffset + ln.dy
	}
}

// attachMarks places every mark on the glyph it belongs to.
//
// A mark attaches to the nearest preceding glyph that is not itself a mark
// (mark-to-base), or to the nearest preceding mark (mark-to-mark), which is how
// two accents stack. Both are the same operation over different tables, so both
// are done here in one pass backwards from each mark.
//
// The mark's advance is set to zero. A font gives its marks an advance of zero
// already, but not always, and a mark that moved the pen would push the next
// letter along by the width of an accent.
func (sh shaper) attachMarks(buf []Glyph) {
	l := sh.l
	if len(l.markAnchors) == 0 {
		return
	}
	for i := range buf {
		mark, isMark := l.markAnchors[buf[i].GID]
		if !isMark {
			continue
		}
		// Find what this mark attaches to, and by which table.
		for j := i - 1; j >= 0; j-- {
			prevIsMark := l.isMark(buf[j].GID)
			var (
				base  anchor
				found bool
			)
			if prevIsMark {
				base, found = l.markMarkBases[key2{buf[j].GID, mark.class}]
			} else {
				base, found = l.markBases[key2{buf[j].GID, mark.class}]
			}
			if !found {
				if prevIsMark {
					continue // stacked marks: keep looking back for the base
				}
				break // a base that says nothing about this mark class
			}
			// Place the mark so its anchor meets the base's. The pen is at the
			// end of everything drawn since the base, so the advances between
			// have to be taken back off — and the base's own displacement
			// carried along, since a base moved by a single adjustment or lifted
			// onto a joining stroke takes its accents with it.
			var since float64
			for k := j; k < i; k++ {
				since += buf[k].XAdvance
			}
			buf[i].XOffset = buf[j].XOffset + sh.f.scale(base.x-mark.anchor.x) - since
			buf[i].YOffset = buf[j].YOffset + sh.f.scale(base.y-mark.anchor.y)
			buf[i].XAdvance = 0
			break
		}
	}
}

// anchor is a point in a glyph's own coordinate space, in font units.
type anchor struct{ x, y int }

// key2 is a glyph and a mark class, which is how a base states where marks of
// each class attach to it.
type key2 struct {
	gid   int
	class int
}

// markAnchor is a mark's own attachment point and the class it belongs to.
// Classes let a font say that, for instance, a base's anchor for accents above
// is not the one for cedillas below.
type markAnchor struct {
	class  int
	anchor anchor
}

// singleAdjust is a GPOS type 1 adjustment: a nudge applied to a glyph wherever
// it occurs.
type singleAdjust struct {
	xPlacement, yPlacement, xAdvance int
}

// readGPOSAttachment reads the positioning lookups beyond pair kerning: single
// adjustments, cursive attachment, mark-to-base and mark-to-mark.
//
// They are read from every feature *tag* rather than from a named one, because
// attachment is not optional the way a stylistic feature is. A font that
// positions its marks through 'mark' and 'mkmk' — which is nearly all of them —
// and another that does it through a script-specific feature such as 'abvm'
// should both work, and applying an attachment that was not asked for cannot
// make text worse: a mark's place is a fact about the font, not a preference.
//
// That argument is about tags, and it survives script selection. The same
// argument for reading every *script* does not, and the selection is applied
// here for one concrete reason: the lookup flags are merged. cursFlags carries
// the RightToLeft bit, which decides which end of a joined run stays on the
// baseline, and a font with both Latin and Arabic cursive attachment states it
// one way for one and the other way for the other. Merging them sets a Latin
// word's whole cursive chain from the Arabic lookup's flag — precisely the "a
// rule meant for another script" this selection exists to stop.
func (l *layout) readGPOSAttachment(gpos []byte, sel featureSet) {
	for _, tag := range featureTags(gpos, sel) {
		for _, lookup := range featureLookups(gpos, tag, sel) {
			kind, flags, subs := subtables(lookup, 9)
			for _, sub := range subs {
				switch kind {
				case 1:
					l.singlePosSubtable(sub)
				case 3:
					l.cursivePos(sub)
				case 4:
					l.markToBase(sub, false)
				case 6:
					l.markToBase(sub, true)
				}
			}
			switch kind {
			case 3:
				l.cursFlags |= flags
			case 4, 6:
				l.markFlags |= flags
			}
		}
	}
}

// featureTags lists every feature tag a layout table declares and the selection
// admits, in order.
func featureTags(t []byte, sel featureSet) []string {
	off := font.Be16(t, 6)
	if off <= 0 || off+2 > len(t) {
		return nil
	}
	list := t[off:]
	n := font.Be16(list, 0)
	if n > maxLookups {
		n = maxLookups
	}
	seen := map[string]bool{}
	var out []string
	for i := 0; i < n; i++ {
		rec := 2 + 6*i
		if rec+6 > len(list) {
			break
		}
		if !sel.selects(i) {
			continue
		}
		tag := string(list[rec : rec+4])
		if !seen[tag] {
			seen[tag] = true
			out = append(out, tag)
		}
	}
	return out
}

// singlePosSubtable reads a GPOS type 1 subtable: one adjustment for every
// covered glyph (format 1) or one per glyph (format 2).
func (l *layout) singlePosSubtable(sub []byte) {
	if len(sub) < 6 {
		return
	}
	covered := coverageGlyphs(sub, font.Be16(sub, 2))
	format := font.Be16(sub, 0)
	valueFormat := font.Be16(sub, 4)
	size := valueSize(valueFormat)
	switch format {
	case 1:
		adj := readValueRecord(sub[6:], valueFormat)
		if adj == (singleAdjust{}) {
			return
		}
		for _, gid := range covered {
			l.singlePos[gid] = adj
		}
	case 2:
		n := font.Be16(sub, 6)
		for i := 0; i < n && i < len(covered); i++ {
			off := 8 + i*size
			if off+size > len(sub) {
				break
			}
			if adj := readValueRecord(sub[off:], valueFormat); adj != (singleAdjust{}) {
				l.singlePos[covered[i]] = adj
			}
		}
	}
}

// readValueRecord reads the placement and advance fields a ValueRecord may
// carry, in the fixed order the format defines.
func readValueRecord(rec []byte, format int) singleAdjust {
	var out singleAdjust
	off := 0
	take := func(bit int) int {
		if format&bit == 0 {
			return 0
		}
		if off+2 > len(rec) {
			return 0
		}
		v := signed16(font.Be16(rec, off))
		off += 2
		return v
	}
	out.xPlacement = take(0x0001)
	out.yPlacement = take(0x0002)
	out.xAdvance = take(0x0004)
	return out
}

// cursivePos reads a cursive attachment subtable: an entry and an exit anchor
// for each covered glyph, either of which may be absent — a letter that begins a
// word has nothing to join back to.
func (l *layout) cursivePos(sub []byte) {
	if len(sub) < 6 || font.Be16(sub, 0) != 1 {
		return
	}
	covered := coverageGlyphs(sub, font.Be16(sub, 2))
	n := font.Be16(sub, 4)
	for i := 0; i < n && i < len(covered); i++ {
		rec := 6 + 4*i
		if rec+4 > len(sub) {
			break
		}
		var c cursiveAnchors
		if a, ok := readAnchor(sub, font.Be16(sub, rec)); ok {
			c.entry, c.hasEntry = a, true
		}
		if a, ok := readAnchor(sub, font.Be16(sub, rec+2)); ok {
			c.exit, c.hasExit = a, true
		}
		if c.hasEntry || c.hasExit {
			l.cursive[covered[i]] = c
		}
	}
}

// markToBase reads a mark-to-base or mark-to-mark subtable. The two have the
// same shape — a mark array and an array of attachment points, one per class —
// and differ only in what the second array is indexed by, so one reader serves
// both.
func (l *layout) markToBase(sub []byte, mkmk bool) {
	if len(sub) < 12 || font.Be16(sub, 0) != 1 {
		return
	}
	markCoverage := coverageGlyphs(sub, font.Be16(sub, 2))
	baseCoverage := coverageGlyphs(sub, font.Be16(sub, 4))
	classCount := font.Be16(sub, 6)
	markArrayOff := font.Be16(sub, 8)
	baseArrayOff := font.Be16(sub, 10)
	if classCount <= 0 || classCount > 1024 {
		return
	}

	// The mark array: a class and an anchor for each covered mark.
	if markArrayOff > 0 && markArrayOff+2 <= len(sub) {
		ma := sub[markArrayOff:]
		n := font.Be16(ma, 0)
		for i := 0; i < n && i < len(markCoverage); i++ {
			rec := 2 + 4*i
			if rec+4 > len(ma) {
				break
			}
			class := font.Be16(ma, rec)
			a, ok := readAnchor(ma, font.Be16(ma, rec+2))
			if !ok || class >= classCount {
				continue
			}
			l.markAnchors[markCoverage[i]] = markAnchor{class: class, anchor: a}
		}
	}

	// The base array: one anchor per class for each covered base.
	if baseArrayOff > 0 && baseArrayOff+2 <= len(sub) {
		ba := sub[baseArrayOff:]
		n := font.Be16(ba, 0)
		for i := 0; i < n && i < len(baseCoverage); i++ {
			for c := 0; c < classCount; c++ {
				rec := 2 + (i*classCount+c)*2
				if rec+2 > len(ba) {
					break
				}
				a, ok := readAnchor(ba, font.Be16(ba, rec))
				if !ok {
					continue
				}
				k := key2{baseCoverage[i], c}
				if mkmk {
					l.markMarkBases[k] = a
				} else {
					l.markBases[k] = a
				}
			}
		}
	}
}

// readAnchor reads an anchor table. All three formats begin with the same two
// coordinates; the later formats add hinting information this ignores, which
// affects rendering at small sizes and not where the anchor is.
func readAnchor(base []byte, off int) (anchor, bool) {
	if off <= 0 || off+6 > len(base) {
		return anchor{}, false
	}
	a := base[off:]
	switch font.Be16(a, 0) {
	case 1, 2, 3:
		return anchor{x: signed16(font.Be16(a, 2)), y: signed16(font.Be16(a, 4))}, true
	}
	return anchor{}, false
}

// isMark reports whether a glyph is a mark.
//
// GDEF's classification is the authority — it is the font saying so — and the
// mark arrays are the fallback for a font that positions marks without
// classifying them. Asking only the mark arrays gets mark-to-mark wrong: the
// first of two stacked accents is a mark that no *mark-to-mark* array lists as
// one, because in that lookup it is the base.
func (l *layout) isMark(gid int) bool {
	if c, ok := l.glyphClass[gid]; ok {
		return c == classMark
	}
	_, inMarkArray := l.markAnchors[gid]
	return inMarkArray
}
