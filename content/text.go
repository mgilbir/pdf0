package content

import "github.com/mgilbir/pdf0/object"

// Text objects, text state, positioning and showing (ISO 32000-2 9.4), plus
// marked content (14.6).

// BeginText opens a text object (BT). Text objects do not nest, and the text
// operators are only meaningful inside one.
func (b *Builder) BeginText() *Builder {
	if b.inText {
		return b.fail("BeginText inside a text object: BT does not nest")
	}
	if b.inPath {
		return b.fail("BeginText with a path under construction")
	}
	b.inText = true
	return b.op("BT")
}

// EndText closes a text object (ET).
func (b *Builder) EndText() *Builder {
	if !b.inText {
		return b.fail("EndText without a matching BeginText")
	}
	b.inText = false
	return b.op("ET")
}

// textOp writes an operator that is only valid inside a text object.
func (b *Builder) textOp(name string, operands ...any) *Builder {
	if !b.inText {
		return b.fail("%s outside a text object: wrap it in BeginText and EndText", name)
	}
	return b.op(name, operands...)
}

// SetFont selects a font and size (Tf). The name must be defined in the page's
// /Resources /Font.
//
// Unlike the other text-state operators this one is not optional: showing text
// without a font selected is undefined, and PDF/A reports it.
func (b *Builder) SetFont(name object.Name, size float64) *Builder {
	if size <= 0 {
		return b.fail("font size %v is not positive", size)
	}
	record(&b.res.Fonts, name)
	return b.textOp("Tf", name, size)
}

// SetCharSpacing sets the extra space between glyphs, in unscaled text units
// (Tc). Negative values tighten.
func (b *Builder) SetCharSpacing(spacing float64) *Builder { return b.textOp("Tc", spacing) }

// SetWordSpacing sets the extra space applied to byte 32, in unscaled text
// units (Tw).
//
// It applies to single-byte code 32 only, which for a composite font with a
// two-byte encoding — the usual case for anything but Latin — means it does
// nothing (ISO 32000-2 9.3.3). Space between words in such a font has to come
// from the text itself or from ShowTextAdjusted.
func (b *Builder) SetWordSpacing(spacing float64) *Builder { return b.textOp("Tw", spacing) }

// SetHorizontalScale sets horizontal glyph scaling as a percentage (Tz). 100 is
// unscaled.
func (b *Builder) SetHorizontalScale(percent float64) *Builder {
	if percent <= 0 {
		return b.fail("horizontal scale %v%% is not positive", percent)
	}
	return b.textOp("Tz", percent)
}

// SetLeading sets the vertical distance between baselines (TL), which NextLine
// uses.
func (b *Builder) SetLeading(leading float64) *Builder { return b.textOp("TL", leading) }

// SetRise raises or lowers the baseline, for superscripts and subscripts (Ts).
func (b *Builder) SetRise(rise float64) *Builder { return b.textOp("Ts", rise) }

// TextRenderMode selects how glyphs are painted (ISO 32000-2 9.3.6, Table 106).
type TextRenderMode int

// The eight text rendering modes. The four that add to the clipping path are
// the mechanism behind text-shaped clips; modes 3 and 7 paint nothing, which is
// how a scanned page carries an invisible text layer over its image.
const (
	FillText TextRenderMode = iota
	StrokeText
	FillStrokeText
	InvisibleText
	FillTextClip
	StrokeTextClip
	FillStrokeTextClip
	ClipText
)

// SetTextRenderMode sets the text rendering mode (Tr).
func (b *Builder) SetTextRenderMode(mode TextRenderMode) *Builder {
	if mode < FillText || mode > ClipText {
		return b.fail("text rendering mode %d is not one of the eight ISO 32000 defines", int(mode))
	}
	return b.textOp("Tr", int(mode))
}

// MoveText starts a new line offset by (tx, ty) from the start of the current
// one (Td).
func (b *Builder) MoveText(tx, ty float64) *Builder { return b.textOp("Td", tx, ty) }

// MoveTextSetLeading is MoveText, also setting the leading to -ty (TD).
func (b *Builder) MoveTextSetLeading(tx, ty float64) *Builder { return b.textOp("TD", tx, ty) }

// SetTextMatrix replaces the text matrix and line matrix (Tm). The operands are
// the six numbers of a PDF matrix, as for Concat.
func (b *Builder) SetTextMatrix(a, bb, c, d, e, f float64) *Builder {
	return b.textOp("Tm", a, bb, c, d, e, f)
}

// NextLine moves to the start of the next line, using the current leading (T*).
func (b *Builder) NextLine() *Builder { return b.textOp("T*") }

// ShowText paints a string (Tj).
//
// The bytes are character codes in the current font's encoding, not text: what
// a code means is the font's business, and this package does not know which
// font is selected. For a simple font with a single-byte encoding they are
// bytes; for a composite font with Identity-H they are two-byte glyph indices,
// big-endian. Encoding a Go string into either is the font layer's job.
func (b *Builder) ShowText(codes []byte) *Builder {
	if b.err != nil {
		return b
	}
	return b.textOp("Tj", encodeString(codes))
}

// ShowTextNextLine moves to the next line and shows a string (').
func (b *Builder) ShowTextNextLine(codes []byte) *Builder {
	if b.err != nil {
		return b
	}
	return b.textOp("'", encodeString(codes))
}

// TextSpan is one element of an adjusted text array: either a run of character
// codes or a horizontal displacement.
//
// The displacement is in thousandths of a unit of text space, and is
// *subtracted* from the current position — so a positive value moves glyphs
// closer together. That sign convention is ISO 32000-2 9.4.3's, and getting it
// backwards is the classic way to produce text that looks stretched.
type TextSpan struct {
	Codes  []byte  // character codes to show; nil for a pure adjustment
	Adjust float64 // displacement in thousandths of text space
}

// ShowTextAdjusted paints a sequence of runs with displacements between them
// (TJ). This is how kerning and justification reach the page.
func (b *Builder) ShowTextAdjusted(spans ...TextSpan) *Builder {
	if b.err != nil {
		return b
	}
	if len(spans) == 0 {
		return b.fail("ShowTextAdjusted needs at least one span")
	}
	arr := []byte{'['}
	for _, s := range spans {
		if s.Codes != nil {
			arr = append(arr, encodeString(s.Codes)...)
		}
		if s.Adjust != 0 {
			sub := &Builder{}
			if !sub.num(s.Adjust) {
				return b.fail("text adjustment %v cannot be written", s.Adjust)
			}
			arr = append(arr, ' ')
			arr = append(arr, sub.buf...)
			arr = append(arr, ' ')
		}
	}
	arr = append(arr, ']')
	return b.textOp("TJ", arr)
}

// encodeString writes character codes as a PDF literal string, escaping the
// three bytes that would otherwise end it or change its meaning. Everything
// else goes through unchanged, including binary: a two-byte glyph index is not
// text and must not be reinterpreted.
func encodeString(codes []byte) []byte {
	out := make([]byte, 0, len(codes)+2)
	out = append(out, '(')
	for _, c := range codes {
		switch c {
		case '(', ')', '\\':
			out = append(out, '\\', c)
		case '\r':
			// A bare CR in a literal string is folded to LF by a conforming
			// reader (ISO 32000-2 7.3.4.2), which would corrupt a glyph index.
			out = append(out, '\\', 'r')
		default:
			out = append(out, c)
		}
	}
	return append(out, ')')
}

// --- Marked content (ISO 32000-2 14.6) ---
//
// These carry the structure a tagged PDF needs. They are here from the start
// because retrofitting them means re-deriving where each mark belonged.

// BeginMarked opens a marked-content sequence with a tag alone (BMC).
func (b *Builder) BeginMarked(tag object.Name) *Builder { return b.op("BMC", tag) }

// BeginMarkedProperties opens a marked-content sequence whose properties are a
// named entry in the page's /Resources /Properties (BDC).
func (b *Builder) BeginMarkedProperties(tag, properties object.Name) *Builder {
	record(&b.res.Properties, properties)
	return b.op("BDC", tag, properties)
}

// EndMarked closes a marked-content sequence (EMC).
func (b *Builder) EndMarked() *Builder { return b.op("EMC") }
