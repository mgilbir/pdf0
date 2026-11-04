package fonts

import "errors"

// Drawing a face into a content stream this package does not write.
//
// Draw writes glyphs into a content.Builder, and on the way it records what
// each glyph was drawn for, which is what the ToUnicode CMap is written from,
// and decides which stretches need an /ActualText (plan, draw.go). A caller
// that writes its own content stream — its own BT and ET, its own font
// selection and positioning — still needs both decisions, and needs them to be
// this package's: the CMap Embed writes says what the record says, and a
// stream whose /ActualText stretches were decided some other way extracts as
// something else. Before Record such a caller either drew every run into a
// builder it threw away, or marked glyphs through forme's Use and got a CMap
// that knew each glyph only by the character the font's cmap names it by: a
// ligature, a conjunct or a glyph no character maps to extracted as nothing,
// and a radical drawn for its ideograph as the ideograph.
//
// Record is plan, exported: the same function Draw, Shape and Encode call, so
// a run recorded here and one drawn there leave the face in the same state.
// AppendCode and NominalAdvance are the other two things Draw's emitter
// uses — the bytes a glyph is shown with and the width the font dictionary
// gives it — so a caller writing the stream itself writes what Draw would.
// TestRecordingAndDrawingAreOneImplementation holds the two to the same
// content stream and the same embedded font, byte for byte.

// Segment is a stretch of a recorded run: the glyphs [Start, End) of the run,
// and whether they are written inside a marked-content sequence that carries
// what they say (ISO 32000-2 14.9.4):
//
//	/Span <</ActualText (...)>> BDC ... EMC
//
// The stretches cover the run, in order. A stretch that is not Marked is
// shown as it is: the ToUnicode CMap already says what its glyphs mean.
type Segment struct {
	Start, End int
	// Marked says the stretch is written inside an /ActualText, and
	// ActualText is its text, which may be empty: glyphs that stand for no
	// text at all, a mark the shaper inserted, say so. It is written as a
	// PDF text string (content.ActualTextStart writes one).
	Marked     bool
	ActualText string
}

// ErrDrawnAsType3 is Record or AppendCode asked of a face written as Type 3
// fonts: its glyphs are given codes, in one of several fonts, as they are
// drawn, by the size and the fill colour (see SubfontName), which only Draw
// knows. Such a face is drawn with Draw.
var ErrDrawnAsType3 = errors.New("fonts: a face written as Type 3 fonts gives its glyphs " +
	"their codes as it draws them; draw it with Draw")

// Record does to the face what Draw does, without drawing: it marks the
// glyphs used, so that Embed keeps them, records what each one was drawn for,
// which the ToUnicode CMap says, and returns the stretches of the run and
// which of them need an /ActualText. text and glyphs are as for Draw: the
// string the glyphs were shaped from, whose byte offsets their Cluster fields
// are, and the glyphs in the order they are drawn.
//
// The caller then writes each glyph's code (AppendCode) in its own content
// stream, in its own font selection, moving the pen as it does with Draw: the
// font dictionary advances it by NominalAdvance for each glyph, and what
// shaping placed differently is the caller's displacement to add. A stretch
// that is Marked is written inside the marked-content sequence Segment
// describes.
//
// What a glyph means is fixed the first time it is recorded, by Record or by
// Draw, and every later run is checked against it, so runs are recorded in
// the order they are written. Embed comes after the last of them, as it does
// after the last Draw.
//
// A face written as Type 3 fonts is refused with ErrDrawnAsType3.
func (f *Face) Record(text string, glyphs []Glyph) ([]Segment, error) {
	if f.isType3() {
		return nil, ErrDrawnAsType3
	}
	segs := f.plan(glyphs, text)
	out := make([]Segment, len(segs))
	for i, s := range segs {
		out[i] = Segment{Start: s.lo, End: s.hi, Marked: s.marked}
		if s.marked {
			out[i].ActualText = s.actual
		}
	}
	return out, nil
}

// AppendCode appends to dst the character code a glyph is shown with in this
// face's font: two bytes for a composite face — the glyph index, or the CID
// for a CID-keyed CFF — and one WinAnsi byte for a simple or standard face. It
// is the function every way of drawing in this package writes its codes with,
// so /W, /CIDSet and the ToUnicode CMap are keyed by the same numbers. A glyph
// no code can carry is written as code 0, .notdef.
//
// A face written as Type 3 fonts is refused with ErrDrawnAsType3.
func (f *Face) AppendCode(dst []byte, gid int) ([]byte, error) {
	if f.isType3() {
		return dst, ErrDrawnAsType3
	}
	return f.appendCode(dst, gid), nil
}

// NominalAdvance is how far showing a glyph's code moves the pen along a
// horizontal line, in thousandths of an em: the width the font dictionary
// Embed writes gives the code. A run placed as shaping placed it adds, after
// each glyph, the difference between its XAdvance and this. A vertical form's
// pen moves by the glyph's vertical advance instead (GlyphVerticalMetrics),
// which /W2 states.
func (f *Face) NominalAdvance(g Glyph) float64 {
	return f.nominalAdvance(g)
}
