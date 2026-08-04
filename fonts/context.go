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
func (sh shaper) applyContextual(buf []Glyph, lookups []int) []Glyph {
	for _, idx := range lookups {
		for i := 0; i < len(buf); {
			consumed, out := sh.applyGSUBAt(idx, buf, i, 0)
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
func (sh shaper) applyGSUBAt(idx int, buf []Glyph, at, depth int) (int, []Glyph) {
	if depth > maxLookupRecursion || idx < 0 || idx >= len(sh.l.gsub) || at >= len(buf) {
		return 0, buf
	}
	lk := sh.l.gsub[idx]
	for _, sub := range lk.subs {
		switch lk.kind {
		case 1:
			if gid, ok := singleSubstAt(sub, buf[at].GID); ok {
				buf[at].GID = gid
				buf[at].XAdvance = sh.f.advanceGID(gid)
				return 1, buf
			}
		case 2:
			// One glyph becomes several: a decomposition, which is what 'ccmp'
			// is usually written with. They share the original's cluster —
			// several glyphs standing for one character is exactly the case
			// clusters exist to record.
			if reps, ok := multipleSubstAt(sub, buf[at].GID); ok && len(reps) > 0 {
				out := make([]Glyph, 0, len(buf)+len(reps)-1)
				out = append(out, buf[:at]...)
				for _, gid := range reps {
					out = append(out, Glyph{
						GID: gid, Cluster: buf[at].Cluster, XAdvance: sh.f.advanceGID(gid),
					})
				}
				out = append(out, buf[at+1:]...)
				sh.resized(at, len(reps)-1)
				return len(reps), out
			}
		case 3:
			if gid, ok := alternateSubstAt(sub, buf[at].GID); ok {
				buf[at].GID = gid
				buf[at].XAdvance = sh.f.advanceGID(gid)
				return 1, buf
			}
		case 4:
			if comps, gid, ok := sh.ligatureAt(sub, buf, at, lk.flags); ok {
				return sh.formLigature(buf, at, gid, comps)
			}
		case 5:
			if n, out, ok := sh.sequenceContext(sub, buf, at, lk.flags, depth); ok {
				return n, out
			}
		case 6:
			if n, out, ok := sh.chainedContext(sub, buf, at, lk.flags, depth); ok {
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

// multipleSubstAt reads a type 2 subtable and reports the glyphs that replace
// one, if it covers it.
func multipleSubstAt(sub []byte, gid int) ([]int, bool) {
	if len(sub) < 6 || font.Be16(sub, 0) != 1 {
		return nil, false
	}
	i, ok := coverageIndex(sub, font.Be16(sub, 2), gid)
	if !ok || i >= font.Be16(sub, 4) || 6+2*i+2 > len(sub) {
		return nil, false
	}
	off := font.Be16(sub, 6+2*i)
	if off <= 0 || off+2 > len(sub) {
		return nil, false
	}
	seq := sub[off:]
	n := font.Be16(seq, 0)
	// A sequence long enough to be a decompression bomb is malformed; a real
	// one is two or three glyphs.
	if n < 0 || n > maxSubstitutionLength || 2+2*n > len(seq) {
		return nil, false
	}
	out := make([]int, n)
	for k := range out {
		out[k] = font.Be16(seq, 2+2*k)
	}
	return out, true
}

// maxSubstitutionLength bounds what one glyph may become. A decomposition is a
// handful of glyphs; a font declaring thousands is describing an attack, and
// nothing stops it from doing so at every position in a run.
const maxSubstitutionLength = 64

// alternateSubstAt reads a type 3 subtable, which offers a choice of glyphs.
//
// The first is taken. Choosing among them is what 'aalt' is for and what a
// caller asks for by name; a lookup reached through a default feature has no
// one to ask, and the font lists its own preference first.
func alternateSubstAt(sub []byte, gid int) (int, bool) {
	if len(sub) < 6 || font.Be16(sub, 0) != 1 {
		return 0, false
	}
	i, ok := coverageIndex(sub, font.Be16(sub, 2), gid)
	if !ok || i >= font.Be16(sub, 4) || 6+2*i+2 > len(sub) {
		return 0, false
	}
	off := font.Be16(sub, 6+2*i)
	if off <= 0 || off+4 > len(sub) {
		return 0, false
	}
	set := sub[off:]
	if font.Be16(set, 0) < 1 {
		return 0, false
	}
	return font.Be16(set, 2), true
}

// ligatureAt reads a type 4 subtable and reports the ligature starting at a
// position, together with the positions of the glyphs it is made of.
//
// The positions rather than a count, because a lookup that ignores marks
// matches its components *across* them and the ones in between are not part of
// the rule. Only what the font named is replaced; formLigature keeps the rest.
func (sh shaper) ligatureAt(sub []byte, buf []Glyph, at, flags int) ([]int, int, bool) {
	if len(sub) < 6 || font.Be16(sub, 0) != 1 {
		return nil, 0, false
	}
	i, ok := coverageIndex(sub, font.Be16(sub, 2), buf[at].GID)
	if !ok || i >= font.Be16(sub, 4) || 6+2*i+2 > len(sub) {
		return nil, 0, false
	}
	off := font.Be16(sub, 6+2*i)
	if off <= 0 || off+2 > len(sub) {
		return nil, 0, false
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
		if compCount < 1 || compCount > maxLigatureComponents {
			continue
		}
		// Match the components against the glyphs after this one, skipping
		// those the lookup ignores.
		//
		// The positions are collected into an array on the stack and copied out
		// only if the whole ligature matched. Most candidates do not — a font
		// lists every ligature beginning with a glyph, and a run matches at most
		// one — so allocating per candidate is allocating for the failures.
		var found [maxLigatureComponents]int
		found[0] = at
		n := 1
		pos := at
		matched := true
		for k := 0; k < compCount-1; k++ {
			if 4+2*k+2 > len(lig) {
				matched = false
				break
			}
			want := font.Be16(lig, 4+2*k)
			pos = sh.nextNotIgnored(buf, pos+1, flags, want)
			if pos >= sh.end(buf) || buf[pos].GID != want {
				matched = false
				break
			}
			found[n] = pos
			n++
		}
		if matched {
			comps := make([]int, n)
			copy(comps, found[:n])
			return comps, font.Be16(lig, 0), true
		}
	}
	return nil, 0, false
}

// formLigature replaces the glyphs a ligature names with the ligature glyph,
// keeping everything the lookup stepped over.
//
// The skipped glyphs are moved to after the ligature, in the order they were
// in. They have to go somewhere — a mark cannot stay between two glyphs that
// are now one — and after is where OpenType puts them and where a mark
// attachment lookup will then find them, since it looks back for its base.
//
// All of it becomes one cluster. The ligature and the marks that were inside it
// came from a stretch of text that can no longer be divided: the accent belongs
// to a letter that is now half a glyph, so there is no position between them for
// a caret to sit at, and saying otherwise would let a caller offer one.
//
// It reports 1 consumed, not the span. The walk continues at the glyph after
// the ligature, which is the first thing that was kept — the next lookup gets to
// see it, and the walk always moves, so a font whose ligature product matches
// its own rule again cannot hold it in place.
func (sh shaper) formLigature(buf []Glyph, at, gid int, comps []int) (int, []Glyph) {
	last := comps[len(comps)-1]

	// The cluster is the earliest of everything the ligature spans, kept glyphs
	// included: it is where the indivisible stretch of text begins.
	cluster := buf[at].Cluster
	for i := at; i <= last; i++ {
		if buf[i].Cluster < cluster {
			cluster = buf[i].Cluster
		}
	}

	isComponent := make([]bool, last-at+1)
	for _, p := range comps {
		isComponent[p-at] = true
	}

	// A ligature of a letter and its own marks is not a ligature in the sense
	// that matters here. Nothing was joined that a mark could belong to *part*
	// of, so there is no component for a later mark to be placed against, and
	// giving it a number would only make the marks inside it look like they
	// belonged to something they do not.
	joined := false
	for _, p := range comps[1:] {
		if !sh.l.isMark(buf[p].GID) {
			joined = true
			break
		}
	}
	id, comps0 := 0, 1
	if joined {
		id = sh.nextLigatureID()
		for _, p := range comps {
			comps0 += componentsOf(buf[p])
		}
		comps0-- // the count started at one for the first component
	}

	out := make([]Glyph, 0, len(buf)-len(comps)+1)
	out = append(out, buf[:at]...)
	out = append(out, Glyph{
		GID: gid, Cluster: cluster, XAdvance: sh.f.advanceGID(gid),
		lig: ligatureRef{id: id, comps: comps0},
	})

	// Walking the components in order, so that each kept glyph is given the
	// part of the ligature it stood between. A mark before the second component
	// belongs to the first, and so on; a ligature made of ligatures counts each
	// of their parts, which is why the running total is of components rather
	// than of glyphs.
	kept, soFar := 0, componentsOf(buf[at])
	for i := at + 1; i <= last; i++ {
		if isComponent[i-at] {
			soFar += componentsOf(buf[i])
			continue
		}
		g := buf[i]
		g.Cluster = cluster
		if id != 0 {
			g.lig = ligatureRef{id: id, comp: soFar, comps: componentsOf(g)}
		}
		out = append(out, g)
		kept++
	}
	out = append(out, buf[last+1:]...)

	sh.resized(at, (1+kept)-(last-at+1))
	return 1, out
}

// nextNotIgnored is the next position a lookup with these flags looks at while
// matching its input.
//
// want is the glyph the caller is about to compare against. A join control
// standing in the way is stepped over — unless the lookup names that very glyph,
// in which case it is what the lookup was looking for and is matched rather than
// skipped. A face that declares a ligature over a joiner means it.
func (sh shaper) nextNotIgnored(buf []Glyph, from, flags, want int) int {
	end := sh.end(buf)
	for i := from; i < end; i++ {
		if sh.l.ignores(flags, buf[i].GID) {
			continue
		}
		if buf[i].GID != want && sh.stepsOverJoiner(i, false) {
			continue
		}
		return i
	}
	return end
}

// matchedPositions collects the positions a lookup with these flags would see
// as its input, starting at a position, up to n of them.
//
// Unlike the ligature walk above this cannot tell whether a joiner in the way is
// the glyph the rule wanted: the rule's items are compared by the caller, and
// may be classes rather than glyphs. A joiner the feature allows to be stepped
// over is therefore always stepped over here, never matched. It costs a font's
// contextual rule that names a joiner explicitly *and* is declared under a
// feature that steps over joiners, which is a combination that contradicts
// itself.
func (sh shaper) matchedPositions(buf []Glyph, at, n, flags int) ([]int, bool) {
	return sh.positionsFrom(buf, at, n, flags, false)
}

// lookaheadPositions is matchedPositions for the part of a rule that says what
// must *follow* what it replaces, which steps over joiners in every case.
func (sh shaper) lookaheadPositions(buf []Glyph, at, n, flags int) ([]int, bool) {
	return sh.positionsFrom(buf, at, n, flags, true)
}

func (sh shaper) positionsFrom(buf []Glyph, at, n, flags int, context bool) ([]int, bool) {
	out := make([]int, 0, n)
	end := sh.end(buf)
	pos := at
	for len(out) < n {
		if pos >= end {
			return nil, false
		}
		if !sh.l.ignores(flags, buf[pos].GID) && !sh.stepsOverJoiner(pos, context) {
			out = append(out, pos)
		}
		pos++
	}
	return out, true
}

// backtrackPositions collects the positions before a position, nearest first,
// which is the order the format stores a backtrack sequence in. It is context,
// so it steps over joiners.
func (sh shaper) backtrackPositions(buf []Glyph, before, n, flags int) ([]int, bool) {
	out := make([]int, 0, n)
	for pos := before - 1; pos >= sh.floor && len(out) < n; pos-- {
		if !sh.l.ignores(flags, buf[pos].GID) && !sh.stepsOverJoiner(pos, true) {
			out = append(out, pos)
		}
	}
	return out, len(out) == n
}

// sequenceContext matches a GSUB type 5 subtable and applies its lookups.
func (sh shaper) sequenceContext(sub []byte, buf []Glyph, at, flags, depth int) (int, []Glyph, bool) {
	if len(sub) < 4 {
		return 0, buf, false
	}
	switch font.Be16(sub, 0) {
	case 1:
		i, ok := coverageIndex(sub, font.Be16(sub, 2), buf[at].GID)
		if !ok {
			return 0, buf, false
		}
		return sh.contextRuleSet(sub, 6, i, buf, at, flags, depth, func(item, pos int) bool {
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
		return sh.contextRuleSet(sub, 8, classes[buf[at].GID], buf, at, flags, depth, func(item, pos int) bool {
			return classes[buf[pos].GID] == item
		})
	case 3:
		glyphCount := font.Be16(sub, 2)
		recCount := font.Be16(sub, 4)
		if glyphCount < 1 || 6+2*glyphCount > len(sub) {
			return 0, buf, false
		}
		positions, ok := sh.matchedPositions(buf, at, glyphCount, flags)
		if !ok {
			return 0, buf, false
		}
		for k := 0; k < glyphCount; k++ {
			if _, covered := coverageIndex(sub, font.Be16(sub, 6+2*k), buf[positions[k]].GID); !covered {
				return 0, buf, false
			}
		}
		return sh.runRecords(sub, 6+2*glyphCount, recCount, positions, buf, depth)
	}
	return 0, buf, false
}

// contextRuleSet walks the rule sets of a format 1 or 2 sequence context, whose
// only difference is whether a rule's items are glyphs or classes.
func (sh shaper) contextRuleSet(sub []byte, setsAt, index int, buf []Glyph, at, flags, depth int,
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
		positions, ok := sh.matchedPositions(buf, at, glyphCount, flags)
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
		return sh.runRecords(rule, 4+2*(glyphCount-1), recCount, positions, buf, depth)
	}
	return 0, buf, false
}

// chainedContext matches a GSUB type 6 subtable, which also constrains what
// comes before and after the part being replaced.
func (sh shaper) chainedContext(sub []byte, buf []Glyph, at, flags, depth int) (int, []Glyph, bool) {
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
		return sh.chainedRuleSet(sub, setsAt, index, buf, at, flags, depth,
			classes, backClasses, aheadClasses)
	case 3:
		return sh.chainedFormat3(sub, buf, at, flags, depth)
	}
	return 0, buf, false
}

// chainedRuleSet walks the rule sets of a chained context in glyph or class
// form. A rule states three sequences — what must precede, what is matched, and
// what must follow — and the first is stored nearest-first.
func (sh shaper) chainedRuleSet(sub []byte, setsAt, index int, buf []Glyph, at, flags, depth int,
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

		positions, ok := sh.matchedPositions(buf, at, inputCount, flags)
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
			bp, ok := sh.backtrackPositions(buf, at, len(back), flags)
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
			ap, ok := sh.lookaheadPositions(buf, positions[inputCount-1]+1, len(ahead), flags)
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
		return sh.runRecords(rule, p, recCount, positions, buf, depth)
	}
	return 0, buf, false
}

// chainedFormat3 is the coverage-based chained context, the form a modern font
// uses most: three lists of coverage tables rather than rule sets.
func (sh shaper) chainedFormat3(sub []byte, buf []Glyph, at, flags, depth int) (int, []Glyph, bool) {
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

	positions, ok := sh.matchedPositions(buf, at, len(input), flags)
	if !ok {
		return 0, buf, false
	}
	for k, cov := range input {
		if _, covered := coverageIndex(sub, cov, buf[positions[k]].GID); !covered {
			return 0, buf, false
		}
	}
	if len(back) > 0 {
		bp, ok := sh.backtrackPositions(buf, at, len(back), flags)
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
		ap, ok := sh.lookaheadPositions(buf, positions[len(input)-1]+1, len(ahead), flags)
		if !ok {
			return 0, buf, false
		}
		for k, cov := range ahead {
			if _, covered := coverageIndex(sub, cov, buf[ap[k]].GID); !covered {
				return 0, buf, false
			}
		}
	}
	return sh.runRecords(sub, p, recCount, positions, buf, depth)
}

// runRecords applies the lookups a matched rule names, each at the position it
// names, and reports how many glyphs of the buffer the rule accounted for.
//
// The positions are those of the *matched* glyphs, so a record naming index two
// means the third thing the rule matched — not the third glyph in the buffer,
// which may differ when the lookup skips marks.
//
// A rule may name several lookups, and one of them may change the buffer's
// length: a ligature makes it shorter, a decomposition longer. Every position
// after the one that changed then moves, and a later record aimed at a
// remembered index would land on the wrong glyph — or past the end. So the
// positions are carried forward rather than read once, which is the whole
// reason this takes the slice rather than the buffer offsets.
func (sh shaper) runRecords(base []byte, at, count int, positions []int, buf []Glyph, depth int) (int, []Glyph, bool) {
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
		target := positions[seqIndex]
		before := len(buf)
		_, out := sh.applyGSUBAt(lookupIndex, buf, target, depth+1)
		buf = out

		delta := len(buf) - before
		if delta == 0 {
			continue
		}
		for k := range positions {
			if positions[k] > target {
				positions[k] += delta
			}
		}
		// The rule now covers correspondingly more or fewer glyphs, which is
		// what tells the caller where to resume.
		if consumed += delta; consumed < 1 {
			consumed = 1
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

// componentsOf is how many parts of a ligature a glyph counts as: one for an
// ordinary glyph, and its own count for a ligature being joined again.
func componentsOf(g Glyph) int {
	if g.lig.comps > 1 {
		return g.lig.comps
	}
	return 1
}
