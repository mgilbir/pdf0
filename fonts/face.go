// Package fonts sets text on a PDF page with a font.
//
// Shaping — turning characters into positioned glyphs, with the ligatures,
// kerning, reordering and mark attachment the font's own tables call for — is
// github.com/mgilbir/forme/shape's, and a Face here is one of its faces. It is
// not a PDF matter: the same work sets a line of Devanagari in any format that
// carries text, and it was extracted so that it could.
//
// What this package adds is the two things PDF wants that shaping does not
// decide. One is writing positioned glyphs into a content stream, where the only
// instructions available are "show these codes" and "move the pen", so
// everything shaping worked out has to be expressed as displacements around the
// glyphs. The other is writing the font itself into the document — a Type0
// font, a descendant, a descriptor, the subsetted program, a CIDSet and a
// ToUnicode CMap — so that a reader can show the page and extract its text.
package fonts

import (
	"github.com/mgilbir/forme/notosans"
	"github.com/mgilbir/forme/shape"
)

// Face is a font, as this package uses one: a shaping face with the PDF
// operations on it.
//
// The embedded face is the whole of the shaping API — ShapeGlyphs, Measure,
// Encode, GlyphID, Features and the rest — and it is exported so that a caller
// can reach it, and so that a face can be handed to something that takes
// forme's own type.
type Face struct{ *shape.Face }

// Adopt wraps a shaping face so it can be drawn and embedded.
//
// It is for a face that came from somewhere this package has no constructor
// for: one out of a cache, or one a caller built with forme directly. The face
// is not copied — the wrapper and the original are the same font, and each
// records the glyphs the other used.
func Adopt(f *shape.Face) *Face { return &Face{f} }

// Load reads a font program — TrueType, OpenType, or an sfnt carrying CFF
// outlines — as a composite face, whose character codes are glyph indices.
//
// That is the form that can set any script the font covers, because a code is
// not limited to what one byte can say, and it is the form shaping needs: a
// glyph index is what the layout tables are written about.
func Load(data []byte) (*Face, error) {
	f, err := shape.Load(data)
	if err != nil {
		return nil, err
	}
	return &Face{f}, nil
}

// LoadSimple reads a font program as a simple face, whose character codes are
// WinAnsi characters, one byte each.
//
// It sets Western European text and nothing else, and in exchange the content
// stream is half the size and the text extracts in any reader at all. Shaping
// does not apply — the codes name characters, and a font's layout tables are
// written about glyphs.
func LoadSimple(data []byte) (*Face, error) {
	f, err := shape.LoadSimple(data)
	if err != nil {
		return nil, err
	}
	return &Face{f}, nil
}

// Standard names one of the fourteen faces every PDF reader is required to
// have, so that nothing is embedded and the document carries no font at all.
//
// StandardNames lists them. They are not for PDF/A, which requires every font
// to be embedded whatever the reader is assumed to have.
func Standard(name string) (*Face, error) {
	f, err := shape.Standard(name)
	if err != nil {
		return nil, err
	}
	return &Face{f}, nil
}

// StandardNames lists the fourteen faces Standard takes.
func StandardNames() []string { return shape.StandardNames() }

// NotoSans is the bundled face: a composite face over Noto Sans, which covers
// Latin, Greek, Cyrillic, Arabic, Hebrew, Devanagari and more, so that a
// document can be written without finding a font first.
//
// Each call gets its own face. Two documents must not share one, because a face
// records the glyphs it was asked to show and that record is what decides the
// subset embedded in each.
func NotoSans() (*Face, error) {
	f, err := notosans.Face()
	if err != nil {
		return nil, err
	}
	return &Face{f}, nil
}

// NotoSansSimple is the bundled face in the simple form: one byte per
// character, WinAnsi, Western European text only.
func NotoSansSimple() (*Face, error) {
	f, err := notosans.Simple()
	if err != nil {
		return nil, err
	}
	return &Face{f}, nil
}

// Clone is a fresh face over the same parsed font, with its own record of the
// glyphs used.
//
// Parsing is the expensive part and its result never changes; what must not be
// shared is the used set, since that decides what each document embeds. So a
// second document takes a clone rather than a second parse.
func (f *Face) Clone() *Face { return &Face{f.Face.Clone()} }

// Glyph is one positioned glyph: which glyph, where in the text it came from,
// and how far it displaces and advances.
type Glyph = shape.Glyph

// Run is a stretch of text set in one face, as a Stack cuts it.
type Run = shape.Run

// Descriptor is a face's own metrics, in the font's own units.
type Descriptor = shape.Descriptor

// MeasureGlyphs is the width a shaped run occupies at a given size.
func MeasureGlyphs(glyphs []Glyph, size float64) float64 {
	return shape.MeasureGlyphs(glyphs, size)
}

// MeasureRuns is the width a sequence of runs occupies at a given size.
func MeasureRuns(runs []Run, size float64) float64 {
	return shape.MeasureRuns(runs, size)
}

// Stack sets text no one face covers, taking each character from the first face
// that has it.
type Stack = shape.Stack

// NewStack builds a stack over the given faces, in preference order.
func NewStack(faces ...*Face) *Stack {
	inner := make([]*shape.Face, len(faces))
	for i, f := range faces {
		inner[i] = f.Face
	}
	return shape.NewStack(inner...)
}

// composite reports whether the face's character codes are glyph indices — two
// bytes each, and shaped — rather than the one-byte WinAnsi characters a simple
// or standard face takes. Everything that writes a code has to know which.
func (f *Face) composite() bool { return !f.IsSimple() && !f.IsStandard() }

// scale converts a value in the font's own units to the 1/1000 em that PDF
// states lengths in.
func (f *Face) scale(v int) float64 {
	return float64(v) * 1000 / float64(f.UnitsPerEm())
}
