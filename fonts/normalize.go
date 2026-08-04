package fonts

import "sort"

// Normalisation: setting text that is written one way with a font whose rules
// are written the other.
//
// Unicode spells the same text in more than one way and says the spellings mean
// the same thing. "é" is one character or two; a nukta and a virama on the same
// consonant may be written in either order. A font does not agree: its rules name
// particular glyphs in a particular order, and text spelled the other way misses
// them — a letter drawn as .notdef where the font had the letter, a conjunct that
// quietly does not form, an accent that lands in the wrong place.
//
// So the characters are put into the form this face can best draw before any
// glyph is chosen. Three rounds, which is what every shaper does:
//
//  1. Decompose. Take each character apart into what it is written as, as far as
//     the face can draw the pieces.
//  2. Order. Sort each cluster's marks by combining class, which is the order
//     Unicode says is canonical and therefore the order a font's rules were
//     written against.
//  3. Compose. Put back together whatever the face has a single glyph for.
//
// # Why this is not NFC, and not NFD
//
// Either would be wrong here, because neither knows what the face has. NFC
// composes "a" and a ring into "å" whether or not the font can draw "å"; NFD
// takes "å" apart whether or not the font can draw a bare ring. What a shaper
// wants is the spelling this face draws best, so every step is conditional on the
// face's own coverage: compose where the face has the composed character,
// decompose where it does not. Two canonically equivalent strings still come out
// the same, which is the point — they just come out in whichever of the two forms
// this particular font can set.
//
// # The two modes
//
// A general run is short-circuited: a character the face already has is left
// alone, and only a cluster that carries marks is taken apart and put back
// together. That is cheap and right for Latin, Greek, Cyrillic and Arabic.
//
// An Indic run is not. Its characters are decomposed whether or not the face has
// them whole, because the Indic model is stated over the pieces: a consonant
// written with a nukta has to be a consonant and a nukta before the syllable can
// be read, and a vowel sign drawn on both sides of its letter cannot be placed at
// all while it is one character. indic.go's own splitting of those signs then has
// nothing left to do, which is the same answer reached one step earlier.
//
// # Clusters
//
// A Glyph.Cluster is the byte offset of the first character its glyph came from,
// and every step here keeps that true. The parts of a decomposition take the
// offset of the character they came out of; a composition takes the earliest
// offset of everything that went into it; a mark that moves past another drags
// the span it crossed down to the earliest offset in it. The offsets stay
// non-decreasing, which is what selection and hit-testing need.
//
// # What is not here
//
//   - HarfBuzz's fallbacks for a character the face cannot draw in any spelling:
//     setting an exotic space as an ordinary one, and U+2011 as U+2010. Those are
//     about drawing something rather than about equivalence, and this package
//     already has one answer for a character it cannot draw — .notdef, counted as
//     missing — which a caller can see and act on.
//   - Variation selectors. A face that states a variant through cmap format 14
//     is not asked; the selector is passed through as its own character, which is
//     what this package did before.
//   - The mark reordering Arabic wants on top of canonical order: a hamza or a
//     similar modifier written after a vowel is drawn before it, which canonical
//     order does not say and every shaper does anyway. Measured against HarfBuzz
//     on a corpus of Arabic bases with two and three marks, it is the whole of
//     the remaining 14%.

// maxCombiningMarks bounds the run of marks that will be sorted. The sort is
// quadratic, so a crafted string must not be able to turn a page of marks into
// work; and no writing system puts more than a few on one letter. It is the
// bound HarfBuzz uses, for the same reason.
const maxCombiningMarks = 32

// maxDecompositionDepth bounds the chain a character may be taken apart into.
// Unicode's deepest canonical chain is three steps; the table is generated, so
// this is a guard against a future one rather than against the data, and it
// truncates rather than recursing.
const maxDecompositionDepth = 8

// The Hangul blocks. Hangul's decomposition and composition are arithmetic
// rather than data — the standard states them as formulas — so they are computed
// here and are in neither generated table. cmd/gencanonical checks that the
// blocks are still where these say.
const (
	hangulSBase  = 0xAC00
	hangulLBase  = 0x1100
	hangulVBase  = 0x1161
	hangulTBase  = 0x11A7
	hangulLCount = 19
	hangulVCount = 21
	hangulTCount = 28
	hangulNCount = hangulVCount * hangulTCount
	hangulSCount = hangulLCount * hangulNCount
)

// charClassOf reports a character's canonical combining class and whether it is
// a combining mark. A character the table does not name is an unmarked starter,
// which is most of the code space.
func charClassOf(r rune) (uint8, bool) {
	if r < charClasses[0].lo || r > charClasses[len(charClasses)-1].hi {
		return 0, false
	}
	i := sort.Search(len(charClasses), func(i int) bool { return charClasses[i].hi >= r })
	if i < len(charClasses) && r >= charClasses[i].lo {
		return charClasses[i].ccc, charClasses[i].mark
	}
	return 0, false
}

// isCombiningMark reports whether a character is a combining mark: Unicode's
// general category Mn, Mc or Me.
//
// This, and not the combining class, is what cuts a run into clusters. A
// Devanagari vowel sign is a spacing mark whose combining class is zero, and it
// belongs to the letter before it as much as an acute accent does.
func isCombiningMark(r rune) bool {
	_, mark := charClassOf(r)
	return mark
}

// reorderClasses permutes the combining classes whose numeric order is not the
// order the marks are drawn in.
//
// This is a deliberate departure from Unicode's own numbering, and it is the one
// every shaper makes. Unicode's classes 10 to 33 are *fixed positions* rather
// than a drawing order: they say where a Hebrew point or an Arabic vowel sits on
// the letter, and sorting by the number puts them in an order no scribe would
// write. Telugu's two length marks are the only vowel signs in the Indic blocks
// with a non-zero class at all, and theirs is above the virama's, so canonical
// order would sort a syllable's vowel behind its virama. Thai's sara u sorts
// after phinthu for the same reason, and Tibetan's vowel signs come out in an
// order that breaks Dzongkha's shortcuts.
//
// An entry of zero means the class is used as Unicode numbers it, which is true
// of every class this does not name. The table stops at the highest class it
// changes; everything above is unchanged.
var reorderClasses = [...]uint8{
	// Hebrew, permuted into the order the SBL manual sets the points in.
	10: 22, 11: 15, 12: 16, 13: 17, 14: 23, 15: 18, 16: 19, 17: 20, 18: 21,
	19: 14, 20: 24, 21: 12, 22: 25, 23: 13, 24: 10, 25: 11, 26: 26,
	// Arabic, moving shadda (33) before the vowels it is written with.
	27: 28, 28: 29, 29: 30, 30: 31, 31: 32, 32: 33, 33: 27,
	// Telugu's length marks, below the virama's class rather than above it.
	84: 4, 91: 5,
	// Thai's sara u and sara uu, before phinthu rather than after.
	103: 3,
	// Tibetan's vowel signs i and u, so that u comes first.
	130: 132, 132: 131,
}

// reorderClass is the class a mark is ordered by: its combining class, with the
// permutation above applied.
func reorderClass(r rune) uint8 {
	switch r {
	// Three characters whose class says nothing about where they go. The sakot
	// and the padma must come after any mark they are written with, and the
	// tsa-phru before U+0F74 rather than after it.
	case 0x1A60, 0x0FC6:
		return 254
	case 0x0F39:
		return 127
	}
	ccc, _ := charClassOf(r)
	if int(ccc) < len(reorderClasses) && reorderClasses[ccc] != 0 {
		return reorderClasses[ccc]
	}
	return ccc
}

// canonicalDecompose reports what a character is written as: one or two
// characters, with a zero second where it is one.
//
// It is a single step, as Unicode states it, so a caller that wants the whole
// decomposition applies it again to the first part — which is what decomposeInto
// does, and what lets it stop as soon as the face can draw what it has.
func canonicalDecompose(r rune) (a, b rune, ok bool) {
	if r >= hangulSBase && r < hangulSBase+hangulSCount {
		i := r - hangulSBase
		if i%hangulTCount != 0 {
			// A syllable with a trailing consonant comes apart into the
			// syllable without it and that consonant, not into three jamo.
			return hangulSBase + (i/hangulTCount)*hangulTCount, hangulTBase + i%hangulTCount, true
		}
		return hangulLBase + i/hangulNCount, hangulVBase + (i%hangulNCount)/hangulTCount, true
	}
	if r < canonicalDecompositions[0].r || r > canonicalDecompositions[len(canonicalDecompositions)-1].r {
		return 0, 0, false
	}
	i := sort.Search(len(canonicalDecompositions), func(i int) bool {
		return canonicalDecompositions[i].r >= r
	})
	if i >= len(canonicalDecompositions) || canonicalDecompositions[i].r != r {
		return 0, 0, false
	}
	d := canonicalDecompositions[i]
	return d.a, d.b, true
}

// canonicalCompose reports the character a pair is written as, where Unicode
// allows the pair to be composed at all.
func canonicalCompose(a, b rune) (rune, bool) {
	// A leading and a vowel jamo make a syllable; that syllable and a trailing
	// jamo make another. U+11A7 is the filler that means "no trailing
	// consonant", so it composes with nothing.
	if a >= hangulLBase && a < hangulLBase+hangulLCount &&
		b >= hangulVBase && b < hangulVBase+hangulVCount {
		return hangulSBase + ((a-hangulLBase)*hangulVCount+(b-hangulVBase))*hangulTCount, true
	}
	if a >= hangulSBase && a < hangulSBase+hangulSCount && (a-hangulSBase)%hangulTCount == 0 &&
		b > hangulTBase && b < hangulTBase+hangulTCount {
		return a + (b - hangulTBase), true
	}
	i := sort.Search(len(canonicalCompositions), func(i int) bool {
		c := canonicalCompositions[i]
		return c.a > a || (c.a == a && c.b >= b)
	})
	if i >= len(canonicalCompositions) ||
		canonicalCompositions[i].a != a || canonicalCompositions[i].b != b {
		return 0, false
	}
	return canonicalCompositions[i].ab, true
}

// hasGlyph reports whether the face can draw a character as itself.
func (f *Face) hasGlyph(r rune) bool {
	_, ok := f.GlyphID(r)
	return ok
}

// normalize puts a run into the spelling this face draws best, with each
// cluster's marks in canonical order.
//
// It returns the slices it was given when nothing can change, which is the
// common case and saves both the copy and the walk — see needsNormalizing.
//
// indic says the run is one of the nine scripts that share the Indic model, in
// which case the run is fully decomposed rather than short-circuited, and the
// two places that model disagrees with Unicode's own tables are honoured.
func (f *Face) normalize(runes []rune, offsets []int, indic bool) ([]rune, []int) {
	if !f.needsNormalizing(runes, !indic) {
		return runes, offsets
	}
	n := normalizer{
		f:        f,
		indic:    indic,
		shortest: !indic,
		out:      make([]rune, 0, len(runes)+4),
		off:      make([]int, 0, len(runes)+4),
	}
	if n.decomposeRound(runes, offsets) {
		// Nothing in the run was a base with marks on it, so there is no cluster
		// to order and nothing that could compose: rounds two and three would
		// walk the buffer to no purpose.
		return n.out, n.off
	}
	n.reorderRound()
	return n.composeRound()
}

// needsNormalizing reports whether normalisation could change a run at all.
//
// It is a shortcut, and it is worth having for the same reason the bidirectional
// one is: this question is asked of every string the pipeline sets, and almost
// every answer is no. Nothing below U+00C0 is a combining mark or has a canonical
// decomposition, so a string of ASCII — which is most of what is set — costs one
// comparison per character and no allocation at all.
//
// The shortcut is sound because each round can only change something it has
// something to work on. Without a mark anywhere, every cluster is simple: round
// one leaves each character alone if it is short-circuited or has no
// decomposition to take, and rounds two and three do not run. What is left is the
// one case a mark-free run can still change — a character the face cannot draw
// but whose pieces it can — and that is what the coverage test below is for.
//
// normalize_test.go checks the shortcut against the full algorithm, because a
// shortcut that disagrees with what it is short-cutting is worse than none.
func (f *Face) needsNormalizing(runes []rune, shortest bool) bool {
	// The lowest character either table has anything to say about, hoisted out
	// of the loop: this is the comparison every character of ordinary text is
	// answered by, and it is the whole cost of the shortcut.
	plain := charClasses[0].lo
	if canonicalDecompositions[0].r < plain {
		plain = canonicalDecompositions[0].r
	}
	for _, r := range runes {
		if r < plain {
			continue
		}
		if isCombiningMark(r) {
			return true
		}
		if _, _, ok := canonicalDecompose(r); !ok {
			continue
		}
		if !shortest {
			// An Indic run is taken apart whether or not the face has the
			// character whole, so having a decomposition at all is enough.
			return true
		}
		if !f.hasGlyph(r) {
			return true
		}
	}
	return false
}

// normalizer is one run being normalised: the face whose coverage every decision
// turns on, and the buffer being built.
type normalizer struct {
	f *Face
	// shortest says a character the face already has is emitted as it is rather
	// than taken apart. It is the general path's setting and not the Indic one.
	shortest bool
	// indic says the two disagreements between the Indic model and Unicode's own
	// tables apply — see decompose and compose below.
	indic bool
	out   []rune
	off   []int
}

func (n *normalizer) emit(r rune, cluster int) {
	n.out = append(n.out, r)
	n.off = append(n.off, cluster)
}

// decomposeRound is the first round: every character taken apart as far as the
// face can draw the pieces. It reports whether the run turned out to be nothing
// but simple clusters, in which case there is no ordering or composing to do.
func (n *normalizer) decomposeRound(runes []rune, offsets []int) bool {
	allSimple := true
	i := 0
	for i < len(runes) {
		// Up to the next mark the characters stand alone. The one immediately
		// before a mark does not — it is that cluster's base — so it is left to
		// the cluster below, which is what makes the base and its marks come
		// apart together.
		end := i + 1
		for end < len(runes) && !isCombiningMark(runes[end]) {
			end++
		}
		if end < len(runes) {
			end--
		}
		for i < end {
			i = n.step(runes, offsets, i, n.shortest)
		}
		if i == len(runes) {
			break
		}
		allSimple = false
		// A base and the marks that follow it: one cluster, taken apart whole
		// rather than short-circuited, so that its marks can be ordered and then
		// put back together.
		end = i + 1
		for end < len(runes) && isCombiningMark(runes[end]) {
			end++
		}
		for i < end {
			i = n.step(runes, offsets, i, false)
		}
	}
	return allSimple
}

// step emits one character of the input, decomposed or not, and returns the
// position after it.
func (n *normalizer) step(runes []rune, offsets []int, i int, shortest bool) int {
	u, cluster := runes[i], offsets[i]
	if shortest && n.f.hasGlyph(u) {
		n.emit(u, cluster)
		return i + 1
	}
	if n.decomposeInto(u, cluster, shortest, 0) {
		return i + 1
	}
	// Nothing came apart, so the character is set as it was written — whether or
	// not the face has it. What a face that has not is drawn as is decided
	// elsewhere: shapeGlyphsIn substitutes .notdef and counts the character
	// missing, which is an answer a caller can see.
	n.emit(u, cluster)
	return i + 1
}

// decomposeInto emits a character as the pieces it is written as, and reports
// whether it did.
//
// It emits nothing at all unless the whole decomposition is drawable, which is
// the rule that keeps this from making things worse: replacing one character the
// face lacks with two it also lacks trades one .notdef for two.
func (n *normalizer) decomposeInto(ab rune, cluster int, shortest bool, depth int) bool {
	if depth >= maxDecompositionDepth {
		return false
	}
	a, b, ok := n.decompose(ab)
	if !ok {
		return false
	}
	// The second part is never taken apart further — Unicode's decompositions
	// are written so that only the first part ever needs it — so the face has to
	// be able to draw it as it is.
	if b != 0 && !n.f.hasGlyph(b) {
		return false
	}
	hasA := n.f.hasGlyph(a)
	emit := func() bool {
		n.emit(a, cluster)
		if b != 0 {
			n.emit(b, cluster)
		}
		return true
	}
	if shortest && hasA {
		return emit()
	}
	if n.decomposeInto(a, cluster, shortest, depth+1) {
		if b != 0 {
			n.emit(b, cluster)
		}
		return true
	}
	if hasA {
		return emit()
	}
	return false
}

// decompose is Unicode's canonical decomposition, less what the Indic model
// keeps whole.
//
// Four letters have a canonical decomposition Unicode states and the Indic
// shaping model does not want: they are letters in their own right in the
// languages that write them, with their own conjuncts, and taking them apart
// makes the shaping rules for the letter they decompose to apply to a letter that
// is not it.
//
// A vowel sign drawn as several marks is left whole here and taken apart by
// indic.go, which is where it was taken apart before this file existed. The order
// is what matters and is not a preference: a sequence that spells a vowel nobody
// writes is shown against a dotted circle, and the list of those sequences is
// written against the sign as it is *spelled*. Splitting it first would hide the
// sign the check is looking for and put a dotted circle into text that reads
// perfectly well — measured, on 23 Malayalam sequences.
func (n *normalizer) decompose(ab rune) (a, b rune, ok bool) {
	if n.indic {
		switch ab {
		case 0x0931, // DEVANAGARI LETTER RRA
			0x09DC, // BENGALI LETTER RRA
			0x09DD, // BENGALI LETTER RHA
			0x0B94: // TAMIL LETTER AU
			return 0, 0, false
		}
		if _, split := indicSplitMatraOf(ab); split {
			return 0, 0, false
		}
	}
	return canonicalDecompose(ab)
}

// compose is Unicode's canonical composition, less what the Indic model keeps
// apart and plus the one thing it wants back.
func (n *normalizer) compose(a, b rune) (rune, bool) {
	if n.indic {
		// A vowel sign that was split into the marks it is drawn as must not be
		// put back together: its parts are on different sides of the letter, and
		// the model cannot place it whole.
		if _, mark := charClassOf(a); mark {
			return 0, false
		}
		// BENGALI LETTER YYA is a composition exclusion, so Unicode will not
		// compose it — but a Bengali font draws it as one letter, and every
		// shaper puts it back.
		if a == 0x09AF && b == 0x09BC {
			return 0x09DF, true
		}
	}
	return canonicalCompose(a, b)
}

// reorderRound is the second round: each run of marks put into canonical order.
func (n *normalizer) reorderRound() {
	for i := 0; i < len(n.out); i++ {
		if reorderClass(n.out[i]) == 0 {
			continue
		}
		end := i + 1
		for end < len(n.out) && reorderClass(n.out[end]) != 0 {
			end++
		}
		if end-i <= maxCombiningMarks {
			n.sortMarks(i, end)
		}
		// The character at end has class zero, so it starts no run of its own.
		i = end
	}
}

// sortMarks puts one run of marks into canonical order.
//
// The sort is by insertion and is stable, which the standard requires: two marks
// of the same class are canonically equivalent in either order only because
// neither may be moved past the other.
func (n *normalizer) sortMarks(start, end int) {
	for i := start + 1; i < end; i++ {
		j := i
		for j > start && reorderClass(n.out[j-1]) > reorderClass(n.out[i]) {
			j--
		}
		if j == i {
			continue
		}
		// Once a mark has moved past another, the glyphs of this span no longer
		// stand one for one for its characters in order, so the whole span takes
		// the earliest character in it. That is what a cluster is, and it keeps
		// the offsets non-decreasing.
		lo := n.off[j]
		for k := j + 1; k <= i; k++ {
			if n.off[k] < lo {
				lo = n.off[k]
			}
		}
		r := n.out[i]
		copy(n.out[j+1:i+1], n.out[j:i])
		n.out[j] = r
		for k := j; k <= i; k++ {
			n.off[k] = lo
		}
	}
}

// composeRound is the third round: every mark put back onto its starter where
// the face has the two as one glyph.
//
// It writes over the buffer it reads, which is safe because composing only ever
// shortens it: the output is never further along than the input.
func (n *normalizer) composeRound() ([]rune, []int) {
	if len(n.out) == 0 {
		// Unreachable as this is called — the round before it emits at least one
		// character whenever it reports a cluster — and here so that moving the
		// call cannot turn into a panic on a crafted string.
		return n.out, n.off
	}
	res, resOff := n.out[:1], n.off[:1]
	starter := 0
	for k := 1; k < len(n.out); k++ {
		r := n.out[k]
		// A character that is not a mark is not composed onto the starter before
		// it. That is not only a shortcut: Hangul fonts are drawn either as
		// syllables or as jamo, and mixing the two is worse than either.
		if _, mark := charClassOf(r); mark &&
			// Nothing between this mark and its starter, or what is between is
			// drawn closer in than this — otherwise this mark is blocked, and
			// composing past it would move it through a mark it is written
			// outside of.
			(starter == len(res)-1 || reorderClass(res[len(res)-1]) < reorderClass(r)) {
			if ab, ok := n.compose(res[starter], r); ok && n.f.hasGlyph(ab) {
				res[starter] = ab
				lo := n.off[k]
				for m := starter; m < len(resOff); m++ {
					if resOff[m] < lo {
						lo = resOff[m]
					}
				}
				for m := starter; m < len(resOff); m++ {
					resOff[m] = lo
				}
				continue
			}
		}
		res = append(res, r)
		resOff = append(resOff, n.off[k])
		if reorderClass(r) == 0 {
			starter = len(res) - 1
		}
	}
	return res, resOff
}
