package fonts

// What the syllabic shapers share.
//
// Three scripts in this package are not set in the order their characters are
// stored: Devanagari and its relatives (indic.go), Khmer (khmer.go) and Myanmar
// (myanmar.go). They are three models, not one — each has its own categories,
// its own syllable grammar and its own reordering, and the OpenType script
// development specifications state them separately because they *are* separate.
//
// What they do share is the machinery underneath: a per-glyph record kept in
// step with a buffer that substitutions are reshaping, features applied to one
// syllable at a time so that a ligature cannot join two of them, and the
// placeholder shown for a syllable with nothing to hang off. That is what is
// here, together with the one decision that has to be taken before any of them
// runs: which of the three, if any, a run belongs to.

// usesSyllabicShaper reports whether a script is set by one of the shapers that
// segments text into syllables and reorders them.
//
// It exists so that the question is asked in one place and answered the same
// way everywhere. Normalisation needs it as much as shaping does: a syllabic
// shaper's rules are written against fully decomposed text — a base, then its
// marks in canonical order — so a run bound for one must not be left composed,
// and the short-circuit that is right for Latin is wrong here.
//
// Asking "is this Indic" instead was correct while Indic was the only such
// shaper, and became silently wrong the moment Khmer and Myanmar joined it.
// That is the drift a predicate with one home prevents, and it is why both the
// dispatch below and the normalisation pass ask this rather than each deciding
// for itself.
func usesSyllabicShaper(script uint16) bool {
	return indicConfigFor(script) != nil || isKhmerScript(script) || isMyanmarScript(script)
}

// shapeSyllabic shapes a run by whichever syllabic model its script belongs to,
// reporting false for a script that belongs to none.
//
// It is the whole of the substitution pass for a run it handles: the reordering
// decides which of the font's rules apply where, so it cannot be a step before
// the general substitutions and has to be them.
func (sh shaper) shapeSyllabic(buf []Glyph, runes []rune, script uint16) ([]Glyph, bool) {
	if !usesSyllabicShaper(script) {
		return buf, false
	}
	if cfg := indicConfigFor(script); cfg != nil {
		return sh.shapeIndic(buf, runes, sh.indicPlan(cfg, sh.f.indicOldSpec(cfg, script))), true
	}
	if isKhmerScript(script) {
		return sh.shapeKhmer(buf, runes), true
	}
	if isMyanmarScript(script) {
		return sh.shapeMyanmar(buf, runes), true
	}
	return buf, false
}

// scriptSelects reports whether a script's OpenType tags include the given one.
//
// A script is identified by the tag a font declares its rules under rather than
// by an index into the generated table, for the same reason indicConfigFor does
// it that way: the tag is what the font and the shaper have in common.
func scriptSelects(script uint16, tag string) bool {
	for _, t := range scriptTags(script) {
		if t == tag {
			return true
		}
	}
	return false
}

// splitCharacters replaces each character that is drawn as several marks by the
// marks it is drawn as, as reported by of.
//
// The parts of such a sign go to different places — one before the letter and
// one after — so there is no single place the sign itself could be given, and
// taking it apart has to happen before anything is placed.
//
// A sign is only taken apart when the face has a glyph for every part. A face
// that draws the sign whole and has no glyph for one of its halves would
// otherwise lose that half altogether, which is worse than drawing the sign
// where the model would rather it were not.
func (sh shaper) splitCharacters(buf []Glyph, runes []rune, of func(rune) ([]rune, bool)) ([]Glyph, []rune) {
	outBuf := make([]Glyph, 0, len(buf))
	outRunes := make([]rune, 0, len(runes))
	for i, r := range runes {
		parts, ok := of(r)
		if !ok {
			outBuf = append(outBuf, buf[i])
			outRunes = append(outRunes, r)
			continue
		}
		gids := make([]int, 0, len(parts))
		for _, p := range parts {
			gid, have := sh.f.GlyphID(p)
			if !have {
				gids = nil
				break
			}
			gids = append(gids, gid)
		}
		if gids == nil {
			outBuf = append(outBuf, buf[i])
			outRunes = append(outRunes, r)
			continue
		}
		// The parts share the sign's cluster: several glyphs standing for one
		// character is exactly what a cluster records.
		for k, gid := range gids {
			outBuf = append(outBuf, Glyph{
				GID: gid, Cluster: buf[i].Cluster, XAdvance: sh.f.advanceGID(gid),
			})
			outRunes = append(outRunes, parts[k])
		}
	}
	return outBuf, outRunes
}

// insertGlyphAt puts one glyph, and the record that describes it, into a buffer
// at a position. It is how a placeholder reaches a syllable that has nothing of
// its own to hang off.
//
// The glyph takes the cluster of what it is inserted before, so that it maps
// back to the character whose mark it is standing in for.
func (sh shaper) insertGlyphAt(buf []Glyph, info []indicInfo, at, gid int, what indicInfo) ([]Glyph, []indicInfo) {
	cluster := 0
	switch {
	case at < len(buf):
		cluster = buf[at].Cluster
	case len(buf) > 0:
		cluster = buf[len(buf)-1].Cluster
	}
	g := Glyph{GID: gid, Cluster: cluster, XAdvance: sh.f.advanceGID(gid)}

	buf = append(buf, Glyph{})
	copy(buf[at+1:], buf[at:])
	buf[at] = g

	info = append(info, indicInfo{})
	copy(info[at+1:], info[at:])
	info[at] = what
	return buf, info
}

// oneCluster gives every glyph of a syllable the cluster of its first
// character.
//
// It has to: once the glyphs are in drawing order they no longer correspond
// one-for-one to the characters, and a syllable is the smallest piece of these
// scripts that can honestly be mapped back to a position in the text.
func oneCluster(buf []Glyph, start, end int) {
	if start >= end {
		return
	}
	cluster := buf[start].Cluster
	for i := start; i < end; i++ {
		if buf[i].Cluster < cluster {
			cluster = buf[i].Cluster
		}
	}
	for i := start; i < end; i++ {
		buf[i].Cluster = cluster
	}
}

// moveGlyphToFront moves the glyph at from to at, shifting what lies between
// forward by one. It is the mirror of rotateIndicLeft, and is what the Khmer
// reordering is made of: a pre-base vowel sign and a subscript Ro are both
// drawn at the front of the syllable whatever stands between.
func moveGlyphToFront(buf []Glyph, info []indicInfo, at, from int) {
	if from <= at || from >= len(buf) || from >= len(info) || at < 0 {
		return
	}
	g, f := buf[from], info[from]
	copy(buf[at+1:from+1], buf[at:from])
	copy(info[at+1:from+1], info[at:from])
	buf[at], info[at] = g, f
}
