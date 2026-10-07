package fonts

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/mgilbir/forme/shape"
	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/object"
)

// A COLR colour glyph as a Type 3 glyph procedure: forme paints the glyph
// (shape.Face.PaintGlyph), and colrPainter writes each call as the PDF that
// paints it. docs/proposals/bitmap-fonts-type3.md, "COLR colour glyphs", has
// the table of what becomes what.
//
// The procedure works in font units, y up, which is what forme paints in: its
// content is wrapped in a cm scaling font units to glyph space. Every push is
// a q and every pop a Q, so a transform and a clip last exactly as long as
// forme says. A group is a content buffer of its own, which the pop that ends
// it combines with the one beneath as the composite mode says.
//
// A paint in the foreground colour sets no colour: a d0 procedure starts in
// the colour the text is shown in. Every other paint sets its colour inside a
// q and Q of its own, so a later foreground paint still sees the text's.

// errCompositeMode is a COLR composite mode PDF cannot state exactly.
var errCompositeMode = errors.New("fonts: the colour glyph composites in a mode PDF has no exact equivalent for")

// errForegroundStop is a gradient stop in the foreground colour drawn in a
// colour that is not one a shading can state.
var errForegroundStop = errors.New("fonts: the colour glyph's gradient has a stop in the text colour, " +
	"and the text is shown in a colour that is not DeviceRGB or DeviceGray")

// matrix is an affine transform in forme's form: (x, y) goes to
// (XX*x + XY*y + X0, YX*x + YY*y + Y0).
type matrix = shape.Transform

var identity = matrix{XX: 1, YY: 1}

func mul(p, t matrix) matrix { // p after t
	return matrix{
		XX: p.XX*t.XX + p.XY*t.YX, YX: p.YX*t.XX + p.YY*t.YX,
		XY: p.XX*t.XY + p.XY*t.YY, YY: p.YX*t.XY + p.YY*t.YY,
		X0: p.XX*t.X0 + p.XY*t.Y0 + p.X0, Y0: p.YX*t.X0 + p.YY*t.Y0 + p.Y0,
	}
}

func apply(m matrix, x, y float64) (float64, float64) {
	return m.XX*x + m.XY*y + m.X0, m.YX*x + m.YY*y + m.Y0
}

func invert(m matrix) (matrix, bool) {
	det := m.XX*m.YY - m.XY*m.YX
	if det == 0 || math.IsNaN(det) || math.IsInf(det, 0) || math.Abs(det) < 1e-12 {
		return matrix{}, false
	}
	return matrix{
		XX: m.YY / det, YX: -m.YX / det, XY: -m.XY / det, YY: m.XX / det,
		X0: (m.XY*m.Y0 - m.YY*m.X0) / det, Y0: (m.YX*m.X0 - m.XX*m.Y0) / det,
	}, true
}

// box is an axis-aligned rectangle.
type box struct{ x0, y0, x1, y1 float64 }

func (b box) empty() bool { return !(b.x1 > b.x0) || !(b.y1 > b.y0) }

func (b box) intersect(o box) box {
	return box{math.Max(b.x0, o.x0), math.Max(b.y0, o.y0), math.Min(b.x1, o.x1), math.Min(b.y1, o.y1)}
}

// through is the box enclosing b's corners taken through m.
func (b box) through(m matrix) box {
	out := box{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
	for _, c := range [][2]float64{{b.x0, b.y0}, {b.x1, b.y0}, {b.x0, b.y1}, {b.x1, b.y1}} {
		x, y := apply(m, c[0], c[1])
		out = box{math.Min(out.x0, x), math.Min(out.y0, y), math.Max(out.x1, x), math.Max(out.y1, y)}
	}
	return out
}

// pushed is what a push opened, for the pop that closes it.
type pushed struct {
	ctm  matrix
	clip box  // the clip's box in the procedure's own space
	dead bool // nothing inside can show: an empty clip, a singular transform
}

// layer is a group's content, and the resources it names.
type layer struct {
	buf  []byte
	used map[resKey]bool
	// isolated says the content blends or masks against what is beneath it
	// in the layer: it has to be drawn as a group of its own wherever it
	// goes, or what it blends with would be whatever the page has there.
	isolated bool
}

type resKey struct {
	kind string // the /Resources sub-dictionary: ExtGState, Shading, XObject
	name object.Name
}

// colrPainter writes one glyph's painting as a procedure's content.
type colrPainter struct {
	f    *Face
	doc  *capture
	gid  int
	fg   *content.Color // the text colour, for a foreground gradient stop
	n    int            // resources named so far
	res  map[resKey]object.IndirectRef
	head []layer
	open []pushed
	err  error
	// coloured says the content sets a colour, paints a shading or an image,
	// or uses an ExtGState: a d0 procedure. One that only fills in the text
	// colour is a d1 procedure, whose glyph a reader may cache as a shape.
	coloured bool
	// inked is the box of everything painted, in the procedure's own space.
	inked box
}

func newColrPainter(f *Face, doc *capture, gid int, fg *content.Color, root box) *colrPainter {
	return &colrPainter{
		f: f, doc: doc, gid: gid, fg: fg,
		res:  map[resKey]object.IndirectRef{},
		head: []layer{{used: map[resKey]bool{}}},
		open: []pushed{{ctm: identity, clip: root}},
	}
}

func (p *colrPainter) fail(err error) {
	if p.err == nil {
		p.err = fmt.Errorf("glyph %d: %w", p.gid, err)
	}
}

func (p *colrPainter) top() *pushed { return &p.open[len(p.open)-1] }

func (p *colrPainter) out() *layer { return &p.head[len(p.head)-1] }

func (p *colrPainter) write(b []byte) { l := p.out(); l.buf = append(l.buf, b...) }

func (p *colrPainter) writeString(s string) { l := p.out(); l.buf = append(l.buf, s...) }

// name adds an object to the procedure's resources under a fresh name.
func (p *colrPainter) name(kind string, o object.Object) object.Name {
	p.n++
	n := object.Name("c" + strconv.Itoa(p.gid) + "_" + strconv.Itoa(p.n))
	k := resKey{kind, n}
	p.res[k] = p.doc.Add(o)
	p.out().used[k] = true
	return n
}

// local is the current clip's box in the current coordinates: what a paint
// that fills "everywhere inside the clips" fills, and what a shading's domain
// has to cover.
func (p *colrPainter) local() box {
	t := p.top()
	inv, ok := invert(t.ctm)
	if !ok {
		return box{}
	}
	return t.clip.through(inv)
}

func (p *colrPainter) push(t matrix, clip box, dead bool) {
	cur := p.top()
	p.open = append(p.open, pushed{ctm: t, clip: clip, dead: dead || cur.dead || clip.empty()})
	p.writeString("q\n")
}

func (p *colrPainter) pop() {
	if len(p.open) > 1 {
		p.open = p.open[:len(p.open)-1]
	}
	p.writeString("Q\n")
}

func (p *colrPainter) PushTransform(t shape.Transform) {
	cur := p.top()
	m := mul(cur.ctm, t)
	_, ok := invert(m)
	p.push(m, cur.clip, !ok)
	if ok {
		p.write(appendNums(nil, t.XX, t.YX, t.XY, t.YY, t.X0, t.Y0))
		p.writeString("cm\n")
	}
}

func (p *colrPainter) PopTransform() { p.pop() }

func (p *colrPainter) PushClipGlyph(gid int) {
	cur := p.top()
	path, bb, err := p.outline(gid)
	if err != nil {
		p.fail(err)
	}
	clip := cur.clip.intersect(bb.through(cur.ctm))
	p.push(cur.ctm, clip, len(path) == 0)
	if len(path) > 0 && !p.top().dead {
		p.write(path)
		p.writeString("W n\n")
	}
}

func (p *colrPainter) PushClipRect(r shape.Rect) {
	cur := p.top()
	b := box{r.XMin, r.YMin, r.XMax, r.YMax}
	p.push(cur.ctm, cur.clip.intersect(b.through(cur.ctm)), b.empty())
	if !p.top().dead {
		p.write(appendNums(nil, b.x0, b.y0, b.x1-b.x0, b.y1-b.y0))
		p.writeString("re W n\n")
	}
}

func (p *colrPainter) PopClip() { p.pop() }

// outline is a glyph's outline as a path, in font units, and its box.
func (p *colrPainter) outline(gid int) ([]byte, box, error) {
	var path []byte
	bb := box{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
	var cx, cy float64 // the current point, to write a quadratic as a cubic
	see := func(pt shape.Point) {
		bb = box{math.Min(bb.x0, pt.X), math.Min(bb.y0, pt.Y), math.Max(bb.x1, pt.X), math.Max(bb.y1, pt.Y)}
	}
	err := p.f.GlyphOutline(gid, func(s shape.Segment) bool {
		switch s.Op {
		case shape.MoveTo:
			path = appendNums(path, s.Pts[0].X, s.Pts[0].Y)
			path = append(path, "m\n"...)
			see(s.Pts[0])
			cx, cy = s.Pts[0].X, s.Pts[0].Y
		case shape.LineTo:
			path = appendNums(path, s.Pts[0].X, s.Pts[0].Y)
			path = append(path, "l\n"...)
			see(s.Pts[0])
			cx, cy = s.Pts[0].X, s.Pts[0].Y
		case shape.QuadTo:
			q, e := s.Pts[0], s.Pts[1]
			c1x, c1y := cx+2.0/3*(q.X-cx), cy+2.0/3*(q.Y-cy)
			c2x, c2y := e.X+2.0/3*(q.X-e.X), e.Y+2.0/3*(q.Y-e.Y)
			path = appendNums(path, c1x, c1y, c2x, c2y, e.X, e.Y)
			path = append(path, "c\n"...)
			see(q)
			see(e)
			cx, cy = e.X, e.Y
		case shape.CubicTo:
			path = appendNums(path, s.Pts[0].X, s.Pts[0].Y, s.Pts[1].X, s.Pts[1].Y, s.Pts[2].X, s.Pts[2].Y)
			path = append(path, "c\n"...)
			see(s.Pts[0])
			see(s.Pts[1])
			see(s.Pts[2])
			cx, cy = s.Pts[2].X, s.Pts[2].Y
		}
		return true
	})
	if errors.Is(err, shape.ErrNoOutline) {
		return nil, box{}, nil
	}
	if err != nil {
		return nil, box{}, err
	}
	if len(path) == 0 {
		return nil, box{}, nil
	}
	return path, bb, nil
}

func (p *colrPainter) PushGroup() {
	p.head = append(p.head, layer{used: map[resKey]bool{}})
}

// PopGroup combines the group with what is beneath it in the group that holds
// it. forme paints a composite as HarfBuzz does — a group, the backdrop, a
// group, the source, the source's group popped with the mode, the outer one
// popped as source-over — so beneath is exactly the backdrop.
func (p *colrPainter) PopGroup(mode shape.CompositeMode) {
	if len(p.head) < 2 {
		p.fail(errors.New("a group popped that was not pushed"))
		return
	}
	src := p.head[len(p.head)-1]
	p.head = p.head[:len(p.head)-1]
	dst := p.out()
	merge := func(l layer) {
		for k := range l.used {
			dst.used[k] = true
		}
	}
	bb := p.local()
	// A layer that blends or masks within itself is drawn as an isolated
	// group, so that it blends with nothing outside it; any other is drawn
	// inline, src-over being associative.
	drawn := func(l layer) []byte {
		if !l.isolated {
			merge(l)
			return l.buf
		}
		x := p.name("XObject", p.form(l, bb, true))
		return []byte("/" + string(x) + " Do\n")
	}
	switch mode {
	case shape.CompositeSrcOver:
		dst.buf = append(dst.buf, drawn(src)...)
	case shape.CompositeDestOver:
		if dst.isolated {
			// What was beneath goes on top, and blends with nothing
			// above it either.
			below := *dst
			*dst = layer{used: map[resKey]bool{}}
			dst = p.out()
			dst.buf = append(append([]byte(nil), drawn(src)...), drawn(below)...)
		} else {
			dst.buf = append(append([]byte(nil), drawn(src)...), dst.buf...)
		}
	case shape.CompositeClear:
		dst.buf, dst.used = nil, map[resKey]bool{}
	case shape.CompositeSrc:
		*dst = src
	case shape.CompositeDest:
	case shape.CompositeSrcIn, shape.CompositeSrcOut:
		*dst = p.masked(src, *dst, mode == shape.CompositeSrcOut, bb)
	case shape.CompositeDestIn, shape.CompositeDestOut:
		*dst = p.masked(*dst, src, mode == shape.CompositeDestOut, bb)
	default:
		bm, ok := blendModes[mode]
		if !ok {
			p.fail(fmt.Errorf("%w (mode %d)", errCompositeMode, mode))
			return
		}
		form := p.form(src, bb, false)
		gs := object.NewDictionary(object.Entry{Key: "BM", Value: object.Name(bm)})
		g := p.name("ExtGState", gs)
		x := p.name("XObject", form)
		p.coloured = true
		p.writeString("q\n/" + string(g) + " gs\n/" + string(x) + " Do\nQ\n")
		p.out().isolated = true
	}
}

// blendModes are COLR's blend modes, which are W3C's and PDF's (ISO 32000-2
// 11.3.5).
var blendModes = map[shape.CompositeMode]string{
	shape.CompositeScreen: "Screen", shape.CompositeOverlay: "Overlay",
	shape.CompositeDarken: "Darken", shape.CompositeLighten: "Lighten",
	shape.CompositeColorDodge: "ColorDodge", shape.CompositeColorBurn: "ColorBurn",
	shape.CompositeHardLight: "HardLight", shape.CompositeSoftLight: "SoftLight",
	shape.CompositeDifference: "Difference", shape.CompositeExclusion: "Exclusion",
	shape.CompositeMultiply: "Multiply", shape.CompositeHSLHue: "Hue",
	shape.CompositeHSLSaturation: "Saturation", shape.CompositeHSLColor: "Color",
	shape.CompositeHSLLuminosity: "Luminosity",
}

// form writes a layer as a form XObject over bb, an isolated transparency
// group when group is set.
func (p *colrPainter) form(l layer, bb box, group bool) *object.Stream {
	res := map[string]*object.Dictionary{}
	keys := slices.SortedFunc(maps.Keys(l.used), compareResKeys)
	for _, k := range keys {
		if res[k.kind] == nil {
			res[k.kind] = &object.Dictionary{}
		}
		res[k.kind].Set(k.name, p.res[k])
	}
	resources := &object.Dictionary{}
	for _, kind := range []string{"ExtGState", "Shading", "XObject"} {
		if d := res[kind]; d != nil {
			resources.Set(object.Name(kind), d)
		}
	}
	s := flateStream(l.buf)
	s.Dict.Set("Type", object.Name("XObject"))
	s.Dict.Set("Subtype", object.Name("Form"))
	s.Dict.Set("BBox", object.Array{realNumber(bb.x0), realNumber(bb.y0), realNumber(bb.x1), realNumber(bb.y1)})
	s.Dict.Set("Resources", resources)
	g := &object.Dictionary{}
	g.Set("S", object.Name("Transparency"))
	g.Set("I", object.Boolean(true))
	g.Set("CS", object.Name("DeviceRGB"))
	s.Dict.Set("Group", g)
	return s
}

// masked is l drawn through a soft mask of mask's alpha, or of one minus it.
func (p *colrPainter) masked(l, mask layer, invertMask bool, bb box) layer {
	out := layer{used: map[resKey]bool{}}
	saved := p.head
	p.head = append(p.head, out)
	smask := &object.Dictionary{}
	smask.Set("Type", object.Name("Mask"))
	smask.Set("S", object.Name("Alpha"))
	smask.Set("G", p.doc.Add(p.form(mask, bb, true)))
	if invertMask {
		// The transfer function 1 − a.
		smask.Set("TR", object.NewDictionary(
			object.Entry{Key: "FunctionType", Value: object.Integer(2)},
			object.Entry{Key: "Domain", Value: object.Array{object.Integer(0), object.Integer(1)}},
			object.Entry{Key: "C0", Value: object.Array{object.Integer(1)}},
			object.Entry{Key: "C1", Value: object.Array{object.Integer(0)}},
			object.Entry{Key: "N", Value: object.Integer(1)},
		))
	}
	gs := object.NewDictionary(object.Entry{Key: "SMask", Value: smask})
	g := p.name("ExtGState", gs)
	x := p.name("XObject", p.form(l, bb, true))
	p.coloured = true
	p.writeString("q\n/" + string(g) + " gs\n/" + string(x) + " Do\nQ\n")
	out = *p.out()
	out.isolated = true
	p.head = saved
	return out
}

// alphaState writes an ExtGState of a fill alpha, and returns its name; none
// for an opaque one.
func (p *colrPainter) alphaState(a uint8) string {
	if a == 0xFF {
		return ""
	}
	gs := object.NewDictionary(object.Entry{Key: "ca", Value: realNumber(float64(a) / 255)})
	return "/" + string(p.name("ExtGState", gs)) + " gs\n"
}

// ink records that a paint covers the current clip.
func (p *colrPainter) ink() {
	c := p.top().clip
	if p.inked.empty() {
		p.inked = c
		return
	}
	p.inked = box{math.Min(p.inked.x0, c.x0), math.Min(p.inked.y0, c.y0), math.Max(p.inked.x1, c.x1), math.Max(p.inked.y1, c.y1)}
}

func (p *colrPainter) Solid(c shape.Color, foreground bool) {
	if p.top().dead {
		return
	}
	bb := p.local()
	if bb.empty() {
		return
	}
	p.ink()
	p.writeString("q\n")
	if gs := p.alphaState(c.A); gs != "" {
		p.coloured = true
		p.writeString(gs)
	}
	if !foreground {
		p.coloured = true
		p.write(appendNums(nil, float64(c.R)/255, float64(c.G)/255, float64(c.B)/255))
		p.writeString("rg\n")
	}
	p.write(appendNums(nil, bb.x0, bb.y0, bb.x1-bb.x0, bb.y1-bb.y0))
	p.writeString("re f\nQ\n")
}

func (p *colrPainter) Image(shape.Image) {
	p.fail(errors.New("an image in a colour glyph is not drawn"))
}

// stop is a colour stop with its colour resolved.
type stop struct {
	at   float64
	rgb  [3]float64
	a    float64
	gray bool
}

// stops resolves a colour line's stops, sorted by offset, and the offsets'
// range: the colour line as HarfBuzz normalises it, the first stop at 0 and
// the last at 1 of that range.
func (p *colrPainter) stops(line shape.ColorLine) (out []stop, lo, hi float64, ok bool) {
	for _, s := range line.Stops {
		c := s.Color
		st := stop{at: s.Offset, rgb: [3]float64{float64(c.R) / 255, float64(c.G) / 255, float64(c.B) / 255}, a: float64(c.A) / 255}
		if s.Foreground {
			fg, ok := foregroundRGB(p.fg)
			if !ok {
				p.fail(errForegroundStop)
				return nil, 0, 0, false
			}
			st.rgb = fg
		}
		out = append(out, st)
	}
	if len(out) == 0 {
		return nil, 0, 0, false
	}
	slices.SortStableFunc(out, func(a, b stop) int {
		switch {
		case a.at < b.at:
			return -1
		case a.at > b.at:
			return 1
		}
		return 0
	})
	lo, hi = out[0].at, out[len(out)-1].at
	if !(hi > lo) {
		// Every stop at one offset: the colour line is a step there.
		hi = lo + 1
	}
	for i := range out {
		out[i].at = (out[i].at - lo) / (hi - lo)
	}
	return out, lo, hi, true
}

// foregroundRGB is the text colour as DeviceRGB components, where it is one a
// shading can state exactly.
func foregroundRGB(c *content.Color) ([3]float64, bool) {
	if c == nil {
		return [3]float64{}, false
	}
	switch {
	case c.Space == "DeviceRGB" && len(c.Components) == 3:
		return [3]float64{c.Components[0], c.Components[1], c.Components[2]}, true
	case c.Space == "DeviceGray" && len(c.Components) == 1:
		g := c.Components[0]
		return [3]float64{g, g, g}, true
	}
	return [3]float64{}, false
}

// psColour writes a Type 4 function's body that maps u, in 0 to 1 on the
// stack, to the colour line's colour there — three components, or the alpha
// for alpha — by the piece of the line it falls in.
func psColour(stops []stop, alpha bool) string {
	comps := func(s stop) []float64 {
		if alpha {
			return []float64{s.a}
		}
		return s.rgb[:]
	}
	push := func(s stop) string {
		out := "pop"
		for _, v := range comps(s) {
			out += " " + psNum(v)
		}
		return out
	}
	// Past the last stop, the last colour; before the first, the first.
	body := push(stops[len(stops)-1])
	for i := len(stops) - 2; i >= 0; i-- {
		a, b := stops[i], stops[i+1]
		span := b.at - a.at
		var seg string
		if span <= 0 {
			// Stops at one offset: the line steps there, and at the offset
			// itself, which is where a padded line clamps what lies before
			// it, it is the first of them (as Skia draws it).
			seg = push(a)
		} else {
			// The fraction f of the way from a to b, then each component
			// a + f(b − a), the fraction kept beneath the components made so
			// far until the last one takes it.
			seg = psNum(a.at) + " sub " + psNum(span) + " div"
			ca, cb := comps(a), comps(b)
			for k := range ca {
				d := cb[k] - ca[k]
				if k < len(ca)-1 {
					seg += " dup " + psNum(d) + " mul " + psNum(ca[k]) + " add exch"
				} else {
					seg += " " + psNum(d) + " mul " + psNum(ca[k]) + " add"
				}
			}
		}
		body = "dup " + psNum(b.at) + " le { " + seg + " } { " + body + " } ifelse"
	}
	return "dup " + psNum(stops[0].at) + " lt { " + push(stops[0]) + " } { " + body + " } ifelse"
}

// psExtend writes the extend mode as a function of u on the stack.
func psExtend(e shape.Extend) string {
	switch e {
	case shape.ExtendRepeat:
		return "dup floor sub"
	case shape.ExtendReflect:
		return "abs dup 2 div floor 2 mul sub dup 1 gt { 2 exch sub } if"
	}
	// The calculator functions have no min or max (ISO 32000-2 7.10.5.2).
	return "dup 0 lt { pop 0 } if dup 1 gt { pop 1 } if"
}

// psNum writes a number for a calculator function, to six decimal places: a
// colour is in 255ths and an offset is normalised, and a reader's calculator
// need not read more (ISO 32000-2 Annex C gives a real about six significant
// digits; Ghostscript's refuses a seventeen-digit one as a syntax error).
func psNum(v float64) string {
	s := strconv.FormatFloat(cleanZero(v), 'f', 6, 64)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	if s == "" || s == "-" || s == "-0" {
		return "0"
	}
	return s
}

// type4 is a PostScript calculator function (ISO 32000-2 7.10.5).
func type4(domain, rng []float64, code string) *object.Stream {
	s := object.NewStream(nil, []byte("{ "+code+" }"))
	s.Dict.Set("FunctionType", object.Integer(4))
	s.Dict.Set("Domain", nums(domain))
	s.Dict.Set("Range", nums(rng))
	s.Dict.Set("Length", object.Integer(len(s.Data)))
	return s
}

func nums(vs []float64) object.Array {
	out := make(object.Array, len(vs))
	for i, v := range vs {
		out[i] = realNumber(v)
	}
	return out
}

// gradient paints a shading of the colour line through the current clip, and
// through a soft mask of its alpha where a stop is not opaque. build makes
// the shading for a function of the colour line's t: colour, or alpha.
func (p *colrPainter) gradient(line shape.ColorLine, build func(fn object.Object, gray bool, stops []stop, lo, hi float64) *object.Dictionary) {
	if p.top().dead {
		return
	}
	bb := p.local()
	if bb.empty() {
		return
	}
	stops, lo, hi, ok := p.stops(line)
	if !ok {
		return
	}
	p.ink()
	p.coloured = true
	colour := build(nil, false, stops, lo, hi)
	if colour == nil {
		return
	}
	sh := p.name("Shading", colour)
	opaque := true
	for _, s := range stops {
		opaque = opaque && s.a >= 1
	}
	p.writeString("q\n")
	if !opaque {
		alpha := build(nil, true, stops, lo, hi)
		mask := layer{used: map[resKey]bool{}}
		p.head = append(p.head, mask)
		a := p.name("Shading", alpha)
		p.writeString("/" + string(a) + " sh\n")
		mask = *p.out()
		p.head = p.head[:len(p.head)-1]
		form := p.form(mask, bb, true)
		form.Dict.Get("Group").(*object.Dictionary).Set("CS", object.Name("DeviceGray"))
		smask := &object.Dictionary{}
		smask.Set("Type", object.Name("Mask"))
		smask.Set("S", object.Name("Luminosity"))
		smask.Set("G", p.doc.Add(form))
		g := p.name("ExtGState", object.NewDictionary(object.Entry{Key: "SMask", Value: smask}))
		p.writeString("/" + string(g) + " gs\n")
	}
	p.writeString("/" + string(sh) + " sh\nQ\n")
}

// lineFunction is the function of t, over t0 to t1, a linear or radial
// shading takes: the colour line from lo to hi, extended as it says.
//
// It is linear (Type 2) pieces stitched together (Type 3), one run of them a
// period of the line, mirrored in alternate periods to reflect it: exact, in
// every reader, and what PDF/A permits. A calculator function would say the
// same in fewer bytes, and a reader that samples a shading's function to
// decide how finely to draw it — Ghostscript does — sees a line that repeats
// at the points it samples as flat. Past maxPeriods periods it is a
// calculator function after all.
func lineFunction(stops []stop, lo, hi, t0, t1 float64, e shape.Extend, gray bool) object.Object {
	type piece struct {
		a, b   float64 // in t
		c0, c1 []float64
	}
	comps := func(s stop) []float64 {
		if gray {
			return []float64{s.a}
		}
		return []float64{s.rgb[0], s.rgb[1], s.rgb[2]}
	}
	period := hi - lo
	// One period, in u from 0 to 1, as the stops divide it.
	var base []piece
	for i := 0; i+1 < len(stops); i++ {
		if stops[i+1].at > stops[i].at {
			base = append(base, piece{stops[i].at, stops[i+1].at, comps(stops[i]), comps(stops[i+1])})
		}
	}
	first, last := comps(stops[0]), comps(stops[len(stops)-1])
	var pieces []piece
	at := func(u, k float64) float64 { return lo + (k+u)*period }
	if e == shape.ExtendPad || len(base) == 0 {
		pieces = append(pieces, piece{math.Min(t0, lo) - 1, lo, first, first})
		for _, b := range base {
			pieces = append(pieces, piece{at(b.a, 0), at(b.b, 0), b.c0, b.c1})
		}
		pieces = append(pieces, piece{hi, math.Max(t1, hi) + 1, last, last})
	} else {
		k0, k1 := math.Floor((t0-lo)/period), math.Floor((t1-lo)/period)
		if k1-k0+1 > maxPeriods {
			out := []float64{0, 1, 0, 1, 0, 1}
			if gray {
				out = []float64{0, 1}
			}
			code := psNum(lo) + " sub " + psNum(period) + " div " + psExtend(e) + " " + psColour(stops, gray)
			return type4([]float64{t0, t1}, out, code)
		}
		for k := k0; k <= k1; k++ {
			mirrored := e == shape.ExtendReflect && int64(k)%2 != 0
			if !mirrored {
				for _, b := range base {
					pieces = append(pieces, piece{at(b.a, k), at(b.b, k), b.c0, b.c1})
				}
				continue
			}
			for i := len(base) - 1; i >= 0; i-- {
				b := base[i]
				pieces = append(pieces, piece{at(1-b.b, k), at(1-b.a, k), b.c1, b.c0})
			}
		}
	}
	// Cut to the domain.
	var fns, bounds, encode object.Array
	for _, pc := range pieces {
		a, b := math.Max(pc.a, t0), math.Min(pc.b, t1)
		if !(b > a) {
			continue
		}
		fns = append(fns, object.NewDictionary(
			object.Entry{Key: "FunctionType", Value: object.Integer(2)},
			object.Entry{Key: "Domain", Value: object.Array{object.Integer(0), object.Integer(1)}},
			object.Entry{Key: "C0", Value: nums(pc.c0)},
			object.Entry{Key: "C1", Value: nums(pc.c1)},
			object.Entry{Key: "N", Value: object.Integer(1)},
		))
		if len(fns) > 1 {
			bounds = append(bounds, realNumber(a))
		}
		encode = append(encode, realNumber((a-pc.a)/(pc.b-pc.a)), realNumber((b-pc.a)/(pc.b-pc.a)))
	}
	return object.NewDictionary(
		object.Entry{Key: "FunctionType", Value: object.Integer(3)},
		object.Entry{Key: "Domain", Value: nums([]float64{t0, t1})},
		object.Entry{Key: "Functions", Value: fns},
		object.Entry{Key: "Bounds", Value: bounds},
		object.Entry{Key: "Encode", Value: encode},
	)
}

// coverLimit bounds how far, in lengths of the colour line, a repeating or
// reflected radial gradient is drawn out when no circle covers the clip.
const coverLimit = 64

// maxPeriods bounds the periods of a repeating or reflected gradient written
// as stitched pieces.
const maxPeriods = 512

func (p *colrPainter) LinearGradient(g shape.LinearGradient) {
	p.gradient(g.Line, func(_ object.Object, gray bool, stops []stop, lo, hi float64) *object.Dictionary {
		// The colour line runs from P0 to P3, P1 projected onto the
		// perpendicular of P0P2: the colours stand along lines parallel to
		// P0P2.
		p0, p1, p2 := g.P0, g.P1, g.P2
		nx, ny := -(p2.Y - p0.Y), p2.X-p0.X
		p3 := p1
		if d := nx*nx + ny*ny; d > 0 {
			k := ((p1.X-p0.X)*nx + (p1.Y-p0.Y)*ny) / d
			p3 = shape.Point{X: p0.X + k*nx, Y: p0.Y + k*ny}
		}
		dx, dy := p3.X-p0.X, p3.Y-p0.Y
		if dx == 0 && dy == 0 {
			return nil
		}
		// t along the line at each corner of the clip, which the shading's
		// domain has to cover.
		t0, t1 := lo, hi
		bb := p.local()
		for _, c := range [][2]float64{{bb.x0, bb.y0}, {bb.x1, bb.y0}, {bb.x0, bb.y1}, {bb.x1, bb.y1}} {
			t := ((c[0]-p0.X)*dx + (c[1]-p0.Y)*dy) / (dx*dx + dy*dy)
			t0, t1 = math.Min(t0, t), math.Max(t1, t)
		}
		at := func(t float64) (float64, float64) { return p0.X + t*dx, p0.Y + t*dy }
		x0, y0 := at(t0)
		x1, y1 := at(t1)
		return shadingDict(2, gray, object.Array{realNumber(x0), realNumber(y0), realNumber(x1), realNumber(y1)},
			p.doc.Add(lineFunction(stops, lo, hi, t0, t1, g.Line.Extend, gray)), t0, t1)
	})
}

func (p *colrPainter) RadialGradient(g shape.RadialGradient) {
	p.gradient(g.Line, func(_ object.Object, gray bool, stops []stop, lo, hi float64) *object.Dictionary {
		// The circle at t is C0 + t(C1−C0), R0 + t(R1−R0). The domain runs
		// to where the circles cover the clip, going outward, and back no
		// further than where the radius is zero.
		cx := func(t float64) float64 { return g.C0.X + t*(g.C1.X-g.C0.X) }
		cy := func(t float64) float64 { return g.C0.Y + t*(g.C1.Y-g.C0.Y) }
		r := func(t float64) float64 { return g.R0 + t*(g.R1-g.R0) }
		t0, t1 := lo, hi
		if g.Line.Extend != shape.ExtendPad {
			// The circle at t covers the clip once its radius reaches the
			// clip's farthest corner from its centre: r0 + t·dr ≥ D + |t|·|ΔC|,
			// D the farthest corner from C0. Circles that grow no faster than
			// their centres move never cover it all, and are taken out to
			// coverLimit times the line.
			bb := p.local()
			far := 0.0
			for _, c := range [][2]float64{{bb.x0, bb.y0}, {bb.x1, bb.y0}, {bb.x0, bb.y1}, {bb.x1, bb.y1}} {
				far = math.Max(far, math.Hypot(c[0]-g.C0.X, c[1]-g.C0.Y))
			}
			move := math.Hypot(g.C1.X-g.C0.X, g.C1.Y-g.C0.Y)
			reach := func(dr float64) float64 {
				if dr > move {
					return (far - g.R0) / (dr - move)
				}
				return coverLimit
			}
			if dr := g.R1 - g.R0; dr > 0 {
				t1 = math.Max(t1, math.Min(reach(dr), coverLimit))
				t0 = math.Max(math.Min(t0, -g.R0/dr), -g.R0/dr)
			} else if dr < 0 {
				t0 = math.Min(t0, -math.Min(reach(-dr), coverLimit))
				t1 = math.Min(math.Max(t1, -g.R0/dr), -g.R0/dr)
			}
		}
		if r(t0) < 0 || r(t1) < 0 {
			t0, t1 = 0, 1
		}
		coords := object.Array{realNumber(cx(t0)), realNumber(cy(t0)), realNumber(math.Max(r(t0), 0)),
			realNumber(cx(t1)), realNumber(cy(t1)), realNumber(math.Max(r(t1), 0))}
		return shadingDict(3, gray, coords, p.doc.Add(lineFunction(stops, lo, hi, t0, t1, g.Line.Extend, gray)), t0, t1)
	})
}

func (p *colrPainter) SweepGradient(g shape.SweepGradient) {
	p.gradient(g.Line, func(_ object.Object, gray bool, stops []stop, lo, hi float64) *object.Dictionary {
		bb := p.local()
		span := g.EndAngle - g.StartAngle
		out := []float64{0, 1, 0, 1, 0, 1}
		if gray {
			out = []float64{0, 1}
		}
		// The angle about the centre, from 0 to 360 degrees counter-clockwise
		// from the x axis, as radians.
		angle := psNum(g.Center.Y) + " sub exch " + psNum(g.Center.X) + " sub" +
			" 2 copy abs exch abs add 0 eq { pop pop 0 } { atan } ifelse " + psNum(math.Pi/180) + " mul"
		coincident := true
		for _, s := range g.Line.Stops {
			coincident = coincident && s.Offset == g.Line.Stops[0].Offset
		}
		if coincident && g.Line.Extend != shape.ExtendPad {
			// Every stop at one offset, repeated or reflected: nothing, as
			// Skia draws it. (A linear or radial line of the kind is drawn
			// as the step a padded one is; the conformance font has none of
			// them to say otherwise.)
			return nil
		}
		var t string
		if math.Abs(span) < 1e-6 {
			// A sweep that starts where it ends: padded, a step there,
			// the line's first colour before it and its last after; repeated
			// or reflected, nothing, as Skia draws them.
			if g.Line.Extend != shape.ExtendPad {
				return nil
			}
			t = " " + psNum(g.StartAngle) + " lt { " + psNum(lo-1) + " } { " + psNum(hi+1) + " } ifelse"
		} else {
			// Along the sweep from start to end, in the colour line's t.
			t = " " + psNum(g.StartAngle) + " sub " + psNum(span) + " div"
		}
		code := angle + t + " " + psNum(lo) + " sub " + psNum(hi-lo) + " div " + psExtend(g.Line.Extend) + " " + psColour(stops, gray)
		fn := type4([]float64{bb.x0, bb.x1, bb.y0, bb.y1}, out, code)
		d := &object.Dictionary{}
		d.Set("ShadingType", object.Integer(1))
		d.Set("ColorSpace", spaceOf(gray))
		d.Set("Domain", nums([]float64{bb.x0, bb.x1, bb.y0, bb.y1}))
		// Identity, which is the default (ISO 32000-2 8.7.4.5.2), written
		// out: Ghostscript 10.02 refuses a function-based shading without it.
		d.Set("Matrix", object.Array{object.Integer(1), object.Integer(0), object.Integer(0),
			object.Integer(1), object.Integer(0), object.Integer(0)})
		d.Set("Function", p.doc.Add(fn))
		return d
	})
}

func spaceOf(gray bool) object.Name {
	if gray {
		return "DeviceGray"
	}
	return "DeviceRGB"
}

// shadingDict is an axial (2) or radial (3) shading over t0 to t1, extended
// at both ends: the function itself says what lies past the colour line.
func shadingDict(kind int, gray bool, coords object.Array, fn object.Object, t0, t1 float64) *object.Dictionary {
	d := &object.Dictionary{}
	d.Set("ShadingType", object.Integer(kind))
	d.Set("ColorSpace", spaceOf(gray))
	d.Set("Coords", coords)
	d.Set("Domain", nums([]float64{t0, t1}))
	d.Set("Function", fn)
	d.Set("Extend", object.Array{object.Boolean(true), object.Boolean(true)})
	return d
}

// colrArt is a colour glyph's procedure content and the resources it names:
// what paintColour writes, kept per glyph and replayed into each embedding.
type colrArt struct {
	cap      *capture
	content  []byte
	res      []resKey
	refs     map[resKey]int // the capture's own reference to each resource
	coloured bool
	inked    box // in font units
}

// paintColour paints a glyph through the translator.
func (f *Face) paintColour(gid int, fg *content.Color, root box) (*colrArt, error) {
	c := &capture{}
	p := newColrPainter(f, c, gid, fg, root)
	if err := f.PaintGlyph(gid, shape.PaintOptions{}, p); err != nil {
		return nil, fmt.Errorf("fonts: painting glyph %d: %w", gid, err)
	}
	if p.err != nil {
		return nil, p.err
	}
	if len(p.head) != 1 || len(p.open) != 1 {
		return nil, fmt.Errorf("fonts: glyph %d's painting does not nest", gid)
	}
	art := &colrArt{cap: c, content: p.head[0].buf, refs: map[resKey]int{}, coloured: p.coloured, inked: p.inked}
	for k := range p.head[0].used {
		art.res = append(art.res, k)
	}
	for k, ref := range p.res {
		art.refs[k] = -ref.Number
	}
	slices.SortFunc(art.res, compareResKeys)
	return art, nil
}

// compareResKeys orders resources by kind and name, so that what is written
// does not depend on map order.
func compareResKeys(a, b resKey) int {
	if c := strings.Compare(a.kind, b.kind); c != 0 {
		return c
	}
	return strings.Compare(string(a.name), string(b.name))
}
