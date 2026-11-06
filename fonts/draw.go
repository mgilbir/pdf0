package fonts

import (
	"slices"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/mgilbir/forme/font"
	"github.com/mgilbir/forme/shape"
	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/simplefont"
)

// From positioned glyphs to content-stream bytes, and from drawn glyphs back to
// the text they were drawn for.
//
// Every public way of putting text on a page — Encode, Shape, ShapeWith, Draw,
// DrawShaped, DrawUpright — comes through here, and that is the point of the file. They used
// to be four loops, each writing its own bytes: two wrote the glyph index where
// a CID-keyed CFF font is addressed by CID, one wrote two-byte codes into a
// one-byte font, and none of them told the ToUnicode CMap what a shaped glyph
// had been drawn for. The first three were each fixed where they were found and
// the fourth was never noticed, because each loop had its own test and each test
// exercised the loop it sat beside.
//
// So there is one function that turns a glyph into a code (appendCode), one
// that says how far the text operator will move the pen for it
// (nominalAdvance), and one plan (plan) that decides, from the glyphs and the
// text they were shaped from, what the ToUnicode CMap will say for each glyph
// and where the page needs an /ActualText to say what no per-glyph mapping can.
// The two emitters — spans for Shape, operators for Draw — only write what the
// plan decided.

// appendCode appends the character code the embedded font addresses a glyph
// by. It is the only place in this package a glyph becomes content-stream bytes.
//
// A composite face's code is two bytes, and it is GlyphCode's answer: the glyph
// index for every face but a CID-keyed CFF, and the CID for that one. /W,
// /CIDSet and /ToUnicode are keyed by the same call, which is what makes them
// agree with the page by construction. A simple or standard face's code is the
// one WinAnsi byte its GlyphID already is.
//
// A number no code can carry — a glyph handed in from somewhere other than this
// face — is written as code 0, .notdef, which a reader shows as the missing
// glyph it is, rather than truncated into some other glyph's code.
//
// A bitmap face's code depends on which of its Type 3 fonts the glyph is drawn
// in, which this cannot say; its writers ask type3.go instead.
func (f *Face) appendCode(dst []byte, gid int) []byte {
	if f.composite() {
		code := f.GlyphCode(gid)
		if code < 0 || code > 0xFFFF {
			code = 0
		}
		return append(dst, byte(code>>8), byte(code))
	}
	if gid < 0 || gid > 0xFF {
		gid = 0
	}
	return append(dst, byte(gid))
}

// nominalAdvance is how far the text-showing operator will move the pen for one
// glyph: the width the font dictionary states for its code, in thousandths of
// an em.
//
// For a composite face that is the glyph's own advance, which /W is written
// from. For a simple or standard face it is the width of the character the code
// names — /Widths, or the standard metrics — and asking the glyph index table
// for it would look the width up under a number that is a character code.
func (f *Face) nominalAdvance(g Glyph) float64 {
	if f.composite() {
		return f.GlyphAdvance(g.GID)
	}
	if r, ok := winAnsiRune(g.GID); ok {
		if w, ok := f.Advance(r); ok {
			return w
		}
	}
	return g.XAdvance
}

// winAnsiRune is the character a one-byte code names in WinAnsiEncoding, which
// is the encoding every simple and standard face here is written with.
func winAnsiRune(code int) (rune, bool) {
	if code < 0 || code > 0xFF {
		return 0, false
	}
	name, ok := simplefont.WinAnsiEncoding.GlyphName(byte(code))
	if !ok {
		return 0, false
	}
	return font.GlyphNameToRune(name, byte(code))
}

// textRecord is what each glyph of a composite face was drawn for, and so what
// the ToUnicode CMap says it means (ISO 32000-2 9.10.3).
//
// It is written by the drawing and read by Embed. A glyph's entry is fixed the
// first time the glyph is drawn and never changes afterwards, which is the
// property the /ActualText decisions rely on: a run drawn earlier was checked
// against the mapping as it then stood, and a later draw that rewrote it would
// silently change what the earlier run extracts as.
type textRecord struct {
	byGID map[int]string
	// scratch is what plan works in; see planScratch.
	scratch planScratch
	// canon is the character the font's own cmap assigns each glyph, where it
	// assigns one: the font's, shared with every wrapper of it, and held here
	// so that it lives while this record does. See canonical.
	canon *canonicalMap
}

func (f *Face) record() *textRecord {
	if f.rec == nil {
		f.rec = &textRecord{byGID: map[int]string{}}
	}
	return f.rec
}

// canonical is the one character the font's cmap maps to a glyph, choosing as
// betterForToUnicode says where several do, and whether there is one.
//
// It is what the ToUnicode CMap says for a glyph drawn for a plain character,
// and it is the whole of what it can say for one reached some other way than
// through this package's drawing — an adopted face's other wrapper, or a caller
// using the shaping face directly.
func (f *Face) canonical(gid int) (rune, bool) {
	rec := f.record()
	if rec.canon == nil {
		rec.canon = canonicalMapFor(f.Face)
	}
	return rec.canon.lookup(f.Face, gid)
}

// maxToUnicodeUnits bounds a ToUnicode destination: a bfchar destination is at
// most 512 bytes (Adobe Technical Note 5411, 1.4.1), which is 256 UTF-16 code
// units. A longer cluster is still extractable — the drawing gives it an
// /ActualText — but it is not what a single glyph's entry can say.
const maxToUnicodeUnits = 256

// toUnicodeText cleans a cluster's text into something a ToUnicode entry may
// say, or reports that it cannot be one: the code points PDF/A forbids a
// destination to name are dropped, and what is left must be non-empty and fit.
func toUnicodeText(s string) (string, bool) {
	if strings.ContainsFunc(s, forbiddenInToUnicode) {
		s = strings.Map(func(r rune) rune {
			if forbiddenInToUnicode(r) {
				return -1
			}
			return r
		}, s)
	}
	if s == "" || !utf8.ValidString(s) {
		return "", false
	}
	units := 0
	for _, r := range s {
		units += utf16.RuneLen(r)
	}
	return s, units <= maxToUnicodeUnits
}

// setText records what a glyph means, unless it already means something.
func (rec *textRecord) setText(gid int, s string) {
	if _, done := rec.byGID[gid]; !done {
		rec.byGID[gid] = s
	}
}

// recordCluster decides the ToUnicode text of the glyphs of one cluster that
// have none yet. text is the cluster's source text; glyphs are its glyphs, in
// the order they are drawn.
//
// A cluster is what shaping could not divide: a character and its marks, a
// ligature, a conjunct, a reordered vowel sign. Its glyphs are given what can be
// said about each of them individually, and when the mapped texts in drawing
// order do not spell the cluster, the drawing wraps it in an /ActualText —
// which is the part that makes extraction exact. What this decides is only how
// much of the text a reader that ignores /ActualText still gets, and what the
// glyph means the next time it is drawn.
func (f *Face) recordCluster(glyphs []Glyph, text string) {
	if !f.composite() {
		return // a one-byte code's meaning is its encoding's, not the drawing's
	}
	rec := f.record()
	if len(glyphs) == 1 {
		g := glyphs[0].GID
		if g == 0 {
			return // .notdef stands for nothing: see plan
		}
		if _, done := rec.byGID[g]; done {
			return
		}
		// One glyph for one character the font's cmap draws with it: say the
		// character the cmap prefers for the glyph. That is the character
		// itself except where Unicode encoded one shape twice — 日 and the
		// radical ⽇ — and there the glyph is better named by the ideograph;
		// the drawing gives the radical its /ActualText.
		if r, size := utf8.DecodeRuneInString(text); size == len(text) && r != utf8.RuneError {
			if gid, ok := f.GlyphID(r); ok && gid == g {
				if c, ok := f.canonical(g); ok {
					rec.setText(g, string(c))
					return
				}
			}
		}
		// Anything else is a glyph that stands for the whole cluster: a
		// ligature for "ffi", a conjunct for "क्ष", an Arabic letter's
		// positional form for the letter. That is exactly what a ToUnicode
		// entry can say, one glyph to several characters.
		if s, ok := toUnicodeText(text); ok {
			rec.setText(g, s)
		} else if c, ok := f.canonical(g); ok {
			rec.setText(g, string(c))
		}
		return
	}

	// Several glyphs. Each one the cmap names, whose character is in the
	// cluster, is that character — a base letter, a vowel sign, a mark. What
	// they leave of the cluster belongs to the first glyph that has no such
	// name: a half-form, a conjunct, a reordered part. The cluster in drawing
	// order may still not spell the text — a vowel sign drawn before the
	// consonant it follows — and the drawing gives that an /ActualText.
	//
	// The characters the named glyphs take are struck out of the cluster by
	// position, so the whole pass is linear in the cluster: a cluster is
	// whatever the text says it is, and a base letter under ten thousand
	// combining marks is one.
	left := newResidual(text)
	var unnamed []int
	for _, gl := range glyphs {
		g := gl.GID
		if g == 0 {
			continue
		}
		if name, known := rec.byGID[g]; known {
			left.take(name)
			continue
		}
		if c, ok := f.canonical(g); ok && left.take(string(c)) {
			rec.setText(g, string(c))
			continue
		}
		unnamed = append(unnamed, g)
	}
	residual := left.String()
	for i, g := range unnamed {
		if _, done := rec.byGID[g]; done {
			continue // the same glyph twice in one cluster
		}
		if i == 0 {
			if s, ok := toUnicodeText(residual); ok {
				rec.setText(g, s)
				continue
			}
		}
		// A second unnamed piece has no share of the text left to take. It is
		// given the character the cmap names it by, if any, or the cluster
		// whole: every glyph shown needs some mapping for the page to be
		// extractable by a reader that has no /ActualText, and the cluster is
		// the truest thing that can be said of a piece of it.
		if c, ok := f.canonical(g); ok {
			rec.setText(g, string(c))
		} else if s, ok := toUnicodeText(text); ok {
			rec.setText(g, s)
		}
	}
}

// residual is a cluster's text with some of its characters struck out.
type residual struct {
	runes  []rune
	struck []bool
	// at lists, for each character, the positions it occurs at that are not
	// struck, in order; a position is dropped from the front once struck.
	at map[rune][]int
}

func newResidual(text string) *residual {
	r := &residual{runes: []rune(text), at: map[rune][]int{}}
	r.struck = make([]bool, len(r.runes))
	for i, c := range r.runes {
		r.at[c] = append(r.at[c], i)
	}
	return r
}

// take strikes out name where it first occurs among the characters not yet
// struck, reading them as consecutive, and reports whether it did. A name of
// several characters (a glyph drawn earlier for a ligature's letters) is
// matched only at the first unstruck occurrence of its first character, which
// keeps the cost to the length of the name.
func (r *residual) take(name string) bool {
	nr := []rune(name)
	if len(nr) == 0 {
		return false
	}
	q := r.at[nr[0]]
	for len(q) > 0 && r.struck[q[0]] {
		q = q[1:]
	}
	r.at[nr[0]] = q
	if len(q) == 0 {
		return false
	}
	idx := make([]int, 1, len(nr))
	idx[0] = q[0]
	j := q[0] + 1
	for _, c := range nr[1:] {
		for j < len(r.runes) && r.struck[j] {
			j++
		}
		if j >= len(r.runes) || r.runes[j] != c {
			return false
		}
		idx = append(idx, j)
		j++
	}
	for _, k := range idx {
		r.struck[k] = true
	}
	return true
}

// String is the characters not struck, in order.
func (r *residual) String() string {
	var b strings.Builder
	for i, c := range r.runes {
		if !r.struck[i] {
			b.WriteRune(c)
		}
	}
	return b.String()
}

// extracted is what a reader following the font's ToUnicode CMap — or, for a
// simple or standard face, its encoding — gets back for one glyph as this
// package writes it.
func (f *Face) extracted(g Glyph) string {
	if f.composite() {
		if g.GID == 0 {
			return ""
		}
		return f.record().byGID[g.GID]
	}
	if f.IsStandard() && (f.Name() == "Symbol" || f.Name() == "ZapfDingbats") {
		// These carry their own built-in encodings, which this package does not
		// model; saying nothing makes every run of them carry its text
		// explicitly, which is correct whatever the reader does.
		return ""
	}
	if r, ok := winAnsiRune(g.GID); ok {
		return string(r)
	}
	return ""
}

// segment is a stretch of drawn glyphs, [lo, hi), and whether it carries an
// /ActualText and what.
type segment struct {
	lo, hi int
	actual string
	marked bool
}

// plan records what the glyphs mean and divides them into the stretches that
// need an /ActualText and the ones whose ToUnicode mapping already says the
// text. It is the one place either decision is made.
//
// text is the string the glyphs were shaped from, and their Cluster offsets are
// byte offsets into it. The clusters partition it: each runs from its own
// offset to the next cluster's, the first from the start of the text and the
// last to its end, so a character shaping drew nothing for — a joiner, a
// character a ligature in a neighbouring run swallowed — still belongs to the
// cluster beside it and is still extracted.
//
// Text drawn in its own order is checked cluster by cluster, and only a
// cluster whose glyphs do not spell it is marked. A stretch drawn in another
// order — a right-to-left word, or glyphs reordered across clusters — is one
// marked stretch with its text, because the glyphs are in
// the order they are drawn and a reader reads them in that order: without the
// /ActualText an Arabic word comes back reversed. Only that stretch is marked,
// not the run around it, and not at all where the caller leaves the order to
// the reader (SetMarkReordered).
func (f *Face) plan(glyphs []Glyph, text string) []segment {
	rec := f.record()
	sc := &rec.scratch
	// Every glyph drawn goes into forme's record of use, which is what the
	// subset keeps and /CIDSet lists. Shaping records what it returns; a glyph
	// a caller placed by index — a formula's stretched operator, drawn from its
	// MATH table — reaches no shaping, and is recorded here (forme #857).
	if f.composite() {
		sc.gids = sc.gids[:0]
		for _, g := range glyphs {
			sc.gids = append(sc.gids, g.GID)
		}
		f.Use(sc.gids...)
	}
	if len(glyphs) == 0 {
		if visible(text) == "" {
			return nil
		}
		// Nothing drawn for text that exists: its characters were drawn by a
		// neighbouring run — a ligature that began there — and a marked
		// stretch with no content keeps them in the extracted text.
		return []segment{{marked: true, actual: visible(text)}}
	}
	// The cluster offsets have to be positions in text, at character
	// boundaries, or nothing about them can be trusted — the glyphs were
	// shaped from some other string. Then the only honest statement is the
	// whole of the text for the whole of the run.
	valid := true
	for _, g := range glyphs {
		if g.Cluster < 0 || g.Cluster > len(text) ||
			(g.Cluster < len(text) && !utf8.RuneStart(text[g.Cluster])) {
			valid = false
			break
		}
	}
	if !valid {
		f.recordCluster(glyphs, visible(text))
		return f.wholeRun(glyphs, visible(text))
	}
	// Group consecutive glyphs of one cluster, and see whether the groups come
	// in the order of the text.
	groups := sc.groups[:0]
	ascending := true
	for i, g := range glyphs {
		if n := len(groups); n > 0 && groups[n-1].cluster == g.Cluster {
			groups[n-1].hi = i + 1
			continue
		} else if n > 0 && g.Cluster < groups[n-1].cluster {
			ascending = false
		}
		groups = append(groups, group{lo: i, hi: i + 1, cluster: g.Cluster})
	}
	sc.groups = groups
	out := sc.out[:0]
	defer func() { sc.out = out[:0] }()

	if ascending && ascendingFastPath {
		// Text in its own order, which is nearly every run: each group is
		// its own cluster and its own block, and its text runs to the next
		// group's offset — the first from the start of the text, the last to
		// its end. What the general path below works out with its tables is
		// read off the groups as they stand.
		for i, gr := range groups {
			lo, hi := gr.cluster, len(text)
			if i == 0 {
				lo = 0
			}
			if i+1 < len(groups) {
				hi = groups[i+1].cluster
			}
			out = f.planCluster(out, glyphs, gr, visible(text[lo:hi]))
		}
		return out
	}

	// Each cluster's text, whatever order the glyphs are in: from its offset
	// to the next larger offset among all the clusters.
	sc.sorted = sc.sorted[:0]
	for _, gr := range groups {
		sc.sorted = append(sc.sorted, gr.cluster)
	}
	sortedUnique := sortUniqueInPlace(sc.sorted)
	clusterText := func(c int) string {
		i := indexOf(sortedUnique, c)
		lo := c
		if i == 0 {
			lo = 0
		}
		hi := len(text)
		if i+1 < len(sortedUnique) {
			hi = sortedUnique[i+1]
		}
		return visible(text[lo:hi])
	}
	// Divide the groups into blocks: the shortest stretches of the run, in
	// drawing order, each of which draws a stretch of the text whole — clusters
	// next to each other in the text, every group of each of them, following
	// the stretch the block before drew. Text drawn in its own order is a block
	// a cluster. A stretch drawn in another order — a right-to-left word in a
	// left-to-right line — is one block, and only it is marked, so the text
	// around it extracts as it reads, word breaks and all (#426). A run with no
	// smaller block is one, the whole run, as every non-ascending run was.
	nc := len(sortedUnique)
	idx := resized(&sc.idx, len(groups)) // each group's cluster, as its index in sortedUnique
	last := resized(&sc.last, nc)        // each cluster's last group
	count := resized(&sc.count, nc)      // how many groups each cluster is drawn in
	stamp := resized(&sc.stamp, nc)      // the block, counted from 1, a cluster was last counted in
	for i, gr := range groups {
		c := indexOf(sortedUnique, gr.cluster)
		idx[i], last[c] = c, i
		count[c]++
	}
	unmarkReordered := f.Horizontal().unmarkReordered
	next, first, block := 0, 0, 0
	var lo, hi, reach, distinct int
	for i := range groups {
		c := idx[i]
		if i == first {
			block++
			lo, hi, reach, distinct = c, c, last[c], 0
		}
		if stamp[c] != block {
			stamp[c] = block
			distinct++
		}
		lo, hi, reach = min(lo, c), max(hi, c), max(reach, last[c])
		if i < reach || lo != next || hi-lo+1 != distinct {
			continue
		}
		b0, bg := first, groups[first:i+1]
		next, first = hi+1, i+1
		if len(bg) == 1 {
			out = f.planCluster(out, glyphs, bg[0], clusterText(bg[0].cluster))
			continue
		}
		// Reordered. A cluster drawn in more than one group is not a cluster
		// this can name; record the ones that are whole.
		whole := true
		for j, gr := range bg {
			if count[idx[b0+j]] > 1 {
				whole = false
			} else {
				f.recordCluster(glyphs[gr.lo:gr.hi], clusterText(gr.cluster))
			}
		}
		if unmarkReordered && whole {
			// The caller leaves the order to the reader (SetMarkReordered):
			// each cluster is still checked, as text in its own order is.
			for _, gr := range bg {
				out = f.planCluster(out, glyphs, gr, clusterText(gr.cluster))
			}
			continue
		}
		from, to := 0, len(text)
		if lo > 0 {
			from = sortedUnique[lo]
		}
		if hi+1 < nc {
			to = sortedUnique[hi+1]
		}
		blockText := visible(text[from:to])
		s := segment{lo: bg[0].lo, hi: bg[len(bg)-1].hi}
		if !f.spells(glyphs[s.lo:s.hi], blockText) {
			s.marked, s.actual = true, blockText
		}
		out = appendSegment(out, s)
	}
	return out
}

// ascendingFastPath lets plan read a run in its text's order straight off
// its groups. TestPlanFastPathIsTheGeneralPath turns it off, to hold the two
// to the same plan.
var ascendingFastPath = true

// group is a stretch of consecutive glyphs of one cluster, [lo, hi), and the
// cluster's byte offset in the text.
type group struct{ lo, hi, cluster int }

// planScratch is what plan works in, kept on the record between calls rather
// than made again for each: a caller recording thousands of short labels
// otherwise allocates a handful of slices for every one (#434). The record is
// written by one drawing at a time already, and plan's result is used before
// the next call, so nothing it returns outlives the reuse.
type planScratch struct {
	gids                    []int
	groups                  []group
	sorted                  []int
	idx, last, count, stamp []int
	out                     []segment
}

// resized is *s at length n, all zero, reusing its array where it is long
// enough.
func resized(s *[]int, n int) []int {
	if cap(*s) < n {
		*s = make([]int, n)
	} else {
		*s = (*s)[:n]
		clear(*s)
	}
	return *s
}

// planCluster records one cluster's glyphs and appends their stretch: marked
// only when the glyphs, read in drawing order, do not spell the cluster.
func (f *Face) planCluster(out []segment, glyphs []Glyph, gr group, ct string) []segment {
	f.recordCluster(glyphs[gr.lo:gr.hi], ct)
	return appendSegment(out, segment{lo: gr.lo, hi: gr.hi, marked: !f.spells(glyphs[gr.lo:gr.hi], ct), actual: ct})
}

// appendSegment appends s, joining it to the stretch before when neither is
// marked: a stretch is only a boundary the emitter has to respect, and fewer
// is smaller output.
func appendSegment(out []segment, s segment) []segment {
	if n := len(out); n > 0 && !s.marked && !out[n-1].marked {
		out[n-1].hi = s.hi
		return out
	}
	return append(out, s)
}

// spells reports whether the glyphs' extracted texts, in drawing order, are
// text — compared piece by piece, with nothing built.
func (f *Face) spells(glyphs []Glyph, text string) bool {
	for _, g := range glyphs {
		s := f.extracted(g)
		if !strings.HasPrefix(text, s) {
			return false
		}
		text = text[len(s):]
	}
	return text == ""
}

// SetMarkReordered says whether a stretch of a run drawn in an order other
// than its text's — a right-to-left word, or a right-to-left line — is wrapped
// in an /ActualText with its text in reading order. It is, by default.
//
// The glyphs of right-to-left text are drawn in the order they stand on the
// line, which is the reverse of the order they are read in, and a reader
// that extracts text in drawing order — Document.ExtractText among them —
// gets the word backwards without the /ActualText. A tagged PDF needs it:
// content inside a marked-content sequence is in logical order (ISO 32000-2
// 14.8.2.5.1).
//
// A reader that orders text itself, by the Unicode bidirectional algorithm,
// does not need it, and some do worse with it: poppler (pdftotext) reorders
// the /ActualText as if it were in drawing order too, and returns the word
// reversed (#426). SetMarkReordered(false) is for a caller writing an
// untagged document for such readers. A stretch is then left unmarked where
// each cluster's glyphs, read in drawing order, spell the cluster, as text
// in its own order is; a cluster they do not spell is still marked, and a
// cluster drawn in pieces apart still marks its stretch, since no per-glyph
// mapping can say what it was.
//
// It applies to runs recorded or drawn after it, and to the face's vertical
// form as well. A face's ToUnicode CMap is the same either way.
func (f *Face) SetMarkReordered(mark bool) {
	f.Horizontal().unmarkReordered = !mark
}

// wholeRun is the plan for a run that cannot be checked cluster by cluster:
// one stretch, marked unless the mapped glyphs happen to spell the text.
func (f *Face) wholeRun(glyphs []Glyph, text string) []segment {
	s := segment{lo: 0, hi: len(glyphs)}
	if !f.spells(glyphs, text) {
		s.marked, s.actual = true, text
	}
	return []segment{s}
}

// visible is the text with the characters nothing is drawn for taken out —
// joiners, bidi controls, soft hyphens, variation selectors.
//
// They are instructions to the shaper and the line breaker, not text a reader
// copies out of the page, and they are what a cluster's text would otherwise
// carry into its ToUnicode entry: "a" followed by a zero width joiner is still
// the letter a. It is also what keeps a right-to-left run's override character,
// which htmlpdf puts in front of the text to state the direction, out of what
// the page says the run reads.
func visible(s string) string {
	if !strings.ContainsFunc(s, shape.DrawsNothing) {
		return s
	}
	return strings.Map(func(r rune) rune {
		if shape.DrawsNothing(r) {
			return -1
		}
		return r
	}, s)
}

// sortUniqueInPlace sorts v and drops its repeats, in v's own array.
func sortUniqueInPlace(v []int) []int {
	// slices.Sort and not an insertion sort that is linear on text already in
	// order: a right-to-left run arrives in exactly the reverse order, where
	// that is quadratic in the length of the run.
	slices.Sort(v)
	n := 0
	for i, x := range v {
		if i == 0 || x != v[n-1] {
			v[n] = x
			n++
		}
	}
	return v[:n]
}

func indexOf(sorted []int, x int) int {
	lo, hi := 0, len(sorted)
	for lo < hi {
		m := (lo + hi) / 2
		if sorted[m] < x {
			lo = m + 1
		} else {
			hi = m
		}
	}
	return lo
}

// spans writes planned glyphs as the spans ShowTextAdjusted takes.
//
// Two displacements per glyph, at most, and usually none: an offset displaces
// a glyph without moving the pen, so it is put in before the glyph and taken
// back out after; and the pen has moved by the width the font dictionary
// states, which is not what shaping decided, so the difference comes off. The
// two are emitted as one number where they meet, because a displacement is
// three bytes of content stream and a page has thousands of them.
//
// A bitmap face's codes are its home font's, there being no stream to switch
// fonts in, and dropped is the count of glyphs that font cannot take: its 256
// codes given out. See type3.go.
func (f *Face) spans(glyphs []Glyph, text string) (out []content.TextSpan, dropped int) {
	var run []byte
	flush := func() {
		if len(run) > 0 {
			out = append(out, content.TextSpan{Codes: run})
			run = nil
		}
	}
	adjust := func(v float64) {
		if v == 0 {
			return
		}
		flush()
		// The sign flips: a positive TJ number moves what follows *closer*.
		out = append(out, content.TextSpan{Adjust: -v})
	}
	for _, seg := range f.plan(glyphs, text) {
		if seg.marked {
			flush()
			out = append(out, content.ActualTextStart(seg.actual))
		}
		for _, g := range glyphs[seg.lo:seg.hi] {
			if f.isType3() {
				c, ok := f.homeCode(g.GID)
				if !ok {
					dropped++
					continue
				}
				adjust(g.XOffset)
				run = append(run, c)
			} else {
				adjust(g.XOffset)
				run = f.appendCode(run, g.GID)
			}
			adjust(g.XAdvance - f.nominalAdvance(g) - g.XOffset)
		}
		if seg.marked {
			flush()
			out = append(out, content.ActualTextEnd())
		}
	}
	flush()
	return out, dropped
}

// draw writes planned glyphs as text operators, with a rise for an offset
// across the line, which spans cannot express.
func (f *Face) draw(b *content.Builder, glyphs []Glyph, text string, size float64) {
	f.drawPlanned(b, glyphs, f.plan(glyphs, text), size)
}

// drawReplaced is draw for a run whose glyphs a reader is not to read as they
// stand: the whole run is one /ActualText saying actual, when wrap is set, and
// the per-glyph plan decides only the ToUnicode CMap. There is no /ActualText
// inside it: the one says it all, and a reader that keeps a single
// /ActualText at a time would lose the outer to an inner. vertical says the
// glyphs are written by the vertical form's emitter.
//
// It is for DrawUpright, whose run is its text in the order it was written:
// where an upright run's glyphs stand is not where a reader rebuilding text
// from positions looks for its words. In a horizontal font each glyph is
// placed by a displacement along the line and a rise across it: a glyph moved
// right by more than a small gap — a mark hung over its base, a narrow letter
// centred in the column — reads as the start of a word, and a glyph below
// another as a new line. In a vertical form the reader knows the line runs
// down, and still reads a letter-spaced column, or a glyph centred in an em
// longer than its own advance, as words, and a move across the line as a new
// one. And it is for DrawReplaced and DrawUprightReplaced, whose glyphs stand
// for something else than their text, or for nothing.
func (f *Face) drawReplaced(b *content.Builder, glyphs []Glyph, text string, size float64, actual string, wrap, vertical bool) {
	emit := f.drawPlanned
	if vertical {
		emit = f.drawPlannedVertical
	}
	segs := f.plan(glyphs, text)
	if !wrap {
		emit(b, glyphs, segs, size)
		return
	}
	for i := range segs {
		segs[i].marked = false
	}
	b.BeginActualText(actual)
	emit(b, glyphs, segs, size)
	b.EndMarked()
}

// drawPlanned writes glyphs as text operators, stretch by stretch as a plan
// divided them.
func (f *Face) drawPlanned(b *content.Builder, glyphs []Glyph, segs []segment, size float64) {
	var (
		run  []byte
		rise float64
	)
	flush := func() {
		if len(run) > 0 {
			b.ShowText(run)
			run = nil
		}
	}
	code := func(g Glyph) bool {
		run = f.appendCode(run, g.GID)
		return true
	}
	if f.isType3() {
		// A bitmap face is several Type 3 fonts, and the one a glyph is drawn
		// in depends on the size and the fill colour; see type3.go. The
		// caller selected the face under its own name, which is the home
		// font, and gets that selection back after the run.
		base, tfSize, ok := b.Font()
		if !ok {
			b.Fail(errType3NoFont)
			return
		}
		t := f.type3()
		cur, err := t.home(f)
		if err != nil {
			b.Fail(err)
			return
		}
		code = func(g Glyph) bool {
			fill, known := b.FillColor()
			sub, c, err := t.place(f, g.GID, size, fill, known, cur)
			if err != nil {
				b.Fail(err)
				return false
			}
			if sub != cur {
				flush()
				b.SetFont(SubfontName(base, sub.index), tfSize)
				cur = sub
			}
			run = append(run, c)
			return true
		}
		defer func() {
			if cur.index != 0 {
				flush()
				b.SetFont(base, tfSize)
			}
		}()
	}
	move := func(d float64) {
		if d == 0 {
			return
		}
		flush()
		// TJ subtracts its number, so moving the pen forward is negative.
		b.ShowTextAdjusted(content.TextSpan{Adjust: -d})
	}
	for _, seg := range segs {
		if seg.marked {
			flush()
			b.BeginActualText(seg.actual)
		}
		for _, g := range glyphs[seg.lo:seg.hi] {
			if g.YOffset != rise {
				flush()
				// A rise is in unscaled text-space units, so an offset in
				// thousandths of an em scales by the size the text is set at.
				b.SetRise(g.YOffset * size / 1000)
				rise = g.YOffset
			}
			move(g.XOffset)
			if !code(g) {
				return
			}
			// The operator will advance the pen by the font's own width; the
			// run wants to end up XAdvance further on, with the offset undone.
			move(g.XAdvance - f.nominalAdvance(g) - g.XOffset)
		}
		if seg.marked {
			flush()
			b.EndMarked()
		}
	}
	flush()
	if rise != 0 {
		b.SetRise(0)
	}
}

// glyphsOf is what Encode draws for a string: in the order written, with no
// shaping, one glyph per character — or one per part, for a character the face
// draws as its canonical decomposition. The codes then go through appendCode
// like every other path's; TestEncodeAgreesWithFormesEncode holds the result
// to forme's Encode byte for byte.
//
// A character the face has is its own glyph. For one it lacks, forme's Encode
// decides, asked about that character alone: it draws the character's
// canonical decomposition where the face has every part of it, and otherwise
// the substitute — .notdef in a composite face, the space in a simple or
// standard one. That rule (forme's drawnAs and missingByCode) is not forme's
// API, and restating it here is what let the two drift: forme began drawing
// decompositions, and setting a simple face's missing character as a space
// rather than leaving it out, while this still did neither. Asking forme per
// character keeps the cluster each part came from, which forme's whole-string
// answer does not carry and the ToUnicode CMap needs. Characters nothing is
// drawn for are skipped, as forme skips them.
func (f *Face) glyphsOf(s string) []Glyph {
	glyphs := make([]Glyph, 0, len(s))
	var byCode map[int]int // code -> glyph, for a CID-keyed face; built on first need
	for i, r := range s {
		if shape.DrawsNothing(r) {
			continue
		}
		if gid, ok := f.GlyphID(r); ok {
			glyphs = append(glyphs, Glyph{GID: gid, Cluster: i})
			continue
		}
		codes, _ := f.Face.Encode(string(r))
		if !f.composite() {
			for _, c := range codes {
				glyphs = append(glyphs, Glyph{GID: int(c), Cluster: i})
			}
			continue
		}
		for k := 0; k+1 < len(codes); k += 2 {
			code := int(codes[k])<<8 | int(codes[k+1])
			gid := code
			// A face addressed by glyph index writes the index. A CID-keyed
			// one writes the CID, and the glyph with that CID is found by
			// inverting GlyphCode: a CFF charset gives each glyph its own
			// CID, so an index whose CID is the code is the only one.
			if f.GlyphCode(code) != code {
				if byCode == nil {
					n := f.NumGlyphs()
					byCode = make(map[int]int, n)
					for g := 0; g < n; g++ {
						byCode[f.GlyphCode(g)] = g
					}
				}
				gid = byCode[code] // absent: glyph 0, .notdef, as forme wrote
			}
			glyphs = append(glyphs, Glyph{GID: gid, Cluster: i})
		}
	}
	return glyphs
}
