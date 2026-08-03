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
// package reads: ligatures, contextual substitution, kerning and mark
// attachment.
//
// It is the full result. Shape is the same pipeline with the vertical part
// dropped, and is enough whenever the text carries no marks.
func (f *Face) ShapeGlyphs(s string) ([]Glyph, int) {
	var (
		buf     []Glyph
		runes   []rune
		missing int
	)
	for i, r := range s {
		runes = append(runes, r)
		gid, ok := f.GlyphID(r)
		if !ok {
			missing++
			gid = 0
		}
		buf = append(buf, Glyph{GID: gid, Cluster: i, XAdvance: f.advanceGID(gid)})
	}
	if len(buf) == 0 {
		return nil, missing
	}
	// Joining first: the joined forms are what a cursive script's ligatures and
	// contextual rules are written against.
	buf = f.applyJoining(buf, runes)
	buf = f.substitute(buf)
	f.position(buf)
	for _, g := range buf {
		f.used[g.GID] = true
	}
	return buf, missing
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
		run = append(run, byte(g.GID>>8), byte(g.GID))
		// The operator will advance the pen by the font's own width; the run
		// wants to end up XAdvance further on, with the offset undone.
		move(g.XAdvance - f.advanceGID(g.GID) - g.XOffset)
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

// substitute runs the GSUB lookups over a shaped buffer: ligatures for now,
// preserving the cluster of the first glyph of each run it replaces so that a
// ligature still maps back to the text it came from.
func (f *Face) substitute(buf []Glyph) []Glyph {
	if len(f.layout.ligatures) == 0 {
		return buf
	}
	out := make([]Glyph, 0, len(buf))
	for i := 0; i < len(buf); {
		matched := false
		for _, lig := range f.layout.ligatures[buf[i].GID] {
			if i+len(lig.components) >= len(buf) {
				continue
			}
			ok := true
			for k, comp := range lig.components {
				if buf[i+1+k].GID != comp {
					ok = false
					break
				}
			}
			if !ok {
				continue
			}
			out = append(out, Glyph{
				GID:      lig.glyph,
				Cluster:  buf[i].Cluster,
				XAdvance: f.advanceGID(lig.glyph),
			})
			i += 1 + len(lig.components)
			matched = true
			break
		}
		if !matched {
			out = append(out, buf[i])
			i++
		}
	}
	return out
}
