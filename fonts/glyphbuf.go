package fonts

import "github.com/mgilbir/pdf0/content"

// The shaped-glyph model.
//
// Shape returns spans, which can say only one thing about a glyph: move the pen
// horizontally before drawing it. That is all kerning needs and all a
// left-to-right run of unmarked Latin needs, and it is not enough for anything
// else. An accent has to sit *over* the letter it belongs to — up and across by
// an amount the font states — and a span cannot say so.
//
// So positioning produces glyphs, not spans: a glyph index, where it goes
// relative to the pen, and how far the pen then moves. Shape is written over
// this, taking the horizontal part and discarding the rest, which is why it is
// still the right call for text that carries no marks.

// Glyph is one positioned glyph of a shaped run. Distances are in thousandths
// of an em, the unit the font's own metrics are in, so they are independent of
// the size the text is finally set at.
type Glyph struct {
	// GID is the glyph to draw.
	GID int

	// Cluster is the byte offset, in the input string, of the first character
	// this glyph came from. Several glyphs may share a cluster — a letter and
	// its accent — and one glyph may stand for several characters, as a
	// ligature does. It is what maps a position in the text to a position on
	// the page, for selection, search and hit-testing.
	Cluster int

	// XAdvance is how far the pen moves after this glyph is drawn. It starts as
	// the font's own advance and is what kerning changes. A mark's is zero,
	// which is what makes it sit on the glyph before it rather than after.
	XAdvance float64

	// XOffset and YOffset displace the glyph from the pen without moving the
	// pen. This is how a mark is placed over its base.
	XOffset, YOffset float64
}

// ShapeGlyphs turns a string into positioned glyphs, applying everything this
// package reads: ligatures, contextual substitution, kerning, mark attachment,
// and the direction each part of the text runs in.
//
// The glyphs come back in *visual* order — the order the pen draws them, left to
// right — so a caller can draw them as they are, at a pen that only moves
// forward, whatever scripts the string mixes. That is not the order the string
// is written in: Hebrew and Arabic read the other way, and a PDF text-showing
// operator has no way to say so. bidi.go decides where each stretch belongs.
//
// It is the full result. Shape is the same pipeline with the vertical part
// dropped, and is enough whenever the text carries no marks.
func (f *Face) ShapeGlyphs(s string) ([]Glyph, int) {
	runs := bidiVisualRuns(s)
	if len(runs) <= 1 {
		// One direction throughout, which is nearly all text. Shaping it whole
		// keeps a ligature or a kern pair that spans the string, which cutting
		// it into runs would lose.
		rtl := len(runs) == 1 && runs[0].rtl()
		return f.shapeGlyphsIn(s, runScript(s), rtl)
	}
	var (
		out     []Glyph
		missing int
	)
	for _, r := range runs {
		piece := s[r.start:r.end]
		glyphs, gone := f.shapeGlyphsIn(piece, runScript(piece), r.rtl())
		missing += gone
		for i := range glyphs {
			glyphs[i].Cluster += r.start
		}
		out = append(out, glyphs...)
	}
	return out, missing
}

// shapeGlyphsIn is ShapeGlyphs with the run's script and direction already
// decided, and shapes one run rather than a whole string.
//
// A caller that split the text into runs knows more about a run's script than
// the run's own characters say: a stretch of digits between two Greek words is
// Greek, and shaping it as if it were scriptless would select the font's
// default rules where its Greek ones were meant. Stack.ShapeRuns made that
// decision when it cut the runs, and passes it here rather than having it
// guessed again from less. The same holds for direction, which is a property of
// the whole paragraph and cannot be read off one run of it.
func (f *Face) shapeGlyphsIn(s string, script uint16, rtl bool) ([]Glyph, int) {
	if !f.composite() {
		return f.shapeByCode(s, rtl)
	}
	// Rule L4: a bracket in a right-to-left run is drawn as the bracket that
	// mirrors it, and the substitution is on the character, before the font is
	// asked for a glyph at all.
	runes, offsets := bidiRunCharacters(s, rtl)
	// Then normalisation, which is about the characters too and has to see the
	// mirrored ones: it puts the run into the spelling this face draws best and
	// each cluster's marks into canonical order. It runs before any glyph is
	// chosen because it decides which characters the font is asked about at all.
	// See normalize.go.
	runes, offsets = f.normalize(runes, offsets, indicConfigFor(script) != nil)
	var (
		buf     []Glyph
		missing int
	)
	for i, r := range runes {
		gid, ok := f.GlyphID(r)
		if !ok {
			missing++
			gid = 0
		}
		buf = append(buf, Glyph{GID: gid, Cluster: offsets[i], XAdvance: f.advanceGID(gid)})
	}
	if len(buf) == 0 {
		return nil, missing
	}
	// The run's script decides which of the font's rules apply, and everything
	// below reads the tables through it.
	sh := shaper{f: f, l: f.layoutFor(script), rtl: rtl}
	// A script whose characters are not in the order they are drawn is shaped
	// whole by its own pass: the reordering decides which of the font's rules
	// apply where, so it cannot be a step before the general substitutions and
	// has to be the substitutions. No script both joins cursively and reorders,
	// which is why these are alternatives rather than stages.
	if out, ok := sh.shapeSyllabic(buf, runes, script); ok {
		buf = out
	} else {
		// Joining first: the joined forms are what a cursive script's ligatures
		// and contextual rules are written against. The join controls have said
		// all they have to say once it has run, and are taken out before any
		// substitution can see them — see ignorable.go.
		buf = sh.applyJoining(buf, runes)
		buf = hideJoiners(buf, runes)
		buf = sh.substitute(buf)
	}
	sh.position(buf)
	if rtl {
		// Last, and only now. Everything above is stated by the font in terms of
		// the order the text is written in; the pen will meet these glyphs in the
		// other one.
		reverseGlyphs(buf)
	}
	for _, g := range buf {
		f.used[g.GID] = true
	}
	return buf, missing
}

// shapeByCode is the shaping path for a face whose codes are characters rather
// than glyph indices: the fourteen standard faces, and any face embedded as a
// simple font.
//
// It applies no substitution and no positioning, and that is not a shortcut. A
// one-byte code addresses at most 256 glyphs, so a ligature the font has cannot
// generally be named at all; and every layout table is keyed by glyph index,
// which the code is not — looking a kern pair up by code finds either nothing
// or the wrong pair. What such a face can do correctly is one code per
// character at the width the font publishes, and that is what this does.
//
// Callers get the same Glyph values either way, so Draw, Measure and the
// fallback stack do not have to know which kind of face they were given.
func (f *Face) shapeByCode(s string, rtl bool) ([]Glyph, int) {
	runes, offsets := bidiRunCharacters(s, rtl)
	var (
		buf     []Glyph
		missing int
	)
	for i, r := range runes {
		code, ok := f.GlyphID(r)
		if !ok {
			missing++
			// The same substitution Encode makes: an unmapped character is set
			// as a space, which is what a reader shows for an undefined code.
			if space, spaceOK := f.GlyphID(' '); spaceOK {
				code = space
			} else {
				code = 0
			}
		}
		width, _ := f.Advance(r)
		if !ok {
			width, _ = f.Advance(' ')
		}
		buf = append(buf, Glyph{GID: code, Cluster: offsets[i], XAdvance: width})
	}
	if rtl {
		// There is nothing here for the direction to interfere with — no marks,
		// no joining, no kerning — but the run still has to come back in the
		// order it is drawn, so that a caller need not ask which kind of face it
		// was given.
		reverseGlyphs(buf)
	}
	for _, g := range buf {
		f.used[g.GID] = true
	}
	return buf, missing
}

// nominalAdvance is how far the text-showing operator will move the pen for one
// glyph, which is the font's own width for whatever the code names.
//
// For a composite face that is the width of the glyph index. For the others no
// positioning was applied, so the advance already in the buffer *is* the font's
// own — and asking for it by index would look the width up under a number that
// is a character code.
func (f *Face) nominalAdvance(g Glyph) float64 {
	if !f.composite() {
		return g.XAdvance
	}
	return f.advanceGID(g.GID)
}

// Draw paints shaped glyphs into a content stream at the given size.
//
// The arithmetic is the whole of it, so it is worth stating. A text-showing
// operator advances the pen by the glyph's *own* width from the font, whatever
// shaping decided; a TJ number moves it by an extra amount, subtracted. So each
// glyph needs at most two displacements: one before it, to apply its offset,
// and one after, to make the net movement the advance shaping asked for and to
// take the offset back off — an offset displaces the glyph and not the pen.
//
// A vertical offset becomes a text rise, which is the only way a text object
// can lift a glyph off the baseline without disturbing the pen. It is set back
// to zero at the end because it is graphics state and would otherwise apply to
// whatever is shown next.
//
// The builder must already be inside a text object with a font selected: what
// size, and in what font, is the caller's business and this cannot know it.
func (f *Face) Draw(b *content.Builder, glyphs []Glyph, size float64) {
	if len(glyphs) == 0 {
		return
	}
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
	move := func(d float64) {
		if d == 0 {
			return
		}
		flush()
		// TJ subtracts its number, so moving the pen forward is negative.
		b.ShowTextAdjusted(content.TextSpan{Adjust: -d})
	}

	for _, g := range glyphs {
		if g.YOffset != rise {
			flush()
			// A rise is in unscaled text-space units, so an offset in
			// thousandths of an em scales by the size the text is set at.
			b.SetRise(g.YOffset * size / 1000)
			rise = g.YOffset
		}
		move(g.XOffset)
		// Two bytes for a composite face, whose codes are glyph indices; one for
		// a simple or standard face, whose codes are WinAnsi characters. Writing
		// two where one is expected makes a reader read every pair of characters
		// as one, which is a page of nonsense rather than a subtle shift.
		if f.composite() {
			run = append(run, byte(g.GID>>8), byte(g.GID))
		} else {
			run = append(run, byte(g.GID))
		}
		// The operator will advance the pen by the font's own width; the run
		// wants to end up XAdvance further on, with the offset undone.
		move(g.XAdvance - f.nominalAdvance(g) - g.XOffset)
	}
	flush()
	if rise != 0 {
		b.SetRise(0)
	}
}

// DrawShaped shapes a string and draws it in one call, which is the common
// case: it needs the face, so it lives here rather than on the builder.
//
// The builder must already be inside a text object with this face's font
// selected at this size.
func (f *Face) DrawShaped(b *content.Builder, s string, size float64) int {
	glyphs, missing := f.ShapeGlyphs(s)
	f.Draw(b, glyphs, size)
	return missing
}

// MeasureGlyphs is the width a shaped run occupies at a given size, which is
// the sum of its advances — offsets displace glyphs without moving the pen and
// so contribute nothing.
func MeasureGlyphs(glyphs []Glyph, size float64) float64 {
	var total float64
	for _, g := range glyphs {
		total += g.XAdvance
	}
	return total * size / 1000
}

// defaultFeatures are the substitution features applied to every run, in the
// order a shaper applies them.
//
// They are the ones that are not a matter of taste. 'ccmp' composes and
// decomposes so the later rules have the glyphs they are written against;
// 'rlig' is required by the script; 'liga' and 'clig' are the ligatures a reader
// expects to see; 'calt' picks the variant that fits its neighbours. A font that
// declares them means them, which is what separates these from 'smcp' or 'onum'
// — those change what the text says it is, and wait to be asked for (ShapeWith).
//
// The order matters and is not alphabetical: composition before the rules that
// read its output, required ligatures before optional ones, contextual
// alternates last so they see the glyphs that survived.
var defaultFeatures = []string{"ccmp", "rlig", "liga", "clig", "calt"}

// substitute runs the GSUB lookups over a shaped buffer, preserving the cluster
// of the first glyph of each run it replaces so that a ligature still maps back
// to the text it came from.
func (sh shaper) substitute(buf []Glyph) []Glyph {
	for _, tag := range defaultFeatures {
		if lookups := sh.l.featureLookups[tag]; len(lookups) > 0 {
			buf = sh.applyContextual(buf, lookups)
		}
	}
	return buf
}
