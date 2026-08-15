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
	"github.com/mgilbir/forme/font"
	"github.com/mgilbir/forme/fonts/notosans"
	"github.com/mgilbir/forme/shape"
)

// Face is a font, as this package uses one: a shaping face with the PDF
// operations on it.
//
// The embedded face is the whole of the shaping API — ShapeGlyphs, Measure,
// Encode, GlyphID, Features and the rest — and it is exported so that a caller
// can reach it, and so that a face can be handed to something that takes
// forme's own type.
type Face struct {
	*shape.Face

	// cidKeyed is the CFF inside this face numbering its glyphs by CID, with
	// its charset mapping CID to glyph index — so the two are different
	// numberings.
	//
	// It is recorded here, from the bytes, because forme does not report it and
	// the subset this package would otherwise ask is not a reliable place to
	// look: subsetting such a font fails today for its own reasons, and a
	// refusal that rests on another component's failure stops being a refusal
	// the moment that component improves. See refuseCIDKeyed.
	//
	// False for a face from Adopt, which is handed a shaping face and never the
	// program it was read from. That is a known gap and is stated on Adopt.
	cidKeyed bool

	// gidToCID maps each glyph index to the CID that reaches it through the
	// font's charset, and registry/ordering/supplement name the collection
	// those CIDs are numbered in. All four are read from the program at load
	// and are nil or empty for anything that is not a CID-keyed CFF.
	//
	// A PDF needs them to embed one: /W is keyed by CID, and /CIDSystemInfo has
	// to state the collection rather than assume it. See embed.go.
	gidToCID   []int
	registry   string
	ordering   string
	supplement int
}

// Adopt wraps a shaping face so it can be drawn and embedded.
//
// It is for a face that came from somewhere this package has no constructor
// for: one out of a cache, or one a caller built with forme directly. The face
// is not copied — the wrapper and the original are the same font, and each
// records the glyphs the other used.
// A face adopted this way is never refused as CID-keyed, because the check is
// made from the font program and this is handed a face rather than the bytes it
// was read from. A caller embedding a CID-keyed CFF adopted from elsewhere gets
// the wrong /W; use Load, which has the program and looks.
func Adopt(f *shape.Face) *Face { return &Face{Face: f} }

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
	face := &Face{Face: f}
	face.readCIDKeying(data)
	return face, nil
}

// readCIDKeying records what a CID-keyed CFF needs to be embedded correctly:
// which CID reaches each glyph, and the collection those CIDs belong to.
//
// A CFF declares itself CID-keyed with the ROS operator, which is also what
// font.ParseCFF reads to build GIDToCID; a font with no CFF table at all —
// every TrueType — is not one, and everything here stays zero.
//
// It is read here, from the program, rather than at embed time from the subset.
// The subset carries the same charset, so either would do today; doing it here
// means the answer does not depend on subsetting having succeeded, and a face
// that cannot be subsetted still knows what it is.
func (f *Face) readCIDKeying(data []byte) {
	cff := font.SFNTTables(data)["CFF "]
	if cff == nil {
		return
	}
	p := font.ParseCFF(cff)
	if p == nil || p.GIDToCID == nil {
		return
	}
	f.cidKeyed = true
	f.gidToCID = p.GIDToCID
	f.registry, f.ordering, f.supplement = p.Registry, p.Ordering, p.Supplement
}

// cidOf is the CID that reaches a glyph, which for everything but a CID-keyed
// CFF is the glyph index itself — that is what "the code is the glyph index"
// means, and what Identity ordering says.
func (f *Face) cidOf(gid int) int {
	if gid >= 0 && gid < len(f.gidToCID) {
		return f.gidToCID[gid]
	}
	return gid
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
	return &Face{Face: f}, nil
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
	return &Face{Face: f}, nil
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
	return &Face{Face: f}, nil
}

// NotoSansSimple is the bundled face in the simple form: one byte per
// character, WinAnsi, Western European text only.
func NotoSansSimple() (*Face, error) {
	f, err := notosans.Simple()
	if err != nil {
		return nil, err
	}
	return &Face{Face: f}, nil
}

// NotoSansLicense is the text of the SIL Open Font License 1.1 as it is
// distributed with the bundled font, including the copyright line.
//
// It is exposed because the licence requires it to travel with the font, and a
// program that embeds the font in something it ships may need to reproduce it —
// in an about box, a credits file, a --licenses flag. Reading it off disk is not
// an option for a single binary, so it is compiled in.
func NotoSansLicense() string { return notosans.License() }

// Clone is a fresh face over the same parsed font, with its own record of the
// glyphs used.
//
// Parsing is the expensive part and its result never changes; what must not be
// shared is the used set, since that decides what each document embeds. So a
// second document takes a clone rather than a second parse.
func (f *Face) Clone() *Face {
	c := *f
	c.Face = f.Face.Clone()
	return &c
}

// Glyph is one positioned glyph: which glyph, where in the text it came from,
// and how far it displaces and advances.
type Glyph = shape.Glyph

// Run is a stretch of text set in one face, as a Stack cuts it.
type Run = shape.Run

// Descriptor is a face's own metrics, in the font's own units.
type Descriptor = shape.Descriptor

// Metric names a metric a font may or may not state, for Descriptor.Declared.
//
// The distinction is the point of it. A font that states a line gap of zero and
// a font with no hhea table at all both report zero, and a renderer that could
// not tell them apart would space its lines by a number it believed came from
// the font. Every one of these is a metric this engine would otherwise guess.
type Metric = shape.Metric

const (
	MetricLineGap     = shape.MetricLineGap
	MetricTypoMetrics = shape.MetricTypoMetrics
	MetricXHeight     = shape.MetricXHeight
	MetricCapHeight   = shape.MetricCapHeight
	MetricUnderline   = shape.MetricUnderline
	MetricStrikeout   = shape.MetricStrikeout
	MetricWeight      = shape.MetricWeight
)

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
