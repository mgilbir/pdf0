package fonts

import "github.com/mgilbir/pdf0/content"

// Turning a string into positioned glyphs: ligature substitution, then pair
// kerning, then the character codes and displacements a PDF text operator takes.

// Shape maps a string to the spans content.Builder.ShowTextAdjusted takes,
// applying the font's own ligatures and kerning.
//
// The displacements it emits are in thousandths of text space and follow the
// TJ convention, where a positive number moves the following glyphs *closer*
// (ISO 32000-2 9.4.3). A kern of -40 font units — pulling a pair together —
// therefore appears as a positive 40 here. That inversion is the single most
// error-prone step in setting kerned text, which is why it happens once, in one
// place, with a test that names the sign.
//
// The second result counts runes the font has no glyph for, as Encode's does.
//
// Ligatures change what the text extracts back to unless the font says
// otherwise: a run replaced by one glyph has one ToUnicode entry, and that
// entry maps to the single character the ligature glyph is registered for. For
// the standard Latin ligatures a font's cmap does map them (U+FB01 for fi and
// so on), so extraction gives "ﬁ" rather than "fi" — searchable in a reader
// that normalises, not in one that does not. Shape is therefore opt-in, and
// Encode without it stays the choice for text that must extract literally.
//
// # What a text operator cannot say
//
// This shapes the text exactly as ShapeGlyphs does — it is the same call — and
// then writes the result as spans. A span sequence can show codes and move the
// pen along the line, and everything shaping decides horizontally therefore
// survives: the kerning, the advance a contextual rule chose, the zero width of
// a mark, and where along the line a mark sits.
//
// It cannot move the pen *across* the line, and one thing shaping decides needs
// that: how far above or below the baseline a mark sits. Over the corpus in
// testdata/harfbuzz that is 475 strings in 5911 — stacked accents, Devanagari
// vowel signs, anything a font places vertically rather than by advance. Their
// marks land on the baseline here.
//
// So: Shape for text that is a line of letters, which is most text and where
// spans are the smaller and simpler thing to write. DrawShaped, which places
// each glyph, for text that carries marks. MeasureShaped agrees with both,
// because a width does not depend on the vertical.
func (f *Face) Shape(s string) (spans []content.TextSpan, missing int) {
	if !f.composite() {
		// A simple or standard face encodes one byte per character, and its
		// codes name nothing in the layout tables — so there is no shaping to
		// do, and the honest answer is the plain encoding. Returning it as a
		// single span keeps the shape of the result the same whichever kind of
		// face a caller was handed.
		//
		// Direction is not applied here, and that is a stated limit rather than
		// an oversight: a simple face encodes one byte per character through
		// WinAnsi, which has no right-to-left script in it at all. Text that
		// needs reordering needs a composite face to have the letters, and
		// ShapeGlyphs is the call for it.
		codes, missing := f.Encode(s)
		if len(codes) == 0 {
			return nil, missing
		}
		return []content.TextSpan{{Codes: codes}}, missing
	}
	glyphs, missing := f.ShapeGlyphs(s)
	return f.spansFromGlyphs(glyphs), missing
}

// spansFromGlyphs turns positioned glyphs into the spans a text operator takes.
//
// TJ can do two things: show codes, and move the pen. So everything shaping
// decided horizontally comes through exactly — a kern, a contextual advance, a
// mark's zero width, the horizontal half of where a mark sits — expressed as
// displacements around the glyphs.
//
// Two of them per glyph, at most, and usually none:
//
//   - An offset displaces a glyph *without* moving the pen, so it is put in
//     before the glyph and taken back out after.
//   - The pen has moved by the advance the font's own /W array states, which is
//     not what shaping decided, so the difference comes off.
//
// The two are emitted as one number where they meet, because a displacement is
// three bytes of content stream and a page has thousands of them.
func (f *Face) spansFromGlyphs(glyphs []Glyph) []content.TextSpan {
	var (
		out []content.TextSpan
		run []byte
	)
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
	for _, g := range glyphs {
		adjust(g.XOffset)
		run = append(run, byte(g.GID>>8), byte(g.GID))
		f.used[g.GID] = true
		// Take the offset back out, and correct the font's advance to the one
		// shaping decided. Both move the pen the other way, so they are one
		// number.
		adjust(g.XAdvance - f.advanceGID(g.GID) - g.XOffset)
	}
	flush()
	return out
}

// MeasureShaped is the width of a shaped string at the given size, in
// user-space units.
//
// It is what the text will occupy on the page: the same shaping Shape and
// DrawShaped do, measured rather than drawn. That is the whole contract, and it
// is the reason this measures by shaping rather than by a cheaper approximation
// of it. A layout engine measures a word to decide whether it fits the line and
// then draws it; if the two disagree the line is filled to one width and painted
// at another, and nothing in either call's own output shows it.
//
// This used to sum a flattened ligature table and a kerning map, which is most
// of shaping and not all of it — no contextual substitution, no syllabic
// reordering, no positioning beyond pair kerning. Over the HarfBuzz corpus that
// was wrong for 1920 of 5911 strings, by up to 17% on a Devanagari conjunct.
func (f *Face) MeasureShaped(s string, size float64) float64 {
	if !f.composite() {
		// Nothing is substituted or kerned for a face whose codes are
		// characters, so what it occupies is what Measure says — and asking the
		// shaped path would want a width by glyph index from a face that has no
		// font program to give one.
		return f.Measure(s, size)
	}
	glyphs, _ := f.ShapeGlyphs(s)
	return MeasureGlyphs(glyphs, size)
}

// HasKerning reports whether the font carries pair kerning this package could
// read. A caller laying out text can use it to decide whether shaping is worth
// the extra spans, and a test can use it to notice a font whose kerning went
// unread.
func (f *Face) HasKerning() bool { return len(f.layout.kern) > 0 }

// HasLigatures reports whether the font carries ligature substitutions this
// package could read.
func (f *Face) HasLigatures() bool { return len(f.layout.ligatures) > 0 }

// ShapeWith is Shape with additional OpenType features applied by name — the
// one-for-one substitutions a font offers and a caller must ask for, such as
// "smcp" for small capitals or "onum" for oldstyle figures.
//
// They are opt-in because they are not corrections: a font's 'smcp' is right
// only where small capitals were wanted, and applying it by default would
// change text nobody asked to change. 'liga' and kerning are applied either
// way, being what the font says its letters should look like when set normally.
//
// A feature the font does not declare is silently no-op — asking for small
// capitals from a face that has none should set the text plainly, not fail.
// Features returns what a face actually offers.
func (f *Face) ShapeWith(s string, features ...string) (spans []content.TextSpan, missing int) {
	glyphs, missing := f.shapeGlyphsWith(s, features)
	return f.spansFromGlyphs(glyphs), missing
}

// Features lists the substitution features this face offers by name, sorted.
// A caller can present them, or check one before asking for it.
func (f *Face) Features() []string {
	out := make([]string, 0, len(f.layout.single))
	for tag := range f.layout.single {
		out = append(out, tag)
	}
	sortStrings(out)
	return out
}

func sortStrings(a []string) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}
