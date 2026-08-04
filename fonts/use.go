package fonts

import "sort"

// The Universal Shaping Engine.
//
// Several dozen scripts are written the way the Indic ones are — a syllable
// stored in one order and drawn in another — without each having a shaper
// written for it. Javanese, Balinese, Buginese, Chakma, Tai Tham, Tibetan and
// the rest are covered by one model instead, and this is it.
//
// It is the same shape of work as indic.go, khmer.go and myanmar.go, and shares
// their machinery (syllabic.go). What it does not share is their per-script
// knowledge: those three encode what a Devanagari or Khmer or Myanmar syllable
// is, character by character. This one is told, by a table derived from
// Unicode's own properties, and so covers a script it has never heard of.

// useCategory is what a character is, as far as the syllable model is
// concerned.
//
// The names are the specification's. They are terse because they appear in the
// syllable grammar, where a longer name would obscure the shape of the rule
// rather than explain it.
type useCategory uint8

const (
	useO    useCategory = iota // other: not part of a syllable
	useB                       // base: a letter a syllable is built on
	useN                       // a Brahmi joining number
	useGB                      // a placeholder a mark can hang off
	useCGJ                     // transparent: the grapheme joiner, ZWJ, variation selectors
	useF                       // a final consonant
	useFM                      // a final modifier
	useM                       // a medial consonant
	useCM                      // a modifier: nukta, gemination, consonant killer
	useSUB                     // a subjoined consonant
	useCS                      // a consonant that stacks what follows
	useH                       // halant: kills the vowel and joins what follows
	useHVM                     // halant or vowel modifier, which Sinhala's is both of
	useHN                      // a number joiner
	useIS                      // an invisible stacker
	useZWNJ                    // the zero width non-joiner
	useRK                      // a reordering killer
	useR                       // a repha, drawn at the end of the syllable
	useSk                      // the sakot, which joins across a syllable break
	useSM                      // a symbol modifier
	useV                       // a dependent vowel
	useVM                      // a vowel modifier
	useWJ                      // a word joiner, and the ignorable code points reserved for one
)

// usePosition is which side of the letter a mark is drawn, for the categories
// that have a side. It is what decides where a glyph is moved to.
type usePosition uint8

const (
	usePosNone usePosition = iota
	usePosPre              // before the letter
	usePosAbv              // above it
	usePosBlw              // below it
	usePosPst              // after it
)

// useRange is one run of characters the table gives the same answer for.
type useRange struct {
	lo, hi rune
	cat    useCategory
	pos    usePosition
}

// useCategoryOf reports what a character is and where its mark sits.
//
// The ranges are sorted and searched, and the first is above almost every
// character of ordinary text — so a Latin string is answered by one comparison
// per character.
func useCategoryOf(r rune) (useCategory, usePosition) {
	if r < useRanges[0].lo {
		return useO, usePosNone
	}
	lo, hi := 0, len(useRanges)-1
	for lo <= hi {
		mid := (lo + hi) / 2
		switch {
		case r < useRanges[mid].lo:
			hi = mid - 1
		case r > useRanges[mid].hi:
			lo = mid + 1
		default:
			return useRanges[mid].cat, useRanges[mid].pos
		}
	}
	return useO, usePosNone
}

// useInfo is what the model knows about one glyph, kept in step with the buffer
// as substitution reshapes it.
type useInfo struct {
	cat useCategory
	pos usePosition
	// ligated says the glyph is what a substitution made of several. It matters
	// for one question only: a halant the font has ligated away is no longer a
	// halant, so it neither stops a repha nor moves the place a pre-base vowel
	// is sent to.
	ligated bool
}

// useClusterKind is which of the grammar's productions matched a cluster.
//
// It is not bookkeeping. Two things are decided by it: only some kinds have
// their glyphs moved, and exactly one kind — the broken one — is what a dotted
// circle is inserted for.
type useClusterKind uint8

const (
	useNonCluster              useClusterKind = iota // a character in no cluster
	useViramaTerminatedCluster                       // ends in a stacker or a reordering killer
	useSakotTerminatedCluster                        // ends in the sakot
	useStandardCluster                               // a base and what hangs off it
	useNumberJoinerTerminatedCluster
	useNumeralCluster
	useSymbolCluster // a symbol, or anything else, carrying marks
	useBrokenCluster // marks with no base: malformed text
)

// reorders reports whether a cluster of this kind has its glyphs moved.
//
// A numeral cluster and a character that is in no cluster do not: there is
// nothing in either that is written on one side of a letter and drawn on the
// other, which is the only thing the reordering is for.
func (k useClusterKind) reorders() bool {
	switch k {
	case useViramaTerminatedCluster, useSakotTerminatedCluster, useStandardCluster,
		useSymbolCluster, useBrokenCluster:
		return true
	}
	return false
}

// useCluster is one cluster: the stretch of glyphs that are drawn together and
// that a font's rules are applied to as a unit, and what it was matched as.
type useCluster struct {
	start, end int
	kind       useClusterKind
}

// useGrammar matches the engine's cluster grammar.
//
// # Why a grammar and not a scan
//
// What stood here before asked, character by character, "can this continue the
// cluster?" — which answers where a cluster ends and cannot answer whether what
// it found is a cluster at all. Two things need that second answer. A run of
// marks with no letter to hang off is *broken*, and a reader is told so by a
// dotted circle; nothing else in the text says the text is malformed. And a
// character the model calls Other is not a gap between clusters but the start of
// one, so a symbol written between a letter and its vowel takes the vowel with
// it rather than leaving it on the letter.
//
// # What it is
//
// The specification states the grammar as a set of productions and HarfBuzz
// states it as a Ragel scanner; the two agree, and what is written below is one
// method per production, each returning how far it reached. A scanner takes the
// *longest* match and breaks a tie in favour of the alternative stated first,
// so the alternatives are tried from the same place and compared, rather than
// the first that matches being taken.
type useGrammar struct {
	info []useInfo
	// idx maps a position in the grammar's input to a position in the run. Two
	// kinds of character are not in it at all — see useGrammarInput.
	idx []int
}

// useGrammarInput is the run as the grammar sees it, which is not all of it.
//
// The grapheme joiner and its relatives "may occur anywhere in a cluster with no
// effect", so they are not in the grammar and must not be matched against it. A
// zero width non-joiner is in the grammar, but only where it does something:
// it continues the cluster before it and breaks the one after, and it does that
// only when what follows is not a mark. Where a mark follows, it is invisible
// here for the same reason the joiner is.
func useGrammarInput(info []useInfo, runes []rune) []int {
	idx := make([]int, 0, len(info))
	for i := range info {
		switch info[i].cat {
		case useCGJ:
			continue
		case useZWNJ:
			if next := nextUseVisible(info, runes, i+1); next >= 0 && isCombiningMark(runes[next]) {
				continue
			}
		}
		idx = append(idx, i)
	}
	return idx
}

// nextUseVisible is the first character at or after i that the grammar can see,
// or -1 if there is none.
func nextUseVisible(info []useInfo, runes []rune, i int) int {
	for ; i < len(info) && i < len(runes); i++ {
		if info[i].cat != useCGJ {
			return i
		}
	}
	return -1
}

func (g *useGrammar) is(i int, c useCategory) bool {
	return i >= 0 && i < len(g.idx) && g.info[g.idx[i]].cat == c
}

func (g *useGrammar) isAt(i int, c useCategory, p usePosition) bool {
	return g.is(i, c) && g.info[g.idx[i]].pos == p
}

// star matches zero or more, opt zero or one, of one positioned category.
func (g *useGrammar) star(i int, c useCategory, p usePosition) int {
	for g.isAt(i, c, p) {
		i++
	}
	return i
}

func (g *useGrammar) opt(i int, c useCategory, p usePosition) int {
	if g.isAt(i, c, p) {
		return i + 1
	}
	return i
}

// h = H | HVM | IS | Sk
func (g *useGrammar) stacker(i int) bool {
	return g.is(i, useH) || g.is(i, useHVM) || g.is(i, useIS) || g.is(i, useSk)
}

// consonant_modifiers = CMAbv* CMBlw* ((h B | SUB) CMAbv* CMBlw*)*
func (g *useGrammar) consonantModifiers(i int) int {
	i = g.star(g.star(i, useCM, usePosAbv), useCM, usePosBlw)
	for {
		switch {
		case g.stacker(i) && g.is(i+1, useB):
			i += 2
		case g.is(i, useSUB):
			i++
		default:
			return i
		}
		i = g.star(g.star(i, useCM, usePosAbv), useCM, usePosBlw)
	}
}

// medial_consonants = MPre? MAbv? MBlw? MPst?
func (g *useGrammar) medialConsonants(i int) int {
	i = g.opt(i, useM, usePosPre)
	i = g.opt(i, useM, usePosAbv)
	i = g.opt(i, useM, usePosBlw)
	return g.opt(i, useM, usePosPst)
}

// dependent_vowels = VPre* VAbv* VBlw* VPst* | H
//
// The second alternative is what makes a letter written with a bare virama a
// *standard* cluster rather than a virama-terminated one, which is reserved for
// the invisible stacker and the reordering killer.
func (g *useGrammar) dependentVowels(i int) int {
	j := g.star(i, useV, usePosPre)
	j = g.star(j, useV, usePosAbv)
	j = g.star(j, useV, usePosBlw)
	j = g.star(j, useV, usePosPst)
	if j == i && g.is(i, useH) {
		return i + 1
	}
	return j
}

// vowel_modifiers = HVM? VMPre* VMAbv* VMBlw* VMPst*
func (g *useGrammar) vowelModifiers(i int) int {
	if g.is(i, useHVM) {
		i++
	}
	i = g.star(i, useVM, usePosPre)
	i = g.star(i, useVM, usePosAbv)
	i = g.star(i, useVM, usePosBlw)
	return g.star(i, useVM, usePosPst)
}

// final_consonants = FAbv* FBlw* FPst*
func (g *useGrammar) finalConsonants(i int) int {
	i = g.star(i, useF, usePosAbv)
	i = g.star(i, useF, usePosBlw)
	return g.star(i, useF, usePosPst)
}

// final_modifiers = FMAbv* FMBlw* | FMPst?
func (g *useGrammar) finalModifiers(i int) int {
	if j := g.star(g.star(i, useFM, usePosAbv), useFM, usePosBlw); j > i {
		return j
	}
	return g.opt(i, useFM, usePosPst)
}

// complex_syllable_start = (R | CS)? (B | GB)
func (g *useGrammar) complexSyllableStart(i int) int {
	if g.is(i, useR) || g.is(i, useCS) {
		i++
	}
	if g.is(i, useB) || g.is(i, useGB) {
		return i + 1
	}
	return -1
}

// complex_syllable_middle = consonant_modifiers medial_consonants
//
//	dependent_vowels vowel_modifiers (Sk B)*
func (g *useGrammar) complexSyllableMiddle(i int) int {
	i = g.consonantModifiers(i)
	i = g.medialConsonants(i)
	i = g.dependentVowels(i)
	i = g.vowelModifiers(i)
	for g.is(i, useSk) && g.is(i+1, useB) {
		i += 2
	}
	return i
}

// complex_syllable_tail = complex_syllable_middle final_consonants final_modifiers
func (g *useGrammar) complexSyllableTail(i int) int {
	return g.finalModifiers(g.finalConsonants(g.complexSyllableMiddle(i)))
}

// virama_terminated_cluster_tail = consonant_modifiers (IS | RK)
func (g *useGrammar) viramaTerminatedTail(i int) int {
	i = g.consonantModifiers(i)
	if g.is(i, useIS) || g.is(i, useRK) {
		return i + 1
	}
	return -1
}

// sakot_terminated_cluster_tail = complex_syllable_middle Sk
func (g *useGrammar) sakotTerminatedTail(i int) int {
	if i = g.complexSyllableMiddle(i); g.is(i, useSk) {
		return i + 1
	}
	return -1
}

// number_joiner_terminated_cluster_tail = (HN N)* HN
func (g *useGrammar) numberJoinerTail(i int) int {
	for g.is(i, useHN) && g.is(i+1, useN) {
		i += 2
	}
	if g.is(i, useHN) {
		return i + 1
	}
	return -1
}

// numeral_cluster_tail = (HN N)+
func (g *useGrammar) numeralTail(i int) int {
	j := i
	for g.is(j, useHN) && g.is(j+1, useN) {
		j += 2
	}
	if j == i {
		return -1
	}
	return j
}

// symbol_cluster_tail = SMAbv+ SMBlw* | SMBlw+
func (g *useGrammar) symbolTail(i int) int {
	if g.isAt(i, useSM, usePosAbv) {
		return g.star(g.star(i, useSM, usePosAbv), useSM, usePosBlw)
	}
	if g.isAt(i, useSM, usePosBlw) {
		return g.star(i, useSM, usePosBlw)
	}
	return -1
}

// tail = complex_syllable_tail | sakot_terminated_cluster_tail
//
//	| symbol_cluster_tail | virama_terminated_cluster_tail
//
// It can match nothing, which is what lets a symbol stand on its own.
func (g *useGrammar) tail(i int) int {
	return maxUse(g.complexSyllableTail(i), g.sakotTerminatedTail(i),
		g.symbolTail(i), g.viramaTerminatedTail(i))
}

func maxUse(ns ...int) int {
	best := -1
	for _, n := range ns {
		if n > best {
			best = n
		}
	}
	return best
}

// The start symbol's alternatives, in the order the specification states them.
//
// Order is what settles a tie, and there are ties that matter: a lone final
// modifier written after the letter is matched both by the eighth alternative,
// which makes it no cluster at all, and by the ninth, which would make it a
// broken one and put a dotted circle under it. The eighth is stated first and so
// wins, and that is the whole of why a stray one gets no placeholder.
func (g *useGrammar) cluster(i int) (int, useClusterKind) {
	type alternative struct {
		end  int
		kind useClusterKind
		zwnj bool
	}
	// virama_terminated_cluster = complex_syllable_start virama_terminated_cluster_tail
	// sakot_terminated_cluster  = complex_syllable_start sakot_terminated_cluster_tail
	// standard_cluster          = complex_syllable_start complex_syllable_tail
	viramaEnd, sakotEnd, standardEnd := -1, -1, -1
	if s := g.complexSyllableStart(i); s >= 0 {
		viramaEnd, sakotEnd, standardEnd =
			g.viramaTerminatedTail(s), g.sakotTerminatedTail(s), g.complexSyllableTail(s)
	}
	// number_joiner_terminated_cluster = N number_joiner_terminated_cluster_tail
	// numeral_cluster                  = N numeral_cluster_tail?
	numberJoinerEnd, numeralEnd := -1, -1
	if g.is(i, useN) {
		numberJoinerEnd = g.numberJoinerTail(i + 1)
		if numeralEnd = g.numeralTail(i + 1); numeralEnd < 0 {
			numeralEnd = i + 1
		}
	}
	// symbol_cluster = (O | GB) tail?
	//
	// The engine's Other is not a gap. Anything it has no other category for —
	// a symbol, a full stop, a letter of another script — begins a cluster and
	// takes a tail, and that is what keeps a mark written after one attached to
	// it instead of drifting onto the letter before.
	symbolEnd := -1
	if g.is(i, useO) || g.is(i, useGB) {
		if symbolEnd = g.tail(i + 1); symbolEnd < 0 {
			symbolEnd = i + 1
		}
	}
	// broken_cluster = R? (tail | number_joiner_terminated_cluster_tail | numeral_cluster_tail)
	//
	// The tail can match nothing, so the result is only a cluster if something
	// was consumed — a production that matched no characters would leave the
	// scan where it started.
	brokenStart := i
	if g.is(i, useR) {
		brokenStart++
	}
	brokenEnd := maxUse(g.tail(brokenStart), g.numberJoinerTail(brokenStart),
		g.numeralTail(brokenStart))
	if brokenEnd <= i {
		brokenEnd = -1
	}
	// FMPst on its own, and then anything at all.
	fmPstEnd := -1
	if g.isAt(i, useFM, usePosPst) {
		fmPstEnd = i + 1
	}
	anyEnd := -1
	if i < len(g.idx) {
		anyEnd = i + 1
	}

	best, bestKind, bestZWNJ := -1, useNonCluster, false
	for _, a := range []alternative{
		{viramaEnd, useViramaTerminatedCluster, true},
		{sakotEnd, useSakotTerminatedCluster, true},
		{standardEnd, useStandardCluster, true},
		{numberJoinerEnd, useNumberJoinerTerminatedCluster, true},
		{numeralEnd, useNumeralCluster, true},
		{symbolEnd, useSymbolCluster, true},
		{fmPstEnd, useNonCluster, false},
		{brokenEnd, useBrokenCluster, true},
		{anyEnd, useNonCluster, false},
	} {
		if a.end > best {
			best, bestKind, bestZWNJ = a.end, a.kind, a.zwnj
		}
	}
	if best > i && bestZWNJ && g.is(best, useZWNJ) {
		best++
	}
	return best, bestKind
}

// useClusters cuts a run into clusters, saying what each was matched as.
func useClusters(info []useInfo, runes []rune) []useCluster {
	g := &useGrammar{info: info, idx: useGrammarInput(info, runes)}
	var out []useCluster
	for i := 0; i < len(g.idx); {
		end, kind := g.cluster(i)
		// Every alternative either consumes a character or does not match, so
		// this cannot fire — and it is here because a production that consumed
		// nothing would not end the loop, and a hang is a worse way to find
		// that out than a cluster of one.
		if end <= i {
			end, kind = i+1, useNonCluster
		}
		// A cluster runs to where the next one starts, so the characters the
		// grammar cannot see are inside a cluster rather than between them:
		// they are still glyphs, and a font's rules have to reach them.
		last := len(info)
		if end < len(g.idx) {
			last = g.idx[end]
		}
		out = append(out, useCluster{start: g.idx[i], end: last, kind: kind})
		i = end
	}
	return out
}

func isUseBase(c useCategory) bool { return c == useB || c == useGB || c == useN }

// The features the engine applies, in the order it applies them.
//
// They are in three groups because a pass runs between them. The first two are
// what a font uses to build the shapes of a cluster — the half forms, the
// subjoined consonants, the conjuncts — and they have to have run before the
// reordering, because the reordering moves what they produced. The last group is
// the typographic polish, applied to a cluster already in the order it is drawn.
var (
	useBasicFeatures = []string{"locl", "ccmp", "nukt", "akhn"}
	// The reordering group, which the specification says is "applied
	// individually in this order, rphf, pref" — each on its own, because what
	// each did has to be read off the buffer before the next runs. See
	// shapeUseCluster.
	//
	// They were applied the other way round here, and with 'rphf' among the
	// orthographic features below. Nothing in these corpora shows it: of the
	// three fonts this engine shapes, none states 'rphf' at all — the bundled
	// face does, and it is Devanagari's, which goes to the Indic shaper and
	// never reaches here. So the order of the two cannot change an answer any of
	// them gives, and it is corrected because the specification states an order
	// and this is it, not because anything was seen to break.
	useRephFeature    = []string{"rphf"}
	usePreBaseFeature = []string{"pref"}
	useShapeFeatures  = []string{
		"rkrf", "abvf", "blwf", "half", "pstf", "vatu", "cjct",
	}
	useFinalFeatures = []string{
		"isol", "init", "medi", "fina", "abvs", "blws", "haln", "pres", "psts",
	}
	// And the substitutions that are not about this model at all: the required
	// ligatures and the contextual alternates every script gets, whatever
	// shaper set it. They are the same list the Khmer pass applies for the same
	// reason, and they go with the presentation features because they are
	// likewise written about what a reader sees rather than about syllables.
	//
	// The Javanese corpus does not exercise them: removing them changes none of
	// its 894 answers. They are here because a font that states a required
	// ligature under 'rlig' — which is what the feature is for — would otherwise
	// have it ignored, not because anything here has been seen to need it.
	useRunFeatures = []string{"rlig", "clig", "calt", "rclt", "liga"}
)

// shapeUniversal is the whole substitution pass for a run the engine handles.
func (sh shaper) shapeUniversal(buf []Glyph, runes []rune) []Glyph {
	info := make([]useInfo, len(runes))
	for i, r := range runes {
		info[i].cat, info[i].pos = useCategoryOf(r)
	}

	// Each cluster is shaped where it lies, and what it does to the buffer's
	// length shifts every cluster after it — so they are walked in order and the
	// shift carried along.
	dotted, hasDotted := sh.f.GlyphID(dottedCircle)
	shift := 0
	for _, cl := range useClusters(info, runes) {
		start, end := cl.start+shift, cl.end+shift
		if start < 0 || end > len(buf) || start >= end {
			continue
		}
		var delta int
		buf, delta = sh.shapeUseCluster(buf, &info, start, end, cl.kind, dotted, hasDotted)
		shift += delta
	}
	// The presentation features, applied in lookup order rather than feature
	// order.
	//
	// Which comes first is the font's decision, not this list's: a font states
	// its rules in one lookup list, and their indices are the order it means
	// them in. Noto Sans Javanese relies on that. Its jha-keret ligature is
	// lookup 18 and the rule that turns that jha into a variant is lookup 26,
	// reached from 'blws'; taking the features in the order this list happens to
	// name them applies the variant first and the ligature can no longer match,
	// which costs exactly the two cases the corpus has of it.
	//
	// This is the one place the engine needs it. The rest of this package still
	// applies features in the order it names them, which is right wherever a
	// font's lookups do not overlap — and every other corpus here says they do
	// not.
	var lookups []int
	seen := map[int]bool{}
	for _, tag := range append(append([]string{}, useFinalFeatures...), useRunFeatures...) {
		for _, idx := range sh.l.featureLookups[tag] {
			if !seen[idx] {
				seen[idx] = true
				lookups = append(lookups, idx)
			}
		}
	}
	sort.Ints(lookups)
	if len(lookups) > 0 {
		buf, _ = sh.applyUseFeature(buf, &info, lookups, 0, len(buf))
	}
	return buf
}

// shapeUseCluster shapes one cluster and reports how much longer or shorter it
// left the buffer.
func (sh shaper) shapeUseCluster(buf []Glyph, info *[]useInfo, start, end int,
	kind useClusterKind, dotted int, hasDotted bool) ([]Glyph, int) {
	total := 0
	apply := func(tags []string) {
		for _, tag := range tags {
			lookups := sh.l.featureLookups[tag]
			if len(lookups) == 0 {
				continue
			}
			var d int
			buf, d = sh.applyUseFeature(buf, info, lookups, start, end)
			total, end = total+d, end+d
		}
	}
	apply(useBasicFeatures)
	apply(useRephFeature)

	// 'pref' is the font saying "this mark has a form that goes before the
	// letter". Which mark it said it about is not something the categories
	// know — it is the font's decision, made glyph by glyph — so the only way
	// to find out is to look at where the feature applied. Whatever it applied
	// to is treated from here on as a vowel written before the letter, because
	// that is what it now is: the reordering below moves it to the front.
	//
	// Where it *applied*, not where it changed the glyph. Noto Sans Javanese
	// states the rule as cakra → cakra, substituting a glyph for itself, and it
	// means it: the substitution is how the font marks the mark, and a reader
	// that looked for a changed glyph index would see nothing happen and leave
	// every cakra on the wrong side of its letter.
	for _, tag := range usePreBaseFeature {
		lookups := sh.l.featureLookups[tag]
		if len(lookups) == 0 {
			continue
		}
		var d, at int
		buf, d, at = sh.applyUseFeatureAt(buf, info, lookups, start, end)
		total, end = total+d, end+d
		if at >= 0 && at < len(*info) {
			(*info)[at].cat, (*info)[at].pos = useV, usePosPre
		}
	}

	apply(useShapeFeatures)

	// The placeholder for a cluster that is not one.
	//
	// It goes in here, after the cluster's own rules have run over it and before
	// anything is moved, which is where the engine puts it: the font's rules are
	// written about the characters the text has, not about a glyph the shaper
	// added, and the reordering below has to see it because it is now the base.
	//
	// A face without U+25CC cannot show one. Nothing is substituted for it —
	// what the placeholder says is "this text is malformed", and a face that
	// cannot say it should say nothing rather than something else.
	if kind == useBrokenCluster && hasDotted {
		// After a repha, which is written before the letter it belongs to and
		// is part of the same malformed cluster.
		at := start
		for at < end && (*info)[at].cat == useR {
			at++
		}
		buf, *info = sh.insertUseGlyph(buf, *info, at, dotted, useInfo{cat: useB})
		end++
		total++
	}
	if kind.reorders() {
		reorderUseCluster(buf, *info, start, end)
	}
	return buf, total
}

// insertUseGlyph puts one glyph, and the record that describes it, into a buffer
// at a position.
//
// The glyph takes the cluster of what it is inserted before, so that it maps
// back to the character whose mark it is standing in for.
func (sh shaper) insertUseGlyph(buf []Glyph, info []useInfo, at, gid int, what useInfo) ([]Glyph, []useInfo) {
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

	info = append(info, useInfo{})
	copy(info[at+1:], info[at:])
	info[at] = what
	return buf, info
}

// applyUseFeature runs one feature's lookups over one cluster, keeping the
// per-glyph record in step with what the lookups do to the buffer.
//
// The cluster is both the range walked and the range a lookup may look at:
// a rule reaching into the next cluster would join glyphs the font never meant
// to see together, which is the whole reason these features are applied a
// cluster at a time rather than over the run.
func (sh shaper) applyUseFeature(buf []Glyph, info *[]useInfo, lookups []int, start, end int) ([]Glyph, int) {
	out, delta, _ := sh.applyUseFeatureAt(buf, info, lookups, start, end)
	return out, delta
}

// applyUseFeatureAt is applyUseFeature, also reporting the first position a
// lookup applied at — which is how 'pref' is read. It is -1 when none did.
func (sh shaper) applyUseFeatureAt(buf []Glyph, info *[]useInfo, lookups []int, start, end int) ([]Glyph, int, int) {
	total, step, first := 0, 0, -1
	sh.onResize = func(at, d int) {
		*info = respliceUseInfo(*info, at, d)
		// Which glyphs a substitution made of several, which is what says a
		// halant is no longer one. A lookup that shortened the run ligated what
		// it consumed; one that lengthened it took a glyph apart, and no piece
		// of it is a ligature whatever the glyph it came from was.
		switch {
		case d < 0 && at < len(*info):
			(*info)[at].ligated = true
		case d > 0:
			for k := 0; k <= d && at+k < len(*info); k++ {
				(*info)[at+k].ligated = false
			}
		}
		step += d
	}
	sh.onDelete = func(at int) {
		if at >= 0 && at < len(*info) {
			*info = append((*info)[:at], (*info)[at+1:]...)
		}
		step--
	}
	sh.floor, sh.limit = start, end
	for _, idx := range lookups {
		for i := start; i < end && i < len(buf); {
			step = 0
			consumed, out := sh.applyGSUBAt(idx, buf, i, 0)
			buf = out
			end += step
			total += step
			if consumed > 0 {
				if first < 0 {
					first = i
				}
				i += consumed
				continue
			}
			// A lookup that consumed nothing and shortened the run took a glyph
			// out; what followed it is now here and has not been looked at.
			if step < 0 {
				continue
			}
			i++
		}
	}
	return buf, total, first
}

// respliceUseInfo keeps the per-glyph record the same length as the buffer.
//
// A lookup that shortened the run replaced several glyphs with one, which keeps
// the first record; one that lengthened it took a glyph apart, and every piece
// is what the whole was.
func respliceUseInfo(info []useInfo, at, delta int) []useInfo {
	switch {
	case delta == 0 || at < 0 || at >= len(info):
		return info
	case delta < 0:
		cut := -delta
		if at+1+cut > len(info) {
			cut = len(info) - at - 1
		}
		if cut <= 0 {
			return info
		}
		return append(info[:at+1], info[at+1+cut:]...)
	default:
		grown := make([]useInfo, 0, len(info)+delta)
		grown = append(grown, info[:at]...)
		for k := 0; k <= delta; k++ {
			grown = append(grown, info[at])
		}
		return append(grown, info[at+1:]...)
	}
}

// reorderUseSyllable puts a cluster's glyphs into the order they are drawn.
//
// Two things move, and they move in opposite directions:
//
//   - A repha is written before the letter it belongs to and drawn after it, so
//     it goes forward, to just before the first glyph that is drawn after the
//     base.
//   - A vowel written before the letter — the engine calls it a pre-base vowel
//     because of where it is *drawn*, not where it is written — goes back, to
//     the front of the cluster or to just after the last halant, since a halant
//     says the letter after it is the one the vowel belongs to.
//
// Only the first glyph of a decomposition moves. A vowel sign the font took
// apart has its pieces on different sides of the letter, and moving the lot
// would take the piece that belongs after the letter round to the front.
func reorderUseCluster(buf []Glyph, info []useInfo, start, end int) {
	if start < 0 || end > len(buf) || end > len(info) || end-start < 2 {
		return
	}
	// The repha, forward: to just before the first glyph that is drawn after
	// the base, or to the end of the cluster if there is none.
	//
	// "Drawn after the base" is a property of the glyph, not a run of letters:
	// a medial, a vowel, a vowel modifier, a final or a final modifier is, and
	// a consonant modifier — a nukta, say — is not, because it belongs to the
	// letter rather than following it. Walking forward over bases and stackers
	// instead, which is what stood here, sends the repha to the wrong side of
	// any nukta the letter carries.
	if info[start].cat == useR {
		for i := start + 1; i < end; i++ {
			post := isUsePostBase(info[i]) || isUseHalant(info[i])
			if !post && i != end-1 {
				continue
			}
			if post {
				i--
			}
			if i > start {
				rotateUse(buf, info, start, start+1, i+1)
			}
			break
		}
	}
	// The pre-base vowels, back.
	at := start
	for i := start; i < end; i++ {
		switch {
		case isUseHalant(info[i]):
			// What follows the halant is the letter the vowel belongs to, so
			// nothing before this point is a destination any more.
			at = i + 1
		case info[i].pos == usePosPre && (info[i].cat == useV || info[i].cat == useVM) &&
			buf[i].lig.comp == 0 && at < i:
			// The destination does not move afterwards. A second vowel written
			// before the letter therefore goes in front of the first, reversing
			// the two — which is what the model says and what looked like an
			// error worth "fixing" until HarfBuzz was asked: for a Balinese
			// letter carrying two of them it gives the same reversal.
			rotateUse(buf, info, at, i, i+1)
		}
	}
}

// isUsePostBase reports whether a glyph is drawn after the base: the medials,
// the vowels, the vowel modifiers, the finals and the final modifier. It is
// what a repha is moved in front of.
func isUsePostBase(t useInfo) bool {
	switch t.cat {
	case useM, useV, useVM, useF, useFM:
		return true
	}
	return false
}

// isUseHalant reports whether a glyph still joins what follows it to what
// precedes it.
//
// A halant the font has ligated into something else no longer does: what it
// made is a letter, and a letter is not a place a vowel is sent to. The sakot
// is not one of these — it joins across a cluster boundary rather than within
// one, and the reordering is not about it.
func isUseHalant(t useInfo) bool {
	if t.ligated {
		return false
	}
	return t.cat == useH || t.cat == useHVM || t.cat == useIS
}

// rotateUse moves buf[mid:end] to the front of buf[start:end], keeping the order
// within each part and carrying the per-glyph record with it.
//
// Everything it moves becomes one cluster: the glyphs are no longer in the order
// their characters were written, so the smallest piece of text that can honestly
// be pointed at is the whole of what was rearranged.
func rotateUse(buf []Glyph, info []useInfo, start, mid, end int) {
	if start < 0 || start >= mid || mid >= end || end > len(buf) || end > len(info) {
		return
	}
	movedBuf := append([]Glyph(nil), buf[mid:end]...)
	movedInfo := append([]useInfo(nil), info[mid:end]...)
	copy(buf[start+len(movedBuf):end], buf[start:mid])
	copy(info[start+len(movedInfo):end], info[start:mid])
	copy(buf[start:], movedBuf)
	copy(info[start:], movedInfo)
	oneCluster(buf, start, end)
}

// universalScripts are the OpenType script tags the engine is used for.
//
// They are named rather than derived, and the list is the engine's
// specification's. Deriving it — "any script with characters of a complex
// category" — would sweep in Latin, whose combining marks have categories for
// Unicode's purposes and want none of this, and would make adding a script a
// silent change in what a document looks like rather than a decision.
//
// A script with a shaper of its own is not here: Devanagari and its relatives,
// Khmer and Myanmar are each modelled in their own file, because each has rules
// the general model does not carry.
var universalScripts = map[string]bool{
	"adlm": true, "ahom": true, "bali": true, "batk": true, "bhks": true,
	"brah": true, "bugi": true, "buhd": true, "cakm": true, "cham": true,
	"chrs": true, "cpmn": true, "diak": true, "dogr": true, "dupl": true,
	"egyp": true, "elym": true, "gong": true, "gonm": true, "gran": true,
	"hano": true, "hmng": true, "hmnp": true, "java": true, "kali": true,
	"kawi": true, "khar": true, "khoj": true, "kits": true, "kthi": true,
	"lana": true, "lepc": true, "limb": true, "mahj": true, "maka": true,
	"mand": true, "mani": true, "marc": true, "medf": true, "modi": true,
	"mong": true, "mtei": true, "mult": true, "nagm": true, "nand": true,
	"newa": true, "nko ": true, "ougr": true, "phag": true, "phlp": true,
	"plrd": true, "rjng": true, "rohg": true, "saur": true, "shrd": true,
	"sidd": true, "sind": true, "sogd": true, "sogo": true, "soyo": true,
	"sund": true, "sylo": true, "tagb": true, "takr": true, "tale": true,
	"talu": true, "tavt": true, "tfng": true, "tglg": true, "tibt": true,
	"tirh": true, "tnsa": true, "toto": true, "vith": true, "wcho": true,
	"yezi": true, "zanb": true,
}

// usesUniversalShaper reports whether a run's script is set by the engine.
func usesUniversalShaper(script uint16) bool {
	for _, tag := range scriptTags(script) {
		if universalScripts[tag] {
			return true
		}
	}
	return false
}
