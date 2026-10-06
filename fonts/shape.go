package fonts

import "github.com/mgilbir/pdf0/content"

// Writing shaped glyphs into a content stream.
//
// A text-showing operator can do two things: show a string of codes, and move
// the pen along the line. So everything shaping decided horizontally comes
// through exactly — a kern, a contextual advance, a mark's zero width, the
// horizontal half of where a mark sits — expressed as displacements around the
// glyphs. What it cannot do is move the pen across the line, which is why Draw
// exists beside Shape and sets a rise.

// Shape maps a string to the spans content.Builder.ShowTextAdjusted takes,
// applying the font's own ligatures, kerning and everything else shaping
// decides.
//
// The displacements it emits are in thousandths of text space and follow the TJ
// convention, where a positive number moves the following glyphs *closer*
// (ISO 32000-2 9.4.3). A kern of -40 font units — pulling a pair together —
// therefore appears as a positive 40 here. That inversion is the single most
// error-prone step in setting kerned text, which is why it happens once, in one
// place, with a test that names the sign.
//
// The second result counts runes the font has no glyph for, as Encode's does.
//
// The text extracts back as it was written. A ligature's ToUnicode entry is
// the characters it was drawn for — "ffi", not the U+FB03 the font's cmap
// happens to register the glyph under — and so is a conjunct's or a positional
// form's; a stretch no per-glyph entry can say, such as a right-to-left word
// or a glyph already mapped to other text, is wrapped in an /ActualText
// between ActualTextStart and ActualTextEnd spans. See plan in draw.go.
//
// # What a text operator cannot say
//
// This shapes the text exactly as ShapeGlyphs does — it is the same call — and
// then writes the result as spans. A span sequence can show codes and move the
// pen along the line, and everything shaping decides horizontally therefore
// survives.
//
// It cannot move the pen *across* the line, and one thing shaping decides needs
// that: how far above or below the baseline a mark sits. Over the corpus in
// forme's testdata/harfbuzz that is 475 strings in 5911 — stacked accents,
// Devanagari vowel signs, anything a font places vertically rather than by
// advance. Their marks land on the baseline here.
//
// So: Shape for text that is a line of letters, which is most text and where
// spans are the smaller and simpler thing to write. DrawShaped, which places
// each glyph, for text that carries marks. MeasureShaped agrees with both,
// because a width does not depend on the vertical.
//
// A face whose glyphs are only bitmaps is several Type 3 fonts (see
// SubfontName), and spans show codes in one of them: its home font, painted
// from its largest strike with no colour of the text's. A glyph that font has
// no code left for is counted in the second result and not shown. Draw and
// DrawShaped pick each glyph's font by size and colour.
func (f *Face) Shape(s string) (spans []content.TextSpan, missing int) {
	glyphs, missing := f.ShapeGlyphs(s)
	spans, dropped := f.spans(glyphs, s)
	return spans, missing + dropped
}

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
// Features returns what a face actually offers. A simple or standard face has
// no layout tables to apply them from, and sets the text as Shape does.
func (f *Face) ShapeWith(s string, features ...string) (spans []content.TextSpan, missing int) {
	glyphs, missing := f.ShapeGlyphsWith(s, features...)
	spans, dropped := f.spans(glyphs, s)
	return spans, missing + dropped
}

// Encode maps a string to character codes, one glyph per character with no
// shaping, which is what content.Builder.ShowText takes.
//
// It is the embedded face's Encode, with the text recorded: a composite face's
// ToUnicode CMap is written from what each glyph was drawn for, and a caller
// using this has told the face exactly that. A character the face lacks is
// drawn as its canonical decomposition where the face has every part of it (é
// as e and U+0301), as shaping and Measure treat it. Otherwise it is .notdef
// in a composite face and the space in a simple or standard one, which keeps
// its place in the text, and the count of those is the second result.
//
// A face whose glyphs are only bitmaps writes codes in its home font, as Shape
// does, and counts a glyph that font has no code left for as missing.
//
// Bare codes cannot carry an /ActualText, which is the one thing this cannot
// say that Draw can: a glyph the font's cmap reaches from two characters — 日
// and the radical ⽇ are one glyph in a CJK face — extracts as the character
// the CMap names it by, whichever of the two was written here.
func (f *Face) Encode(s string) (codes []byte, missing int) {
	// forme's Encode is what records the glyphs the subset keeps; its codes are
	// the same bytes appendCode writes (TestEncodeAgreesWithFormesEncode), and
	// are written here by appendCode so that one function writes every code
	// this package emits.
	_, missing = f.Face.Encode(s)
	glyphs := f.glyphsOf(s)
	f.plan(glyphs, s)
	codes = make([]byte, 0, 2*len(glyphs))
	for _, g := range glyphs {
		if f.isType3() {
			c, ok := f.homeCode(g.GID)
			if !ok {
				missing++
				continue
			}
			codes = append(codes, c)
			continue
		}
		codes = f.appendCode(codes, g.GID)
	}
	return codes, missing
}

// Draw writes shaped glyphs to a content stream, placing each one where shaping
// put it.
//
// It is the call that loses nothing: an offset across the line becomes a rise,
// which spans cannot express, so text carrying marks comes out where the font
// says rather than on the baseline.
//
// text is the string the glyphs were shaped from — their Cluster offsets are
// byte offsets into it — and it is what a reader extracting the page gets back.
// Each glyph's ToUnicode entry is written from the part of text it was drawn
// for, and a stretch whose glyphs cannot say it glyph by glyph — a right-to-left
// run, a vowel sign drawn before its consonant, a glyph that already stands for
// something else — is marked with an /ActualText. The glyphs must be this face's,
// from one of its shaping calls: that is what records them for the subset.
//
// The builder must already be inside a text object with this face's font
// selected at this size.
//
// A face whose glyphs are only bitmaps is drawn as Type 3 fonts: each glyph in
// the one for the strike the size picks and, for a greyscale strike, the fill
// colour the builder last set, selected as SubfontName of the name the builder
// selected for the face, which is selected again after the run. A builder that
// has selected no font is failed. A glyph forme cannot paint fails the
// builder too, rather than being drawn blank.
func (f *Face) Draw(b *content.Builder, text string, glyphs []Glyph, size float64) {
	f.draw(b, glyphs, text, size)
}

// DrawShaped shapes a string and draws it in one call, which is the common
// case. It returns the count of runes the font has no glyph for.
func (f *Face) DrawShaped(b *content.Builder, s string, size float64) int {
	glyphs, missing := f.ShapeGlyphs(s)
	f.draw(b, glyphs, s, size)
	return missing
}

// DrawUpright writes glyphs set upright down the page — a run shaped with
// shape.Features.Vertical — placing each one where shaping hung it.
//
// The pen starts at the origin of the current text matrix and moves down it:
// each glyph's YAdvance, which is negative, is how far. A glyph is hung from
// its vertical origin (VOriginX, VOriginY, measured from its horizontal
// origin: half its width across, and from the top of its em box down to
// where the font puts its baseline), and moved from there by its XOffset and
// YOffset, as the font's positioning decided. So its horizontal origin is
// drawn at the pen plus (XOffset-VOriginX, YOffset-VOriginY), in thousandths
// of an em at size. For a CJK face the pen is the top centre of each glyph's
// ideographic em box. The text matrix must not be turned: the glyphs stand as
// they do in the font.
//
// How it is written depends on the form of the face, which must be the font
// the builder has selected, at this size:
//
//   - A vertical form (see Vertical) is the standard way: an Identity-V font,
//     whose /W2 states each glyph's own vertical advance and origin, so each
//     code moves the pen down by the glyph's advance and hangs it from its
//     origin, and only where shaping moved a glyph from those — a mark's
//     advance taken away, 'vkrn', 'vpal', an offset — is a displacement
//     written: a TJ number down the line, a move of the line across it. A
//     reader knows the text is vertical.
//   - Any other face is written in its horizontal font, which is the only
//     font a simple or standard face has: every glyph placed explicitly,
//     with a displacement along the line and a rise across it. The page
//     shows the same glyphs in the same places; a reader sees horizontal
//     text arranged in a column.
//
// text is the string the glyphs were shaped from, as for Draw, and it is what
// a reader extracting the page gets back, in the order it was written: the
// run is one /ActualText, because where the glyphs stand is not where a
// reader rebuilding the text from positions looks for word breaks — a
// letter-spaced column, a glyph centred in a wider em, a mark moved across
// the line. The ToUnicode CMap says what each glyph means, as for every other
// path.
func (f *Face) DrawUpright(b *content.Builder, text string, glyphs []Glyph, size float64) {
	actual := visible(text)
	f.drawUpright(b, text, glyphs, size, actual, actual != "")
}

// DrawReplaced is Draw for glyphs that do not stand in the page's text for the
// string they were shaped from, and says what they stand for instead: the run
// is one /ActualText saying actual (ISO 32000-2 14.9.4), with none inside it.
//
// actual may be empty, which says the glyphs are no text at all: a text
// shadow, an emphasis mark — ink that repeats or decorates the text and is
// not a character of the document. A reader that honours /ActualText, pdf0's
// extractor among them, reads nothing for them. It is also what a set of
// glyphs that stand for one character says, when shaping did not make them
// from it: the pieces of a stretched bracket, each placed where a formula put
// it and none reached from the character by the font's cmap, are the bracket
// once.
//
// text is what the glyphs were shaped from, if anything — their Cluster
// offsets are byte offsets into it — and is what the ToUnicode CMap is written
// from, as for Draw: a shadow's glyphs are the text's own glyphs, and mean
// what they mean there.
func (f *Face) DrawReplaced(b *content.Builder, text string, glyphs []Glyph, size float64, actual string) {
	f.drawReplaced(b, glyphs, text, size, actual, true, false)
}

// DrawUprightReplaced is DrawUpright for glyphs that stand for actual in the
// page's text, as DrawReplaced is Draw.
func (f *Face) DrawUprightReplaced(b *content.Builder, text string, glyphs []Glyph, size float64, actual string) {
	f.drawUpright(b, text, glyphs, size, actual, true)
}

func (f *Face) drawUpright(b *content.Builder, text string, glyphs []Glyph, size float64, actual string, wrap bool) {
	if f.IsVertical() {
		f.drawReplaced(b, glyphs, text, size, actual, wrap, true)
		return
	}
	placed := make([]Glyph, len(glyphs))
	pen := 0.0
	for i, g := range glyphs {
		p := g
		// The pen does not move along the line: each glyph is placed from
		// the run's origin by its offsets, and draw takes back the width the
		// text operator advances by.
		p.XAdvance = 0
		p.XOffset = g.XOffset - g.VOriginX
		p.YOffset = pen + g.YOffset - g.VOriginY
		p.YAdvance, p.VOriginX, p.VOriginY = 0, 0, 0
		placed[i] = p
		pen += g.YAdvance
	}
	f.drawReplaced(b, placed, text, size, actual, wrap, false)
}
