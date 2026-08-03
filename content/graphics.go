package content

import "github.com/mgilbir/pdf0/object"

// Graphics state, path construction, painting, clipping and colour
// (ISO 32000-2 8.4, 8.5 and 8.6).

// Save pushes the graphics state (q). Every Save needs a matching Restore, and
// nesting is bounded: see MaxNestingDepth.
func (b *Builder) Save() *Builder {
	if b.inText {
		return b.fail("Save inside a text object: q is not permitted between BT and ET")
	}
	if b.inPath {
		return b.fail("Save with a path under construction")
	}
	if b.depth == MaxNestingDepth {
		return b.fail("q/Q nesting deeper than %d", MaxNestingDepth)
	}
	b.depth++
	if b.depth > b.maxDep {
		b.maxDep = b.depth
	}
	return b.op("q")
}

// Restore pops the graphics state (Q).
func (b *Builder) Restore() *Builder {
	if b.inText {
		return b.fail("Restore inside a text object: Q is not permitted between BT and ET")
	}
	if b.inPath {
		return b.fail("Restore with a path under construction")
	}
	if b.depth == 0 {
		return b.fail("Restore without a matching Save")
	}
	b.depth--
	return b.op("Q")
}

// Concat concatenates a matrix onto the current transformation matrix (cm).
// The operands are the six numbers of a PDF matrix, in the order
// [a b c d e f]: scale/skew in a–d, translation in e and f.
func (b *Builder) Concat(a, bb, c, d, e, f float64) *Builder {
	if b.inText {
		return b.fail("Concat inside a text object: cm is not permitted between BT and ET")
	}
	return b.op("cm", a, bb, c, d, e, f)
}

// Translate, Scale and Rotate are the three transformations a caller reaches
// for most often, expressed through Concat so the emitted stream is the same
// as a hand-written one.

// Translate shifts the coordinate system by (tx, ty).
func (b *Builder) Translate(tx, ty float64) *Builder { return b.Concat(1, 0, 0, 1, tx, ty) }

// Scale scales the coordinate system by (sx, sy).
func (b *Builder) Scale(sx, sy float64) *Builder { return b.Concat(sx, 0, 0, sy, 0, 0) }

// SetLineWidth sets the stroke width in user-space units (w).
func (b *Builder) SetLineWidth(w float64) *Builder {
	if w < 0 {
		return b.fail("negative line width %v", w)
	}
	return b.op("w", w)
}

// LineCap is the shape drawn at the ends of an open stroked path
// (ISO 32000-2 8.4.3.3, Table 52).
type LineCap int

// The three line caps.
const (
	ButtCap   LineCap = 0 // squared off at the endpoint
	RoundCap  LineCap = 1 // a semicircular arc beyond the endpoint
	SquareCap LineCap = 2 // a square extending half a line width beyond
)

// SetLineCap sets the line cap style (J).
func (b *Builder) SetLineCap(c LineCap) *Builder {
	if c < ButtCap || c > SquareCap {
		return b.fail("line cap %d is not one of the three ISO 32000 defines", int(c))
	}
	return b.op("J", int(c))
}

// LineJoin is the shape drawn where two path segments meet
// (ISO 32000-2 8.4.3.4, Table 53).
type LineJoin int

// The three line joins.
const (
	MiterJoin LineJoin = 0
	RoundJoin LineJoin = 1
	BevelJoin LineJoin = 2
)

// SetLineJoin sets the line join style (j).
func (b *Builder) SetLineJoin(j LineJoin) *Builder {
	if j < MiterJoin || j > BevelJoin {
		return b.fail("line join %d is not one of the three ISO 32000 defines", int(j))
	}
	return b.op("j", int(j))
}

// SetMiterLimit sets the miter length limit (M). ISO 32000-2 8.4.3.5 requires
// it to be at least 1.
func (b *Builder) SetMiterLimit(limit float64) *Builder {
	if limit < 1 {
		return b.fail("miter limit %v is below the minimum of 1", limit)
	}
	return b.op("M", limit)
}

// SetDash sets the dash pattern (d): alternating on and off lengths, starting
// at phase units into the pattern. A nil or empty pattern restores a solid
// line.
func (b *Builder) SetDash(pattern []float64, phase float64) *Builder {
	if b.err != nil {
		return b
	}
	// A pattern of all zeros makes nothing visible and is a malformed file
	// rather than an invisible line (ISO 32000-2 8.4.3.6).
	allZero := true
	for _, d := range pattern {
		if d < 0 {
			return b.fail("negative dash length %v", d)
		}
		if d != 0 {
			allZero = false
		}
	}
	if len(pattern) > 0 && allZero {
		return b.fail("dash pattern is entirely zero, which paints nothing")
	}
	if phase < 0 {
		return b.fail("negative dash phase %v", phase)
	}
	arr := []byte{'['}
	for i, d := range pattern {
		if i > 0 {
			arr = append(arr, ' ')
		}
		sub := &Builder{}
		if !sub.num(d) {
			return b.fail("dash length %v cannot be written", d)
		}
		arr = append(arr, sub.buf...)
	}
	arr = append(arr, ']')
	return b.op("d", arr, phase)
}

// SetExtGState applies a named graphics state parameter dictionary (gs). The
// name must be defined in the page's /Resources /ExtGState.
func (b *Builder) SetExtGState(name object.Name) *Builder {
	record(&b.res.ExtGStates, name)
	return b.op("gs", name)
}

// --- Path construction (ISO 32000-2 8.5.2) ---
//
// A path is built by MoveTo and the segment operators, then consumed by exactly
// one painting operator. The Builder tracks that so a caller cannot leave a
// path dangling, which would make the next drawing operator part of it.

// MoveTo begins a new subpath at (x, y) (m).
func (b *Builder) MoveTo(x, y float64) *Builder {
	if b.inText {
		return b.fail("MoveTo inside a text object: path operators are not permitted between BT and ET")
	}
	b.inPath = true
	return b.op("m", x, y)
}

// LineTo appends a straight segment to (x, y) (l).
func (b *Builder) LineTo(x, y float64) *Builder {
	if !b.inPath {
		return b.fail("LineTo without a current point: begin the path with MoveTo")
	}
	return b.op("l", x, y)
}

// CurveTo appends a cubic Bézier segment with both control points (c).
func (b *Builder) CurveTo(x1, y1, x2, y2, x3, y3 float64) *Builder {
	if !b.inPath {
		return b.fail("CurveTo without a current point: begin the path with MoveTo")
	}
	return b.op("c", x1, y1, x2, y2, x3, y3)
}

// ClosePath closes the current subpath with a straight segment (h).
func (b *Builder) ClosePath() *Builder {
	if !b.inPath {
		return b.fail("ClosePath without a path")
	}
	return b.op("h")
}

// Rect appends a complete rectangular subpath (re). Width and height may be
// negative, which mirrors the rectangle about the given corner.
func (b *Builder) Rect(x, y, w, h float64) *Builder {
	if b.inText {
		return b.fail("Rect inside a text object: path operators are not permitted between BT and ET")
	}
	b.inPath = true
	return b.op("re", x, y, w, h)
}

// --- Path painting (ISO 32000-2 8.5.3) ---

// paint ends the path, applying any pending clip.
func (b *Builder) paint(operator string) *Builder {
	if !b.inPath {
		return b.fail("%s without a path to paint", operator)
	}
	b.inPath = false
	b.pending = false
	return b.op(operator)
}

// Fill fills the path with the nonzero winding rule (f).
func (b *Builder) Fill() *Builder { return b.paint("f") }

// FillEvenOdd fills the path with the even-odd rule (f*).
func (b *Builder) FillEvenOdd() *Builder { return b.paint("f*") }

// Stroke strokes the path (S).
func (b *Builder) Stroke() *Builder { return b.paint("S") }

// CloseStroke closes the path and strokes it (s).
func (b *Builder) CloseStroke() *Builder { return b.paint("s") }

// FillStroke fills then strokes the path, nonzero winding (B).
func (b *Builder) FillStroke() *Builder { return b.paint("B") }

// FillStrokeEvenOdd fills then strokes the path, even-odd (B*).
func (b *Builder) FillStrokeEvenOdd() *Builder { return b.paint("B*") }

// EndPath ends the path without painting it (n). This is how a clip is applied
// without also drawing the clipping path.
func (b *Builder) EndPath() *Builder { return b.paint("n") }

// --- Clipping (ISO 32000-2 8.5.4) ---

// Clip intersects the clipping path with the current path, nonzero winding (W).
// It takes effect at the next painting operator, so it is followed by one —
// usually EndPath.
func (b *Builder) Clip() *Builder {
	if !b.inPath {
		return b.fail("Clip without a path")
	}
	b.pending = true
	return b.op("W")
}

// ClipEvenOdd is Clip with the even-odd rule (W*).
func (b *Builder) ClipEvenOdd() *Builder {
	if !b.inPath {
		return b.fail("ClipEvenOdd without a path")
	}
	b.pending = true
	return b.op("W*")
}

// --- Colour (ISO 32000-2 8.6.8) ---

// SetGray sets the fill colour to a DeviceGray level in [0,1] (g).
func (b *Builder) SetGray(level float64) *Builder {
	if !inUnit(level) {
		return b.fail("gray level %v is outside [0,1]", level)
	}
	return b.op("g", level)
}

// SetStrokeGray sets the stroke colour to a DeviceGray level (G).
func (b *Builder) SetStrokeGray(level float64) *Builder {
	if !inUnit(level) {
		return b.fail("gray level %v is outside [0,1]", level)
	}
	return b.op("G", level)
}

// SetRGB sets the fill colour in DeviceRGB, each component in [0,1] (rg).
func (b *Builder) SetRGB(r, g, bl float64) *Builder {
	if !inUnit(r) || !inUnit(g) || !inUnit(bl) {
		return b.fail("RGB components (%v, %v, %v) are outside [0,1]", r, g, bl)
	}
	return b.op("rg", r, g, bl)
}

// SetStrokeRGB sets the stroke colour in DeviceRGB (RG).
func (b *Builder) SetStrokeRGB(r, g, bl float64) *Builder {
	if !inUnit(r) || !inUnit(g) || !inUnit(bl) {
		return b.fail("RGB components (%v, %v, %v) are outside [0,1]", r, g, bl)
	}
	return b.op("RG", r, g, bl)
}

// SetCMYK sets the fill colour in DeviceCMYK, each component in [0,1] (k).
func (b *Builder) SetCMYK(c, m, y, k float64) *Builder {
	if !inUnit(c) || !inUnit(m) || !inUnit(y) || !inUnit(k) {
		return b.fail("CMYK components (%v, %v, %v, %v) are outside [0,1]", c, m, y, k)
	}
	return b.op("k", c, m, y, k)
}

// SetStrokeCMYK sets the stroke colour in DeviceCMYK (K).
func (b *Builder) SetStrokeCMYK(c, m, y, k float64) *Builder {
	if !inUnit(c) || !inUnit(m) || !inUnit(y) || !inUnit(k) {
		return b.fail("CMYK components (%v, %v, %v, %v) are outside [0,1]", c, m, y, k)
	}
	return b.op("K", c, m, y, k)
}

// SetColorSpace selects a named colour space for filling (cs). Device spaces
// may be named directly (/DeviceRGB and its siblings); anything else must be
// defined in the page's /Resources /ColorSpace.
func (b *Builder) SetColorSpace(name object.Name) *Builder {
	if !isDeviceSpace(name) {
		record(&b.res.ColorSpaces, name)
	}
	return b.op("cs", name)
}

// SetStrokeColorSpace selects a named colour space for stroking (CS).
func (b *Builder) SetStrokeColorSpace(name object.Name) *Builder {
	if !isDeviceSpace(name) {
		record(&b.res.ColorSpaces, name)
	}
	return b.op("CS", name)
}

// SetColor sets the fill colour components in the current colour space (scn).
func (b *Builder) SetColor(components ...float64) *Builder {
	return b.setColorN("scn", components)
}

// SetStrokeColor sets the stroke colour components in the current stroking
// colour space (SCN).
func (b *Builder) SetStrokeColor(components ...float64) *Builder {
	return b.setColorN("SCN", components)
}

func (b *Builder) setColorN(operator string, components []float64) *Builder {
	if len(components) == 0 {
		return b.fail("%s needs at least one colour component", operator)
	}
	operands := make([]any, len(components))
	for i, c := range components {
		operands[i] = c
	}
	return b.op(operator, operands...)
}

// SetPattern sets the fill colour to a named pattern (scn with a name operand).
// The name must be defined in the page's /Resources /Pattern, and the current
// colour space must be /Pattern.
func (b *Builder) SetPattern(name object.Name) *Builder {
	record(&b.res.Patterns, name)
	return b.op("scn", name)
}

// SetStrokePattern is SetPattern for stroking (SCN).
func (b *Builder) SetStrokePattern(name object.Name) *Builder {
	record(&b.res.Patterns, name)
	return b.op("SCN", name)
}

// --- Shadings and XObjects ---

// Shading paints a named shading over the current clip (sh).
func (b *Builder) Shading(name object.Name) *Builder {
	if b.inText {
		return b.fail("Shading inside a text object: sh is not permitted between BT and ET")
	}
	record(&b.res.Shadings, name)
	return b.op("sh", name)
}

// Draw paints a named XObject — a form or an image (Do). The name must be
// defined in the page's /Resources /XObject.
func (b *Builder) Draw(name object.Name) *Builder {
	if b.inText {
		return b.fail("Draw inside a text object: Do is not permitted between BT and ET")
	}
	if b.inPath {
		return b.fail("Draw with a path under construction")
	}
	record(&b.res.XObjects, name)
	return b.op("Do", name)
}

func inUnit(v float64) bool { return v >= 0 && v <= 1 }

func isDeviceSpace(n object.Name) bool {
	switch n {
	case "DeviceGray", "DeviceRGB", "DeviceCMYK", "Pattern":
		return true
	}
	return false
}
