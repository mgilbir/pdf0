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
//
// # Which glyph gives ground horizontally
//
// The joint is between the first glyph's exit and the second's entry whichever
// way the run is drawn, but which of the two has to move is not the same. Drawn
// left to right the first glyph is reached first, so it is cut short at its exit
// and the second is pulled back onto it. Drawn right to left the pen reaches the
// *second* glyph first — the run is reversed after this — so it is that one that
// stops at its entry, and the first that is pulled back onto it. Doing it the
// left-to-right way for a right-to-left run leaves every letter of an Arabic
// word displaced by the width of its neighbour.
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
				// The glyph the pen reaches first advances exactly to the joint,
				// and the other is pulled back so its own anchor lands there. The
				// offsets already in place are carried through: a glyph moved by
				// a single adjustment joins from where it now is.
				if sh.rtl {
					d := sh.f.scale(a.exit.x) + buf[prev].XOffset
					buf[prev].XAdvance -= d
					buf[prev].XOffset -= d
					buf[i].XAdvance = sh.f.scale(b.entry.x) + buf[i].XOffset
				} else {
					buf[prev].XAdvance = sh.f.scale(a.exit.x) + buf[prev].XOffset
					d := sh.f.scale(b.entry.x) + buf[i].XOffset
					buf[i].XAdvance -= d
					buf[i].XOffset -= d
				}
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
	if len(l.markGlyphs) == 0 {
		return
	}
	for i := range buf {
		if !l.markGlyphs[buf[i].GID] {
			continue
		}
		// Find what this mark attaches to, and by which table.
		for j := i - 1; j >= 0; j-- {
			prevIsMark := l.isMark(buf[j].GID)
			set := l.markBase
			if prevIsMark {
				set = l.markMark
			}
			// Which part of the base this mark belongs to, if the base is a
			// ligature and the mark came from inside it. Zero otherwise, which
			// anchorFor reads as "the last part".
			component := 0
			if lig := buf[i].lig; lig.id != 0 && lig.id == buf[j].lig.id {
				component = lig.comp
			}
			mark, base, found := attachmentFor(set, buf[i].GID, buf[j].GID, component)
			if !found {
				if prevIsMark {
					continue // stacked marks: keep looking back for the base
				}
				break // a base that says nothing about this mark
			}
			// Place the mark so its anchor meets the base's. The pen is at the
			// end of everything drawn since the base, so the advances between
			// have to be taken back off — and the base's own displacement
			// carried along, since a base moved by a single adjustment or lifted
			// onto a joining stroke takes its accents with it.
			//
			// What has to be corrected for is where the pen will be when the
			// mark is drawn, and that depends on which way the run is drawn.
			// Left to right the pen has passed the base and everything between
			// them, so those advances come off. Right to left the buffer is
			// about to be reversed and the mark will be drawn *before* its
			// base, so the same advances are still ahead of the pen and go on
			// rather than off. The mark's own advance is zeroed first, so that
			// a font which gave its marks a width does not have it counted.
			buf[i].XAdvance = 0
			var since float64
			if sh.rtl {
				for k := j + 1; k <= i; k++ {
					since -= buf[k].XAdvance
				}
			} else {
				for k := j; k < i; k++ {
					since += buf[k].XAdvance
				}
			}
			buf[i].XOffset = buf[j].XOffset + sh.f.scale(base.x-mark.anchor.x) - since
			buf[i].YOffset = buf[j].YOffset + sh.f.scale(base.y-mark.anchor.y)
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

// markAttachment is one mark-attachment subtable, kept whole: which marks it
// covers, with the class and anchor of each, and where a base receives a mark
// of each class. lookup is the index of the lookup it came from, which is what
// tells subtables that are alternatives to each other from subtables that are
// applied one after another.
type markAttachment struct {
	lookup int
	marks  map[int]markAnchor
	// bases is where a base receives a mark of each class: mark-to-base and
	// mark-to-mark. components is the same for mark-to-ligature, which states
	// one anchor per component of the ligature rather than one for the whole of
	// it. A subtable has one or the other, never both.
	bases      map[key2]anchor
	components map[key2][]anchor
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
func (l *layout) readGPOSAttachment(gpos []byte, feats tableFeatures) {
	for _, tag := range featureTags(gpos, feats.sel) {
		for _, lookup := range featureLookups(gpos, tag, feats) {
			kind, flags, subs := subtables(lookup, 9)
			switch kind {
			case 4, 5:
				// Mark-to-base and mark-to-ligature go into one ordered set:
				// they are alternatives for the same mark, decided by which
				// lookup covers the glyph the mark is attaching to, and a
				// ligature glyph may be covered by either.
				l.readMarkAttachment(subs, kind == 5, false)
			case 6:
				l.readMarkAttachment(subs, false, true)
			default:
				for _, sub := range subs {
					switch kind {
					case 1:
						l.singlePosSubtable(sub)
					case 3:
						l.cursivePos(sub)
					}
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

// readMarkAttachment reads all the subtables of one mark-to-base or
// mark-to-mark lookup, keeping each subtable whole.
//
// Keeping them apart is the whole point. A mark class is a number local to the
// subtable that declares it: Noto Sans has eighteen mark-to-base subtables, and
// class 0 means one thing in the one that places accents over Latin letters and
// something else entirely in each of the others. A reader that merges them into
// one table keyed by glyph and class pairs a mark's anchor from the subtable
// that covers the mark with a base's anchor from whichever subtable was read
// last — an anchor written for four particular marks, applied to all two
// hundred and fifty of them.
//
// The two kinds have the same shape — a mark array and an array of attachment
// points, one per class — and differ only in what the second array is indexed
// by, so one reader serves both.
func (l *layout) readMarkAttachment(subs [][]byte, ligature, mkmk bool) {
	lookup := l.markLookups
	l.markLookups++
	for _, sub := range subs {
		st, ok := readMarkSubtable(sub, lookup, ligature)
		if !ok {
			continue
		}
		for gid := range st.marks {
			l.markGlyphs[gid] = true
		}
		if mkmk {
			l.markMark = append(l.markMark, st)
		} else {
			l.markBase = append(l.markBase, st)
		}
	}
}

// readMarkSubtable reads one mark-attachment subtable.
func readMarkSubtable(sub []byte, lookup int, ligature bool) (markAttachment, bool) {
	if len(sub) < 12 || font.Be16(sub, 0) != 1 {
		return markAttachment{}, false
	}
	markCoverage := coverageGlyphs(sub, font.Be16(sub, 2))
	baseCoverage := coverageGlyphs(sub, font.Be16(sub, 4))
	classCount := font.Be16(sub, 6)
	markArrayOff := font.Be16(sub, 8)
	baseArrayOff := font.Be16(sub, 10)
	if classCount <= 0 || classCount > 1024 {
		return markAttachment{}, false
	}
	st := markAttachment{
		lookup: lookup,
		marks:  map[int]markAnchor{},
		bases:  map[key2]anchor{},
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
			st.marks[markCoverage[i]] = markAnchor{class: class, anchor: a}
		}
	}

	switch {
	case ligature:
		readLigatureArray(sub, baseArrayOff, baseCoverage, classCount, &st)
	default:
		readBaseArray(sub, baseArrayOff, baseCoverage, classCount, &st)
	}
	if len(st.marks) == 0 || (len(st.bases) == 0 && len(st.components) == 0) {
		return markAttachment{}, false
	}
	return st, true
}

// readBaseArray reads a BaseArray: one anchor per class for each covered base.
func readBaseArray(sub []byte, off int, coverage []int, classCount int, st *markAttachment) {
	if off <= 0 || off+2 > len(sub) {
		return
	}
	ba := sub[off:]
	n := font.Be16(ba, 0)
	for i := 0; i < n && i < len(coverage); i++ {
		for c := 0; c < classCount; c++ {
			rec := 2 + (i*classCount+c)*2
			if rec+2 > len(ba) {
				break
			}
			a, ok := readAnchor(ba, font.Be16(ba, rec))
			if !ok {
				continue
			}
			st.bases[key2{coverage[i], c}] = a
		}
	}
}

// readLigatureArray reads a LigatureArray, which is the one thing that makes a
// mark-to-ligature subtable different from a mark-to-base one.
//
// A ligature is several letters drawn as one glyph, so a mark written under it
// has to say which of them it belongs to: a dot under the first f of "ffi" goes
// somewhere quite different from a dot under the second. The font answers by
// giving each ligature not one anchor per class but one per component per class,
// and the shaper picks the component from which part of the text the mark came
// from — which is why forming a ligature has to record that.
func readLigatureArray(sub []byte, off int, coverage []int, classCount int, st *markAttachment) {
	if off <= 0 || off+2 > len(sub) {
		return
	}
	la := sub[off:]
	n := font.Be16(la, 0)
	st.components = map[key2][]anchor{}
	for i := 0; i < n && i < len(coverage); i++ {
		rec := 2 + 2*i
		if rec+2 > len(la) {
			break
		}
		attachOff := font.Be16(la, rec)
		if attachOff <= 0 || attachOff+2 > len(la) {
			continue
		}
		attach := la[attachOff:]
		count := font.Be16(attach, 0)
		// A ligature of more components than any font ever writes is malformed,
		// and the count is a length this would otherwise allocate from.
		if count < 1 || count > maxLigatureComponents {
			continue
		}
		for c := 0; c < classCount; c++ {
			anchors := make([]anchor, 0, count)
			any := false
			for comp := 0; comp < count; comp++ {
				a, ok := readAnchor(attach, font.Be16(attach, 2+(comp*classCount+c)*2))
				if 2+(comp*classCount+c)*2+2 > len(attach) {
					break
				}
				anchors = append(anchors, a)
				any = any || ok
			}
			// A class the ligature says nothing about for any component is not
			// stored, so that attachmentFor can tell "no anchor" from "an
			// anchor at the origin".
			if any {
				st.components[key2{coverage[i], c}] = anchors
			}
		}
	}
	if len(st.components) == 0 {
		st.components = nil
	}
}

// maxLigatureComponents bounds what a font may claim a ligature is made of. The
// longest anybody writes is a handful; a count near the format's ceiling is an
// allocation this would otherwise make on the strength of two untrusted bytes.
const maxLigatureComponents = 64

// attachmentFor finds where a mark meets a base, over a set of subtables read in
// lookup order.
//
// A subtable applies only when it covers *both* — the mark, so it knows the
// mark's class and its anchor, and that base for that class. Coverage of one
// without the other is a subtable that has nothing to say about this pair, and
// the search moves on.
//
// Which match wins follows how the lookups are applied. Within a lookup the
// subtables are alternatives and the first that applies is the one used; across
// lookups each runs in turn over the whole run, so a later lookup that applies
// overwrites what an earlier one placed. Hence: last applying lookup, first
// applying subtable within it.
func attachmentFor(set []markAttachment, markGID, baseGID, component int) (mark markAnchor, base anchor, ok bool) {
	matched := -1
	for i := range set {
		st := &set[i]
		if ok && st.lookup == matched {
			continue // this lookup already applied, by an earlier subtable
		}
		m, has := st.marks[markGID]
		if !has {
			continue
		}
		b, has := st.anchorFor(baseGID, m.class, component)
		if !has {
			continue
		}
		mark, base, ok, matched = m, b, true, st.lookup
	}
	return mark, base, ok
}

// anchorFor is where this subtable says a mark of a class attaches to a glyph,
// for a mark that came from a given component of it.
//
// component is 1-based and is zero for a mark that is not part of the glyph's
// own ligature — a mark written after it, or one the glyph is not a ligature
// for. Such a mark goes on the *last* component, which is what OpenType says and
// is the only sensible answer: a mark written after "ffi" belongs to the i.
func (st *markAttachment) anchorFor(gid, class, component int) (anchor, bool) {
	if st.components == nil {
		a, ok := st.bases[key2{gid, class}]
		return a, ok
	}
	anchors, ok := st.components[key2{gid, class}]
	if !ok || len(anchors) == 0 {
		return anchor{}, false
	}
	at := len(anchors) - 1
	if component > 0 && component <= len(anchors) {
		at = component - 1
	}
	return anchors[at], true
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
	return l.markGlyphs[gid]
}
