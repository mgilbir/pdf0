package content

import (
	"strconv"

	"github.com/mgilbir/pdf0/object"
)

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
	b.textFloor = b.marked
	return b.op("BT")
}

// EndText closes a text object (ET).
func (b *Builder) EndText() *Builder {
	if !b.inText {
		return b.fail("EndText without a matching BeginText")
	}
	if b.marked > b.textFloor {
		return b.fail("EndText with %d marked-content sequence(s) begun inside the text object still open; "+
			"a sequence and a text object nest, so it has to end first", b.marked-b.textFloor)
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

	// marker is set on the two spans ActualTextStart and ActualTextEnd make,
	// which carry no codes and no displacement: they say where a stretch of
	// the other spans begins and ends. Unexported, so that the only way to
	// make one is the constructor that states what it means.
	marker spanMarker
	// actual is the text an ActualTextStart span names.
	actual string
}

type spanMarker uint8

const (
	noMarker spanMarker = iota
	actualStart
	actualEnd
)

// ActualTextStart begins a stretch of spans that stand for the given text,
// and ActualTextEnd ends it. ShowTextAdjusted writes the stretch inside a
// marked-content sequence carrying the text as /ActualText (ISO 32000-2
// 14.9.4), which is what a reader extracts in place of what the codes'
// ToUnicode mapping says.
//
// It is how a shaped run says what it was set from where no per-glyph mapping
// can: a glyph drawn for two different texts, glyphs reordered across a
// cluster, a right-to-left run whose glyphs are in the order they are drawn
// rather than the order they are read. Stretches do not nest, and each start
// needs its end within the same ShowTextAdjusted call.
func ActualTextStart(text string) TextSpan {
	return TextSpan{marker: actualStart, actual: text}
}

// ActualTextEnd ends the stretch ActualTextStart began.
func ActualTextEnd() TextSpan { return TextSpan{marker: actualEnd} }

// ShowTextAdjusted paints a sequence of runs with displacements between them
// (TJ). This is how kerning and justification reach the page.
//
// A stretch marked by ActualTextStart and ActualTextEnd splits the array: the
// marked-content operators cannot appear inside a TJ, so the spans before, in
// and after the stretch become TJ operators of their own. That changes nothing
// on the page, because a displacement in one TJ moves the same text matrix the
// next one starts from.
func (b *Builder) ShowTextAdjusted(spans ...TextSpan) *Builder {
	if b.err != nil {
		return b
	}
	if len(spans) == 0 {
		return b.fail("ShowTextAdjusted needs at least one span")
	}
	// Checked before anything is written, so a malformed sequence leaves no
	// half-open marked-content sequence behind it.
	open := false
	for _, s := range spans {
		switch s.marker {
		case actualStart:
			if open {
				return b.fail("ActualTextStart inside another: the stretches do not nest")
			}
			open = true
		case actualEnd:
			if !open {
				return b.fail("ActualTextEnd without a matching ActualTextStart")
			}
			open = false
		}
	}
	if open {
		return b.fail("ActualTextStart without a matching ActualTextEnd")
	}

	var arr []byte
	wrote := false
	flush := func(always bool) {
		// A stretch boundary with nothing before it writes no empty TJ; the
		// end of a call with nothing written at all does, which keeps a span
		// list of pure zeros the operator it always was, text-object check
		// included.
		if len(arr) > 0 || (always && !wrote) {
			b.textOp("TJ", append(append([]byte{'['}, arr...), ']'))
			arr = arr[:0]
			wrote = true
		}
	}
	for _, s := range spans {
		switch s.marker {
		case actualStart:
			flush(false)
			b.BeginActualText(s.actual)
			wrote = true
			continue
		case actualEnd:
			flush(false)
			b.EndMarked()
			continue
		}
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
	flush(true)
	return b
}

// BeginActualText opens a marked-content sequence whose content stands for the
// given text: /Span with an inline /ActualText (ISO 32000-2 14.9.4). EndMarked
// closes it.
//
// A reader extracting text takes this in place of what the enclosed glyphs'
// ToUnicode mapping would give, which is the one way to say what a run of
// glyphs was set from when no mapping of individual glyphs can: a ligature
// glyph drawn for "ffi" in one place and for "ﬃ" in another, a conjunct whose
// parts are reordered, a right-to-left word drawn in visual order.
//
// The text is written as UTF-16BE with a byte-order mark, which is the text
// string encoding every reader supports (ISO 32000-2 7.9.2.2), in hexadecimal
// so that no byte of it needs escaping.
func (b *Builder) BeginActualText(text string) *Builder {
	if b.err != nil {
		return b
	}
	props := append([]byte(nil), "<</ActualText <FEFF"...)
	props = appendUTF16BEHex(props, text)
	props = append(props, ">>>"...)
	return b.beginMarked("BDC", object.Name("Span"), props)
}

// appendUTF16BEHex writes text as the hexadecimal digits of its UTF-16BE form,
// with surrogate pairs for characters beyond the Basic Multilingual Plane.
// Invalid UTF-8 becomes U+FFFD, which is what ranging over a string yields.
func appendUTF16BEHex(dst []byte, text string) []byte {
	const digits = "0123456789ABCDEF"
	unit := func(u uint16) {
		dst = append(dst, digits[u>>12], digits[u>>8&0xF], digits[u>>4&0xF], digits[u&0xF])
	}
	for _, r := range text {
		if r > 0xFFFF {
			r -= 0x10000
			unit(uint16(0xD800 + r>>10))
			unit(uint16(0xDC00 + r&0x3FF))
			continue
		}
		unit(uint16(r))
	}
	return dst
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

// beginMarked writes BMC or BDC and opens the sequence it begins. Every
// sequence needs its EndMarked: Bytes refuses a stream that leaves one open.
func (b *Builder) beginMarked(operator string, operands ...any) *Builder {
	if b.err != nil {
		return b
	}
	b.op(operator, operands...)
	if b.err == nil {
		b.marked++
	}
	return b
}

// BeginMarked opens a marked-content sequence with a tag alone (BMC).
func (b *Builder) BeginMarked(tag object.Name) *Builder { return b.beginMarked("BMC", tag) }

// BeginMarkedProperties opens a marked-content sequence whose properties are a
// named entry in the page's /Resources /Properties (BDC).
func (b *Builder) BeginMarkedProperties(tag, properties object.Name) *Builder {
	record(&b.res.Properties, properties)
	return b.beginMarked("BDC", tag, properties)
}

// BeginTagged opens a marked-content sequence carrying a marked-content
// identifier (BDC with an inline property list).
//
// This is what makes a content stream taggable. A structure element says "my
// content is identifier 4 on page 2", and this is the other end of that
// sentence: the span of the stream that identifier names. Without it a
// structure tree describes a document whose content it cannot point at.
//
// The property list is written inline rather than through the page's
// /Resources /Properties because an identifier is not a shared resource — every
// span has its own, and a page of a thousand paragraphs would otherwise need a
// thousand named entries that are each used once.
//
// Identifiers must be unique within a page and are conventionally assigned in
// the order the content is drawn. Nesting is allowed and is how a heading
// inside a section is expressed; every BeginTagged needs its EndMarked.
func (b *Builder) BeginTagged(tag object.Name, mcid int) *Builder {
	if mcid < 0 {
		return b.fail("marked-content identifier %d is negative", mcid)
	}
	var props []byte
	props = append(props, "<</MCID "...)
	props = strconv.AppendInt(props, int64(mcid), 10)
	props = append(props, ">>"...)
	return b.beginMarked("BDC", tag, props)
}

// EndMarked closes the innermost open marked-content sequence (EMC).
//
// One with nothing open is refused, and so is one inside a text object that
// would close a sequence begun outside it: that EMC would end a sequence the
// text object sits inside, and the two no longer nest.
func (b *Builder) EndMarked() *Builder {
	if b.err != nil {
		return b
	}
	if b.marked == 0 {
		return b.fail("EndMarked without a matching BeginMarked or BeginTagged")
	}
	if b.inText && b.marked == b.textFloor {
		return b.fail("EndMarked inside a text object would close a sequence begun outside it; " +
			"end the text object first")
	}
	b.op("EMC")
	if b.err == nil {
		b.marked--
	}
	return b
}

// MarkPoint records a marked-content point: a place in the stream rather than a
// span of it (MP). It is what an anchor, a footnote reference or a
// cross-reference target is attached to.
func (b *Builder) MarkPoint(tag object.Name) *Builder { return b.op("MP", tag) }

// MarkPointProperties is MarkPoint with properties, named from the page's
// /Resources /Properties (DP).
func (b *Builder) MarkPointProperties(tag, properties object.Name) *Builder {
	record(&b.res.Properties, properties)
	return b.op("DP", tag, properties)
}
