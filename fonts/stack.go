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

	// Glyphs are the positioned glyphs, in the order they are drawn. Their
	// Cluster values are byte offsets into the *whole* string that was shaped,
	// not into this run — a caller mapping a glyph back to the text should not
	// have to know that runs exist.
	Glyphs []Glyph

	// Start is the byte offset in the input where this run begins — where it
	// begins in the text, that is, which in a right-to-left run is where the
	// *last* glyph drawn came from.
	Start int

	// Level is the run's bidirectional embedding level: even runs left to
	// right, odd right to left. A caller drawing the runs in the order they are
	// returned does not need it; one aligning a line, or hit-testing a click
	// back to a character, does.
	Level int
}

// ShapeRuns sets a string across the stack, returning one run per stretch of
// text that shares a face, a script and a direction, and the number of
// characters no face could set.
//
// The runs are in *visual* order — the order they are drawn, left to right —
// and cover the input exactly, so drawing them in order at a continuing pen
// position sets the text. That is not the order they are written in whenever the
// text is not all one direction, and neither is the order of the glyphs within a
// right-to-left run: this is where UAX #9 is applied, and where the reversal
// that makes Arabic and Hebrew legible happens.
//
// # Why script cuts a run as much as face does
//
// A font that covers several scripts states different rules for each, and the
// rules it states for one are wrong for another: a Greek word given the
// substitutions a font declares for Arabic is not merely unkerned but
// misspelt. So a run is a stretch that shares both — one face, one script —
// and each is shaped with what that font declares for that script.
//
// Characters that are in no script of their own — a space, a digit, a comma,
// a combining accent — take the script of what they are written among, so the
// space in the middle of a sentence does not cut it in two.
//
// # And why direction cuts one too
//
// A run is shaped by one call into one font, and a call sets one direction: the
// positioning pass has to know which way the pen will meet the glyphs it is
// placing. A stretch of Latin inside a Hebrew sentence is a run of its own for
// the same reason a stretch of Greek inside it would be.
func (s *Stack) ShapeRuns(text string) ([]Run, int) {
	if len(s.faces) == 0 || text == "" {
		return nil, 0
	}

	// The embedding levels first, because a level boundary cuts a run as surely
	// as a change of face does, and the levels are also what puts the runs into
	// the order they are drawn.
	levelRuns := bidiLogicalRuns(text)

	// One entry per base-plus-marks unit, with the face, script and level it
	// chose. The unit is the atom: a level boundary is not allowed to fall
	// between a letter and its accent, and the algorithm does not put one there
	// — rule W1 gives a mark the direction of what it is written on.
	type unit struct {
		start, end int
		face       int
		script     uint16
		level      int
	}
	var units []unit
	lr := 0
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
		for lr+1 < len(levelRuns) && levelRuns[lr].end <= i {
			lr++
		}
		units = append(units, unit{
			start: i, end: end,
			face:   s.faceFor(text[i:end], base),
			script: runScript(text[i:end]),
			level:  levelRuns[lr].level,
		})
		i = end
	}

	// A unit whose characters decide no script takes the one before it, and
	// failing that the one after — so leading punctuation joins the word it
	// introduces rather than forming a run of its own.
	last := uint16(scriptUnknown)
	for i := range units {
		if decides(units[i].script) {
			last = units[i].script
			continue
		}
		units[i].script = last
	}
	next := uint16(scriptUnknown)
	for i := len(units) - 1; i >= 0; i-- {
		if decides(units[i].script) {
			next = units[i].script
			continue
		}
		units[i].script = next
	}

	var (
		runs    []Run
		levels  []int
		missing int
	)
	for k := 0; k < len(units); {
		j := k
		for j < len(units) && units[j].face == units[k].face &&
			units[j].script == units[k].script && units[j].level == units[k].level {
			j++
		}
		start, end := units[k].start, units[j-1].end
		face := s.faces[units[k].face]
		level := units[k].level

		// The whole stretch goes to the face at once, so its ligatures, kerning
		// and joining still see the run they were written for — in the order it
		// is written, which is what those rules are stated against. The run
		// comes back in the order it is drawn.
		glyphs, gone := face.shapeGlyphsIn(text[start:end], units[k].script, level&1 == 1)
		missing += gone
		for gi := range glyphs {
			glyphs[gi].Cluster += start
		}
		runs = append(runs, Run{Face: face, Glyphs: glyphs, Start: start, Level: level})
		levels = append(levels, level)
		k = j
	}

	// Rule L2, over whole runs. Each run's glyphs are already in the order they
	// are drawn; this puts the runs themselves in it.
	order := bidiVisualOrder(levels)
	visual := make([]Run, len(runs))
	for i, k := range order {
		visual[i] = runs[k]
	}
	return visual, missing
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
