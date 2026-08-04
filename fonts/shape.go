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
	var out []content.TextSpan
	for _, run := range bidiVisualRuns(s) {
		piece := s[run.start:run.end]
		glyphs, gone := f.glyphRunIn(piece, run.rtl())
		missing += gone
		if len(glyphs) == 0 {
			continue
		}
		sh := shaper{f: f, l: f.layoutFor(runScript(piece)), rtl: run.rtl()}
		out = append(out, sh.shapeGlyphs(glyphs)...)
	}
	return out, missing
}

// shapeGlyphs turns a glyph run into spans: ligatures first, then kerning, then
// the codes and displacements a text operator takes.
//
// The glyphs arrive in the order the text is written and leave in the order it
// is drawn. Ligatures are applied before the reversal, because a font's
// ligatures are stated over the written order; the kerning is then looked up by
// the pair as the font states it, which for a reversed run is the pair the other
// way round from the one the pen sees.
func (sh shaper) shapeGlyphs(glyphs []int) []content.TextSpan {
	glyphs = sh.applyLigatures(glyphs)
	if sh.rtl {
		for i, j := 0, len(glyphs)-1; i < j; i, j = i+1, j-1 {
			glyphs[i], glyphs[j] = glyphs[j], glyphs[i]
		}
	}

	var (
		run  []byte
		out  []content.TextSpan
		prev = -1
	)
	for _, gid := range glyphs {
		if prev >= 0 && !sh.l.ignores(sh.l.kernFlags, gid) {
			if k, ok := sh.l.kern[sh.kernPair(prev, gid)]; ok && k != 0 {
				// Flush what has accumulated, then the displacement. The sign
				// flips: a negative kern closes the gap, and TJ subtracts.
				out = append(out, content.TextSpan{Codes: run})
				out = append(out, content.TextSpan{Adjust: -sh.f.scale(k)})
				run = nil
			}
		}
		run = append(run, byte(gid>>8), byte(gid))
		sh.f.used[gid] = true
		// A glyph the kerning lookup ignores does not become the left half of
		// the next pair either: the pair is between the glyphs either side of
		// it, which is the whole point of the flag.
		if !sh.l.ignores(sh.l.kernFlags, gid) {
			prev = gid
		}
	}
	if len(run) > 0 {
		out = append(out, content.TextSpan{Codes: run})
	}
	return out
}

// MeasureShaped is the width of a shaped string at the given size, in
// user-space units. It includes the ligature substitutions and the kerning, so
// it is what Shape will actually occupy — unlike Measure, which sums the runes
// as written.
//
// Direction does not change a width — a line occupies the same space whichever
// way it is read — but it changes where the run boundaries fall, and a ligature
// or a kern pair does not cross one. So the same cuts are made here as in Shape,
// or the two would disagree on mixed text and a caller would lay out to one
// width and draw another.
func (f *Face) MeasureShaped(s string, size float64) float64 {
	if !f.composite() {
		// Nothing is substituted or kerned for a face whose codes are
		// characters, so what Shape occupies is what Measure says. It has to
		// agree with Shape or a caller lays out to one width and draws another
		// — and it would otherwise ask a face with no font program for a width
		// by glyph index.
		return f.Measure(s, size)
	}
	var total float64
	for _, run := range bidiVisualRuns(s) {
		piece := s[run.start:run.end]
		sh := shaper{f: f, l: f.layoutFor(runScript(piece)), rtl: run.rtl()}
		glyphs, _ := f.glyphRunIn(piece, run.rtl())
		glyphs = sh.applyLigatures(glyphs)
		prev := -1
		for _, gid := range glyphs {
			if prev >= 0 && !sh.l.ignores(sh.l.kernFlags, gid) {
				total += f.scale(sh.l.kern[[2]int{prev, gid}])
			}
			total += f.advanceGID(gid)
			if !sh.l.ignores(sh.l.kernFlags, gid) {
				prev = gid
			}
		}
	}
	return total * size / 1000
}

// kernPair names a kern pair the way the font states it: in the order the text
// is written. A right-to-left run has been reversed by the time the pen walks
// it, so the glyph the pen meets second is the one the font calls first.
func (sh shaper) kernPair(prev, gid int) [2]int {
	if sh.rtl {
		return [2]int{gid, prev}
	}
	return [2]int{prev, gid}
}

// glyphRun maps runes to glyph indices, substituting .notdef for any the font
// does not cover, exactly as Encode does.
func (f *Face) glyphRun(s string) (glyphs []int, missing int) {
	return f.glyphRunIn(s, false)
}

// glyphRunIn is glyphRun for one run of a known direction, applying rule L4
// where it runs right to left: a bracket in right-to-left text is drawn as the
// bracket that mirrors it.
func (f *Face) glyphRunIn(s string, rtl bool) (glyphs []int, missing int) {
	runes, _ := bidiRunCharacters(s, rtl)
	for _, r := range runes {
		if hiddenBeforeShaping(r) {
			continue // nothing is drawn for it — see ignorable.go
		}
		gid, ok := f.GlyphID(r)
		if !ok {
			missing++
			gid = 0
		}
		glyphs = append(glyphs, gid)
	}
	return glyphs, missing
}

// applyLigatures replaces runs of glyphs with the single glyph the font defines
// for them, preferring the longest match so that ffi wins over ff.
//
// This is the span path's own ligature pass, over the flattened table. The glyph
// path (ShapeGlyphs) goes through the lookup list instead, which honours lookup
// flags and can be invoked from a contextual rule; the two agree on plain text,
// which is all the span path is for.
func (sh shaper) applyLigatures(glyphs []int) []int {
	if len(sh.l.ligatures) == 0 {
		return glyphs
	}
	out := make([]int, 0, len(glyphs))
	for i := 0; i < len(glyphs); {
		matched := false
		for _, lig := range sh.l.ligatures[glyphs[i]] {
			// components are the glyphs after the first, so the run needs
			// len(components) more glyphs to exist beyond i.
			if i+len(lig.components) >= len(glyphs) {
				continue
			}
			ok := true
			for k, comp := range lig.components {
				if glyphs[i+1+k] != comp {
					ok = false
					break
				}
			}
			if !ok {
				continue
			}
			out = append(out, lig.glyph)
			i += 1 + len(lig.components)
			matched = true
			break
		}
		if !matched {
			out = append(out, glyphs[i])
			i++
		}
	}
	return out
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
	var out []content.TextSpan
	for _, run := range bidiVisualRuns(s) {
		piece := s[run.start:run.end]
		sh := shaper{f: f, l: f.layoutFor(runScript(piece)), rtl: run.rtl()}
		glyphs, gone := f.glyphRunIn(piece, run.rtl())
		missing += gone
		for _, tag := range features {
			table := sh.l.single[tag]
			if table == nil {
				continue
			}
			for i, gid := range glyphs {
				if to, ok := table[gid]; ok {
					glyphs[i] = to
				}
			}
		}
		if len(glyphs) == 0 {
			continue
		}
		out = append(out, sh.shapeGlyphs(glyphs)...)
	}
	return out, missing
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
