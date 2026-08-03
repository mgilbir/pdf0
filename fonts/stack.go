package fonts

import (
	"unicode"
	"unicode/utf8"
)

// Falling back from one face to the next.
//
// A Face is one font, and one font does not cover Unicode. Ask any face to set
// "café — 日本語" and some of it comes back as .notdef: a row of empty boxes
// where the text was. That is not a font problem to be solved by choosing a
// better font, it is the normal condition of setting real text, and the answer
// every text system reaches is the same — an ordered list of faces, each
// character set in the first one that has it.
//
// # Why this cannot be done per glyph
//
// The obvious implementation picks a face for each character and draws them in
// order. It is wrong twice.
//
// Shaping is per face. Ligatures, kerning, contextual substitution and joining
// are all statements a *font* makes about its *own* glyphs, and they only apply
// to a run of characters shaped together by that font. Choosing per character
// and shaping each alone loses every one of them: "fi" stops ligating, "AV"
// stops kerning, and an Arabic word falls apart into isolated letters.
//
// And a combining mark must stay with its base. If the base is in one face and
// the accent in another, the accent is positioned using anchors from a font
// that never saw the letter — so it lands in the wrong place, usually at the
// origin. So the unit of choice is a base character together with its marks,
// not a character; and consecutive units that chose the same face are shaped as
// one run, so that everything the font has to say about them still applies.

// Stack is an ordered list of faces. Each piece of text is set in the first
// face that has the characters for it.
type Stack struct {
	faces []*Face
}

// NewStack builds a fallback list. The order is the priority: the first face is
// the one text is set in wherever it can be, and the rest are what it falls
// back to. A nil face is ignored, so a caller assembling a list from optional
// sources need not filter it.
func NewStack(faces ...*Face) *Stack {
	s := &Stack{}
	for _, f := range faces {
		if f != nil {
			s.faces = append(s.faces, f)
		}
	}
	return s
}

// Faces returns the faces in priority order. A caller needs them to embed each
// one that was actually used and to name it in the page's resources.
func (s *Stack) Faces() []*Face { return s.faces }

// Run is a piece of text set in one face.
type Run struct {
	// Face is what this piece was set in.
	Face *Face

	// Glyphs are the positioned glyphs. Their Cluster values are byte offsets
	// into the *whole* string that was shaped, not into this run — a caller
	// mapping a glyph back to the text should not have to know that runs exist.
	Glyphs []Glyph

	// Start is the byte offset in the input where this run begins.
	Start int
}

// ShapeRuns sets a string across the stack, returning one run per stretch of
// text that shares a face, and the number of characters no face could set.
//
// The runs are in reading order and cover the input exactly, so drawing them in
// order at a continuing pen position sets the text.
func (s *Stack) ShapeRuns(text string) ([]Run, int) {
	if len(s.faces) == 0 || text == "" {
		return nil, 0
	}

	// One entry per base-plus-marks unit, with the face it chose.
	type unit struct {
		start, end int
		face       int
	}
	var units []unit
	for i := 0; i < len(text); {
		base, size := utf8.DecodeRuneInString(text[i:])
		end := i + size
		for end < len(text) {
			r, n := utf8.DecodeRuneInString(text[end:])
			if !unicode.Is(unicode.M, r) {
				break
			}
			end += n
		}
		units = append(units, unit{start: i, end: end, face: s.faceFor(text[i:end], base)})
		i = end
	}

	var (
		runs    []Run
		missing int
	)
	for k := 0; k < len(units); {
		j := k
		for j < len(units) && units[j].face == units[k].face {
			j++
		}
		start, end := units[k].start, units[j-1].end
		face := s.faces[units[k].face]

		// The whole stretch goes to the face at once, so its ligatures, kerning
		// and joining still see the run they were written for.
		glyphs, gone := face.ShapeGlyphs(text[start:end])
		missing += gone
		for gi := range glyphs {
			glyphs[gi].Cluster += start
		}
		runs = append(runs, Run{Face: face, Glyphs: glyphs, Start: start})
		k = j
	}
	return runs, missing
}

// faceFor chooses the face for one base-plus-marks unit.
//
// A face that has the whole unit is preferred over one that has only the base,
// because a mark taken from a different font than its letter is positioned by
// anchors that never saw the letter. Only when no face has all of it does the
// base decide, and if none has even that, the first face sets it — where it
// becomes .notdef, which is a visible box rather than a silently dropped
// character.
func (s *Stack) faceFor(unitText string, base rune) int {
	for i, f := range s.faces {
		complete := true
		for _, r := range unitText {
			if _, ok := f.GlyphID(r); !ok {
				complete = false
				break
			}
		}
		if complete {
			return i
		}
	}
	for i, f := range s.faces {
		if _, ok := f.GlyphID(base); ok {
			return i
		}
	}
	return 0
}

// Covers reports whether any face in the stack has a glyph for a character.
// It is what a caller asks before deciding to add another fallback.
func (s *Stack) Covers(r rune) bool {
	for _, f := range s.faces {
		if _, ok := f.GlyphID(r); ok {
			return true
		}
	}
	return false
}

// MeasureRuns is the width a set of runs occupies at a given size.
func MeasureRuns(runs []Run, size float64) float64 {
	var total float64
	for _, r := range runs {
		total += MeasureGlyphs(r.Glyphs, size)
	}
	return total
}

// Measure is the width the stack sets a string in, at a given size. It shapes
// the text to answer, because the width of text with fallback is not the sum of
// its characters' widths in any one font.
func (s *Stack) Measure(text string, size float64) float64 {
	runs, _ := s.ShapeRuns(text)
	return MeasureRuns(runs, size)
}
