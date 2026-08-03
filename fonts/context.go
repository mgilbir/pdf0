package fonts

import "github.com/mgilbir/pdf0/internal/font"

// Contextual substitution: rules that fire only where a glyph has particular
// neighbours.
//
// Everything else this package reads can be flattened at load — a kern pair is
// a pair, a ligature is a run — because each rule stands alone. A contextual
// rule does not. It says "where this sequence occurs, apply lookup number seven
// at position two", and lookup seven is another lookup with its own type and
// its own subtables. So the lookups have to be kept as things that can be
// *applied*, at a position, on demand, and possibly from inside one another.
//
// That is what this file adds, and why it could not be bolted onto the flat
// tables: the indirection is the feature. 'calt' — contextual alternates, the
// feature that swaps a glyph for a better-fitting variant next to particular
// neighbours — does nothing without it, and it is the most widely used feature
// in modern Latin fonts after kerning and ligatures.
//
// # What is matched
//
// All six forms: sequence context by glyph, by class and by coverage, and the
// chained versions of each, which also match what comes before and after the
// part being replaced. Backtrack sequences are stored nearest-first, which is
// the format's convention and reads backwards from every other list here.

// rawLookup is a lookup kept whole, so that a contextual rule can name it.
type rawLookup struct {
	kind  int
	flags int
	subs  [][]byte
}

// applyContextual runs the substitution lookups of a feature over a buffer,
// left to right, applying each where it matches.
//
// A lookup may replace a run with fewer glyphs, so the position advances by
// what the lookup consumed rather than by one.
func (f *Face) applyContextual(buf []Glyph, lookups []int) []Glyph {
	for _, idx := range lookups {
		for i := 0; i < len(buf); {
			consumed, out := f.applyGSUBAt(idx, buf, i, 0)
			if consumed > 0 {
				buf = out
				i += consumed
				continue
			}
			i++
		}
	}
	return buf
}

// maxLookupRecursion bounds how deeply a contextual rule may call into another.
// A font can describe a cycle — rule A applying lookup B which applies A — and
// nothing in the format forbids it, so the depth is what stops it.
const maxLookupRecursion = 8

// applyGSUBAt applies one GSUB lookup at a position, returning how many input
// glyphs it consumed (zero when it did not match) and the resulting buffer.
func (f *Face) applyGSUBAt(idx int, buf []Glyph, at, depth int) (int, []Glyph) {
	if depth > maxLookupRecursion || idx < 0 || idx >= len(f.layout.gsub) || at >= len(buf) {
		return 0, buf
	}
	lk := f.layout.gsub[idx]
	for _, sub := range lk.subs {
		switch lk.kind {
		case 1:
			if gid, ok := singleSubstAt(sub, buf[at].GID); ok {
				buf[at].GID = gid
				buf[at].XAdvance = f.advanceGID(gid)
				return 1, buf
			}
		case 4:
			if n, gid, ok := f.ligatureAt(sub, buf, at, lk.flags); ok {
				out := append(buf[:at:at], Glyph{
					GID: gid, Cluster: buf[at].Cluster, XAdvance: f.advanceGID(gid),
				})
				out = append(out, buf[at+n:]...)
				return 1, out
			}
		case 5:
			if n, out, ok := f.sequenceContext(sub, buf, at, lk.flags, depth); ok {
				return n, out
			}
		case 6:
			if n, out, ok := f.chainedContext(sub, buf, at, lk.flags, depth); ok {
				return n, out
			}
		}
	}
	return 0, buf
}

// singleSubstAt reads a type 1 subtable and reports the replacement for a
// glyph, if it covers one.
func singleSubstAt(sub []byte, gid int) (int, bool) {
	if len(sub) < 6 {
		return 0, false
	}
	i, ok := coverageIndex(sub, font.Be16(sub, 2), gid)
	if !ok {
		return 0, false
	}
	switch font.Be16(sub, 0) {
	case 1:
		to := gid + signed16(font.Be16(sub, 4))
		if to < 0 || to > 0xFFFF {
			return 0, false
		}
		return to, true
	case 2:
		if 6+2*i+2 > len(sub) || i >= font.Be16(sub, 4) {
			return 0, false
		}
		return font.Be16(sub, 6+2*i), true
	}
	return 0, false
}

// ligatureAt reads a type 4 subtable and reports the ligature starting at a
// position, with how many glyphs it consumes.
func (f *Face) ligatureAt(sub []byte, buf []Glyph, at, flags int) (int, int, bool) {
	if len(sub) < 6 || font.Be16(sub, 0) != 1 {
		return 0, 0, false
	}
	i, ok := coverageIndex(sub, font.Be16(sub, 2), buf[at].GID)
	if !ok || i >= font.Be16(sub, 4) || 6+2*i+2 > len(sub) {
		return 0, 0, false
	}
	off := font.Be16(sub, 6+2*i)
	if off <= 0 || off+2 > len(sub) {
		return 0, 0, false
	}
	set := sub[off:]
	for j := 0; j < font.Be16(set, 0); j++ {
		if 2+2*j+2 > len(set) {
			break
		}
		lo := font.Be16(set, 2+2*j)
		if lo <= 0 || lo+4 > len(set) {
			continue
		}
		lig := set[lo:]
		compCount := font.Be16(lig, 2)
		if compCount < 1 || compCount > 64 {
			continue
		}
		// Match the components against the glyphs after this one, skipping
		// those the lookup ignores.
		pos := at
		matched := true
		for k := 0; k < compCount-1; k++ {
			if 4+2*k+2 > len(lig) {
				matched = false
				break
			}
			pos = f.nextNotIgnored(buf, pos+1, flags)
			if pos >= len(buf) || buf[pos].GID != font.Be16(lig, 4+2*k) {
				matched = false
				break
			}
		}
		if matched {
			return pos - at + 1, font.Be16(lig, 0), true
		}
	}
	return 0, 0, false
}

// nextNotIgnored is the next position a lookup with these flags looks at.
func (f *Face) nextNotIgnored(buf []Glyph, from, flags int) int {
	for i := from; i < len(buf); i++ {
		if !f.layout.ignores(flags, buf[i].GID) {
			return i
		}
	}
	return len(buf)
}

// matched collects the positions a lookup with these flags would see, starting
// at a position, up to n of them.
func (f *Face) matchedPositions(buf []Glyph, at, n, flags int) ([]int, bool) {
	out := make([]int, 0, n)
	pos := at
	for len(out) < n {
		if pos >= len(buf) {
			return nil, false
		}
		if !f.layout.ignores(flags, buf[pos].GID) {
			out = append(out, pos)
		}
		pos++
	}
	return out, true
}

// backtrackPositions collects the positions before a position, nearest first,
// which is the order the format stores a backtrack sequence in.
func (f *Face) backtrackPositions(buf []Glyph, before, n, flags int) ([]int, bool) {
	out := make([]int, 0, n)
	for pos := before - 1; pos >= 0 && len(out) < n; pos-- {
		if !f.layout.ignores(flags, buf[pos].GID) {
			out = append(out, pos)
		}
	}
	return out, len(out) == n
}

// sequenceContext matches a GSUB type 5 subtable and applies its lookups.
func (f *Face) sequenceContext(sub []byte, buf []Glyph, at, flags, depth int) (int, []Glyph, bool) {
	if len(sub) < 4 {
		return 0, buf, false
	}
	switch font.Be16(sub, 0) {
	case 1:
		i, ok := coverageIndex(sub, font.Be16(sub, 2), buf[at].GID)
		if !ok {
			return 0, buf, false
		}
		return f.contextRuleSet(sub, 6, i, buf, at, flags, depth, func(item, pos int) bool {
			return buf[pos].GID == item
		})
	case 2:
		if len(sub) < 8 {
			return 0, buf, false
		}
		if _, ok := coverageIndex(sub, font.Be16(sub, 2), buf[at].GID); !ok {
			return 0, buf, false
		}
		classes := classDef(sub, font.Be16(sub, 4))
		return f.contextRuleSet(sub, 8, classes[buf[at].GID], buf, at, flags, depth, func(item, pos int) bool {
			return classes[buf[pos].GID] == item
		})
	case 3:
		glyphCount := font.Be16(sub, 2)
		recCount := font.Be16(sub, 4)
		if glyphCount < 1 || 6+2*glyphCount > len(sub) {
			return 0, buf, false
		}
		positions, ok := f.matchedPositions(buf, at, glyphCount, flags)
		if !ok {
			return 0, buf, false
		}
		for k := 0; k < glyphCount; k++ {
			if _, covered := coverageIndex(sub, font.Be16(sub, 6+2*k), buf[positions[k]].GID); !covered {
				return 0, buf, false
			}
		}
		return f.runRecords(sub, 6+2*glyphCount, recCount, positions, buf, depth)
	}
	return 0, buf, false
}

// contextRuleSet walks the rule sets of a format 1 or 2 sequence context, whose
// only difference is whether a rule's items are glyphs or classes.
func (f *Face) contextRuleSet(sub []byte, setsAt, index int, buf []Glyph, at, flags, depth int,
	match func(item, pos int) bool) (int, []Glyph, bool) {

	count := font.Be16(sub, setsAt-2)
	if index < 0 || index >= count || setsAt+2*index+2 > len(sub) {
		return 0, buf, false
	}
	off := font.Be16(sub, setsAt+2*index)
	if off <= 0 || off+2 > len(sub) {
		return 0, buf, false
	}
	set := sub[off:]
	for r := 0; r < font.Be16(set, 0); r++ {
		if 2+2*r+2 > len(set) {
			break
		}
		ro := font.Be16(set, 2+2*r)
		if ro <= 0 || ro+4 > len(set) {
			continue
		}
		rule := set[ro:]
		glyphCount := font.Be16(rule, 0)
		recCount := font.Be16(rule, 2)
		if glyphCount < 1 || 4+2*(glyphCount-1) > len(rule) {
			continue
		}
		positions, ok := f.matchedPositions(buf, at, glyphCount, flags)
		if !ok {
			continue
		}
		matched := true
		for k := 1; k < glyphCount; k++ {
			if !match(font.Be16(rule, 4+2*(k-1)), positions[k]) {
				matched = false
				break
			}
		}
		if !matched {
			continue
		}
		return f.runRecords(rule, 4+2*(glyphCount-1), recCount, positions, buf, depth)
	}
	return 0, buf, false
}

// chainedContext matches a GSUB type 6 subtable, which also constrains what
// comes before and after the part being replaced.
func (f *Face) chainedContext(sub []byte, buf []Glyph, at, flags, depth int) (int, []Glyph, bool) {
	if len(sub) < 4 {
		return 0, buf, false
	}
	switch font.Be16(sub, 0) {
	case 1, 2:
		var classes, backClasses, aheadClasses map[int]int
		setsAt := 6
		if font.Be16(sub, 0) == 2 {
			if len(sub) < 12 {
				return 0, buf, false
			}
			backClasses = classDef(sub, font.Be16(sub, 4))
			classes = classDef(sub, font.Be16(sub, 6))
			aheadClasses = classDef(sub, font.Be16(sub, 8))
			setsAt = 12
		}
		// A rule set is chosen by coverage index in the glyph form and by input
		// class in the class form; coverage still gates whether any rule applies.
		index, ok := coverageIndex(sub, font.Be16(sub, 2), buf[at].GID)
		if !ok {
			return 0, buf, false
		}
		if classes != nil {
			index = classes[buf[at].GID]
		}
		return f.chainedRuleSet(sub, setsAt, index, buf, at, flags, depth,
			classes, backClasses, aheadClasses)
	case 3:
		return f.chainedFormat3(sub, buf, at, flags, depth)
	}
	return 0, buf, false
}

// chainedRuleSet walks the rule sets of a chained context in glyph or class
// form. A rule states three sequences — what must precede, what is matched, and
// what must follow — and the first is stored nearest-first.
func (f *Face) chainedRuleSet(sub []byte, setsAt, index int, buf []Glyph, at, flags, depth int,
	classes, backClasses, aheadClasses map[int]int) (int, []Glyph, bool) {

	count := font.Be16(sub, setsAt-2)
	if index < 0 || index >= count || setsAt+2*index+2 > len(sub) {
		return 0, buf, false
	}
	off := font.Be16(sub, setsAt+2*index)
	if off <= 0 || off+2 > len(sub) {
		return 0, buf, false
	}
	set := sub[off:]
	byGlyph := classes == nil
	item := func(m map[int]int, pos int) int {
		if byGlyph {
			return buf[pos].GID
		}
		return m[buf[pos].GID]
	}

	for r := 0; r < font.Be16(set, 0); r++ {
		if 2+2*r+2 > len(set) {
			break
		}
		ro := font.Be16(set, 2+2*r)
		if ro <= 0 || ro+2 > len(set) {
			continue
		}
		rule := set[ro:]
		p := 0
		read := func() (int, bool) {
			if p+2 > len(rule) {
				return 0, false
			}
			v := font.Be16(rule, p)
			p += 2
			return v, true
		}
		backCount, ok := read()
		if !ok {
			continue
		}
		back := make([]int, 0, backCount)
		for k := 0; k < backCount; k++ {
			v, ok := read()
			if !ok {
				break
			}
			back = append(back, v)
		}
		inputCount, ok := read()
		if !ok || inputCount < 1 {
			continue
		}
		input := make([]int, 0, inputCount-1)
		for k := 0; k < inputCount-1; k++ {
			v, ok := read()
			if !ok {
				break
			}
			input = append(input, v)
		}
		aheadCount, ok := read()
		if !ok {
			continue
		}
		ahead := make([]int, 0, aheadCount)
		for k := 0; k < aheadCount; k++ {
			v, ok := read()
			if !ok {
				break
			}
			ahead = append(ahead, v)
		}
		recCount, ok := read()
		if !ok {
			continue
		}

		positions, ok := f.matchedPositions(buf, at, inputCount, flags)
		if !ok || len(input) != inputCount-1 {
			continue
		}
		matched := true
		for k := 1; k < inputCount; k++ {
			if item(classes, positions[k]) != input[k-1] {
				matched = false
				break
			}
		}
		if matched && len(back) > 0 {
			bp, ok := f.backtrackPositions(buf, at, len(back), flags)
			if !ok {
				matched = false
			} else {
				for k, want := range back {
					if item(backClasses, bp[k]) != want {
						matched = false
						break
					}
				}
			}
		}
		if matched && len(ahead) > 0 {
			ap, ok := f.matchedPositions(buf, positions[inputCount-1]+1, len(ahead), flags)
			if !ok {
				matched = false
			} else {
				for k, want := range ahead {
					if item(aheadClasses, ap[k]) != want {
						matched = false
						break
					}
				}
			}
		}
		if !matched {
			continue
		}
		return f.runRecords(rule, p, recCount, positions, buf, depth)
	}
	return 0, buf, false
}

// chainedFormat3 is the coverage-based chained context, the form a modern font
// uses most: three lists of coverage tables rather than rule sets.
func (f *Face) chainedFormat3(sub []byte, buf []Glyph, at, flags, depth int) (int, []Glyph, bool) {
	p := 2
	readList := func() ([]int, bool) {
		if p+2 > len(sub) {
			return nil, false
		}
		n := font.Be16(sub, p)
		p += 2
		if p+2*n > len(sub) {
			return nil, false
		}
		out := make([]int, n)
		for i := range out {
			out[i] = font.Be16(sub, p+2*i)
		}
		p += 2 * n
		return out, true
	}
	back, ok := readList()
	if !ok {
		return 0, buf, false
	}
	input, ok := readList()
	if !ok || len(input) < 1 {
		return 0, buf, false
	}
	ahead, ok := readList()
	if !ok {
		return 0, buf, false
	}
	if p+2 > len(sub) {
		return 0, buf, false
	}
	recCount := font.Be16(sub, p)
	p += 2

	positions, ok := f.matchedPositions(buf, at, len(input), flags)
	if !ok {
		return 0, buf, false
	}
	for k, cov := range input {
		if _, covered := coverageIndex(sub, cov, buf[positions[k]].GID); !covered {
			return 0, buf, false
		}
	}
	if len(back) > 0 {
		bp, ok := f.backtrackPositions(buf, at, len(back), flags)
		if !ok {
			return 0, buf, false
		}
		for k, cov := range back {
			if _, covered := coverageIndex(sub, cov, buf[bp[k]].GID); !covered {
				return 0, buf, false
			}
		}
	}
	if len(ahead) > 0 {
		ap, ok := f.matchedPositions(buf, positions[len(input)-1]+1, len(ahead), flags)
		if !ok {
			return 0, buf, false
		}
		for k, cov := range ahead {
			if _, covered := coverageIndex(sub, cov, buf[ap[k]].GID); !covered {
				return 0, buf, false
			}
		}
	}
	return f.runRecords(sub, p, recCount, positions, buf, depth)
}

// runRecords applies the lookups a matched rule names, each at the position it
// names, and reports how many input glyphs the rule consumed.
//
// The positions are those of the *matched* glyphs, so a record naming index two
// means the third thing the rule matched — not the third glyph in the buffer,
// which may differ when the lookup skips marks.
func (f *Face) runRecords(base []byte, at, count int, positions []int, buf []Glyph, depth int) (int, []Glyph, bool) {
	consumed := len(positions)
	for i := 0; i < count; i++ {
		rec := at + 4*i
		if rec+4 > len(base) {
			break
		}
		seqIndex := font.Be16(base, rec)
		lookupIndex := font.Be16(base, rec+2)
		if seqIndex < 0 || seqIndex >= len(positions) {
			continue
		}
		before := len(buf)
		_, out := f.applyGSUBAt(lookupIndex, buf, positions[seqIndex], depth+1)
		buf = out
		// A nested lookup may have shortened the buffer; the rule then consumes
		// correspondingly fewer glyphs, since some of what it matched is gone.
		if shrunk := before - len(buf); shrunk > 0 {
			consumed -= shrunk
			if consumed < 1 {
				consumed = 1
			}
		}
	}
	return consumed, buf, true
}

// coverageIndex reports a glyph's index within a coverage table, which is what
// the tables using one are indexed by.
func coverageIndex(base []byte, off, gid int) (int, bool) {
	if off <= 0 || off+4 > len(base) {
		return 0, false
	}
	c := base[off:]
	switch font.Be16(c, 0) {
	case 1:
		n := font.Be16(c, 2)
		lo, hi := 0, n-1
		for lo <= hi {
			mid := (lo + hi) / 2
			if 4+2*mid+2 > len(c) {
				return 0, false
			}
			g := font.Be16(c, 4+2*mid)
			switch {
			case g == gid:
				return mid, true
			case g < gid:
				lo = mid + 1
			default:
				hi = mid - 1
			}
		}
	case 2:
		n := font.Be16(c, 2)
		for i := 0; i < n; i++ {
			rec := 4 + 6*i
			if rec+6 > len(c) {
				return 0, false
			}
			start, end := font.Be16(c, rec), font.Be16(c, rec+2)
			if gid >= start && gid <= end {
				return font.Be16(c, rec+4) + (gid - start), true
			}
		}
	}
	return 0, false
}
