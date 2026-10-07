package fonts

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/mgilbir/forme/shape"
	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/object"
)

// A face whose glyphs are only bitmaps — EBDT, Apple's bdat, CBDT or sbix, and
// no outlines — is written as Type 3 fonts, whose glyphs are content streams
// that paint the bitmaps (ISO 32000-2 9.6.4). A font program without outlines
// would be drawn blank by every reader. The design record is
// docs/proposals/bitmap-fonts-type3.md.
//
// A Type 3 font is a simple font: one-byte codes, 256 glyphs. And what a
// glyph's procedure paints depends on more than the glyph: on the strike the
// text size picks, and, for a greyscale strike, on the text colour, which a
// procedure that keeps its anti-aliasing has to carry itself. So a bitmap face
// is a set of sub-fonts, each one Type 3 font, keyed by strike and colour, and
// split into planes of 256 codes. The caller names the face once, as it names
// any face; the first sub-font, the home font, is that name, and the others
// are SubfontName of it. Drawing switches between them with Tf inside a run
// and selects the caller's name again after it.

// maxCodes is how many glyphs one Type 3 font addresses: its codes are one
// byte. It is a variable only so a test can split a face with a few glyphs
// across planes.
var maxCodes = 256

// SubfontName is the font name a bitmap face's sub-font k is selected under,
// when the face itself is selected under base: base for the home font, k = 0,
// and base.k for the others. A page naming the face under base defines each of
// these in its /Resources /Font, with the font dictionaries EmbedForms returns
// in Embedded.Subfonts.
func SubfontName(base object.Name, k int) object.Name {
	if k == 0 {
		return base
	}
	return base + "." + object.Name(strconv.Itoa(k))
}

// isType3 reports whether the face is written as Type 3 fonts: a face whose
// glyphs are only bitmaps, or one whose licence permits embedding only its
// bitmaps and that has bitmaps forme paints (bitmapLicensed). Such a face is
// loaded composite — LoadSimple refuses one with only bitmaps — and shapes
// and records as one, and only the codes it writes and the fonts it embeds
// differ.
func (f *Face) isType3() bool {
	return f.composite() && (f.hasBitmaps() || f.colourFace())
}

// hasBitmaps reports whether the face's glyphs are drawn from bitmap strikes:
// it has nothing else, or its licence permits embedding nothing else.
func (f *Face) hasBitmaps() bool { return f.BitmapOnly() || f.bitmapLicensed() }

// SetColourGlyphs says whether a face with COLR colour glyphs is drawn in
// colour, as Type 3 fonts whose glyphs paint them (the default), or as it was
// before, its outlines embedded as a font program and shown in the text
// colour. It has to be said before the face draws anything, since the two
// write different codes, and it changes nothing for a face without COLR
// glyphs.
func (f *Face) SetColourGlyphs(on bool) {
	f.Horizontal().noColour = !on
	f.Horizontal().coloured = 0
}

// colourFace reports whether the face is drawn as its COLR colour glyphs:
// it has one, and SetColourGlyphs has not said otherwise. Asked once.
func (f *Face) colourFace() bool {
	h := f.Horizontal()
	if h.noColour {
		return false
	}
	if h.coloured != 0 {
		return h.coloured > 0
	}
	h.coloured = -1
	for gid := range f.NumGlyphs() {
		if k := f.GlyphColour(gid, 0); k == shape.ColourLayers || k == shape.ColourPaint {
			h.coloured = 1
			return true
		}
	}
	return false
}

// vectorGlyph reports whether a glyph of a Type 3 face is painted as vectors,
// through the COLR translator (colr.go): a colour glyph, and in a face with
// no bitmaps every glyph, a plain one being its outline in the text colour.
func (f *Face) vectorGlyph(gid int) bool {
	if !f.colourFace() {
		return false
	}
	if k := f.GlyphColour(gid, 0); k == shape.ColourLayers || k == shape.ColourPaint {
		return true
	}
	return !f.hasBitmaps()
}

// bitmapLicensed reports whether a face with outlines may be embedded only as
// its bitmaps (OS/2 fsType 0x0200) and has bitmaps forme paints: CBDT or sbix
// images, which forme paints whatever outlines the face has. Its EBDT strikes
// it does not paint beside outlines (forme#918), and such a face is refused
// with ErrBitmapEmbeddingOnly. The answer is the face's, fixed when it was
// loaded, and asked once.
func (f *Face) bitmapLicensed() bool {
	if f.licensed != 0 {
		return f.licensed > 0
	}
	f.licensed = -1
	fsType, stated := f.EmbeddingPermissions()
	if !stated || fsType&shape.FSTypeBitmapOnly == 0 {
		return false
	}
	for gid := range f.NumGlyphs() {
		if f.GlyphColour(gid, 0) == shape.ColourBitmap {
			f.licensed = 1
			return true
		}
	}
	return false
}

// errOutlineOnly is a glyph of a face that may be embedded only as its
// bitmaps (bitmapLicensed) that has ink and no bitmap in any strike: what it
// draws is its outline, which its licence forbids embedding.
func errOutlineOnly(gid int) error {
	return fmt.Errorf("%w: glyph %d has an outline and no bitmap", ErrBitmapEmbeddingOnly, gid)
}

// subKey is what decides a glyph's procedure, beside the glyph.
type subKey struct {
	// vector marks a font of glyphs painted as vectors (vectorGlyph), whose
	// strike means nothing.
	vector bool
	// strike is the ppem of the strike the glyphs are painted from.
	strike int
	// colour is the text colour a greyscale glyph is painted in, written as
	// colourKey writes it, or "" for a font whose glyphs carry no colour of
	// the text: stencils, colour bitmaps, and greyscale glyphs set in a colour
	// this cannot bake (see place).
	colour string
}

// subFont is one Type 3 font of a bitmap face.
type subFont struct {
	key   subKey
	index int          // creation order: 0 is the home font
	codes map[int]byte // glyph -> code
	gids  []int        // code -> glyph
}

// glyphKind is what a glyph's image is.
type glyphKind uint8

const (
	glyphEmpty  glyphKind = iota // nothing to paint, such as a space
	glyphMask                    // coverage that is all 0 or 255: a stencil
	glyphGrey                    // partial coverage: anti-aliased
	glyphColour                  // a colour bitmap, a PNG
)

// bitmapGlyph is a glyph as one strike paints it.
type bitmapGlyph struct {
	kind glyphKind
	img  shape.Image // Box in font units; Data is the font's own bytes
}

// type3State is a bitmap face's sub-fonts and what it has learnt about its
// strikes. It belongs to the wrapper, as the text record does: a Clone draws a
// different document.
type type3State struct {
	subs  []*subFont
	byKey map[subKey][]*subFont // a key's planes, in order
	// colours are the colours the keys name, by key.
	colours map[string]content.Color
	// strikeOf is the strike a requested ppem paints from, and glyphs a
	// glyph as a strike paints it, falling back to the nearest strike that
	// has it.
	strikeOf map[int]int
	glyphs   map[[2]int]bitmapGlyph
	// fgStops says whether a vector glyph has a gradient stop in the text
	// colour, by glyph; arts are vector glyphs' paintings, by key and glyph.
	fgStops map[int]bool
	arts    map[builtKey]*colrArt
	// assigned counts the codes given out, for EmbedRevision.
	assigned int
	// built holds each glyph's image objects once written, by sub-font key
	// and glyph, so a document re-embedding the face as it grows does not
	// decode and compress them again.
	built map[builtKey]*capture
}

type builtKey struct {
	key    subKey
	gid    int
	opaque bool
}

func (f *Face) type3() *type3State {
	if f.t3 == nil {
		f.t3 = &type3State{
			byKey:    map[subKey][]*subFont{},
			colours:  map[string]content.Color{},
			strikeOf: map[int]int{},
			glyphs:   map[[2]int]bitmapGlyph{},
			built:    map[builtKey]*capture{},
			fgStops:  map[int]bool{},
			arts:     map[builtKey]*colrArt{},
		}
	}
	return f.t3
}

// SetPixelsPerUnit says how many CSS pixels one unit of the size this face is
// drawn at is: 4/3, the default, for a size in points, and 1 for a caller
// whose content stream is in CSS pixels, as htmlpdf's is. It matters only for
// a face whose glyphs are only bitmaps, whose strike is picked by the size in
// CSS pixels — the strike a browser at 1× draws, since a strike is designed for
// the pixel size it is shown at. A value that is not positive and finite
// restores the default.
func (f *Face) SetPixelsPerUnit(v float64) {
	if !(v > 0) || math.IsInf(v, 0) {
		v = 0
	}
	f.pxPerUnit = v
}

// ppemFor is the strike size a text size asks for: the size in CSS pixels.
func (f *Face) ppemFor(size float64) int {
	scale := f.pxPerUnit
	if scale == 0 {
		scale = 4.0 / 3
	}
	p := int(math.Round(size * scale))
	if p < 1 {
		p = 1
	}
	return p
}

// errUnpaintable is a bitmap glyph painted with something other than one
// image placed by its box.
var errUnpaintable = errors.New("fonts: a bitmap glyph is painted with something a Type 3 glyph here cannot draw")

// imageCapture is a shape.Painter that keeps the one image a bitmap glyph is
// painted as, with the transforms around it applied to its box.
type imageCapture struct {
	stack []shape.Transform
	img   shape.Image
	got   bool
	err   error
}

func (c *imageCapture) current() shape.Transform {
	if len(c.stack) == 0 {
		return shape.Transform{XX: 1, YY: 1}
	}
	return c.stack[len(c.stack)-1]
}

func (c *imageCapture) PushTransform(t shape.Transform) {
	p := c.current()
	// The inner transform first, then the outer one.
	c.stack = append(c.stack, shape.Transform{
		XX: p.XX*t.XX + p.XY*t.YX,
		YX: p.YX*t.XX + p.YY*t.YX,
		XY: p.XX*t.XY + p.XY*t.YY,
		YY: p.YX*t.XY + p.YY*t.YY,
		X0: p.XX*t.X0 + p.XY*t.Y0 + p.X0,
		Y0: p.YX*t.X0 + p.YY*t.Y0 + p.Y0,
	})
}

func (c *imageCapture) PopTransform() {
	if len(c.stack) > 0 {
		c.stack = c.stack[:len(c.stack)-1]
	}
}

func (c *imageCapture) unsupported(what string) {
	if c.err == nil {
		c.err = fmt.Errorf("%w: %s", errUnpaintable, what)
	}
}

func (c *imageCapture) PushClipGlyph(int)                   { c.unsupported("a clip to an outline") }
func (c *imageCapture) PushClipRect(shape.Rect)             { c.unsupported("a clip") }
func (c *imageCapture) PopClip()                            {}
func (c *imageCapture) PushGroup()                          { c.unsupported("a compositing group") }
func (c *imageCapture) PopGroup(shape.CompositeMode)        {}
func (c *imageCapture) Solid(shape.Color, bool)             { c.unsupported("a solid fill") }
func (c *imageCapture) LinearGradient(shape.LinearGradient) { c.unsupported("a gradient") }
func (c *imageCapture) RadialGradient(shape.RadialGradient) { c.unsupported("a gradient") }
func (c *imageCapture) SweepGradient(shape.SweepGradient)   { c.unsupported("a gradient") }

func (c *imageCapture) Image(img shape.Image) {
	if c.got {
		c.unsupported("more than one image")
		return
	}
	t := c.current()
	if t.YX != 0 || t.XY != 0 || t.XX <= 0 || t.YY <= 0 {
		// An image is placed by its box, and a box only scales and moves.
		c.unsupported("an image turned or mirrored")
		return
	}
	b := img.Box
	img.Box = shape.Rect{
		XMin: t.XX*b.XMin + t.X0, YMin: t.YY*b.YMin + t.Y0,
		XMax: t.XX*b.XMax + t.X0, YMax: t.YY*b.YMax + t.Y0,
	}
	c.img, c.got = img, true
}

// paint is a glyph as the strike for ppem paints it, and whether it has an
// image there.
func (f *Face) paint(gid, ppem int) (bitmapGlyph, bool, error) {
	switch f.GlyphColour(gid, ppem) {
	case shape.ColourBitmap, shape.ColourMask:
	case shape.ColourSVG:
		return bitmapGlyph{}, false, fmt.Errorf("%w: glyph %d is an SVG document", errUnpaintable, gid)
	default:
		// A glyph with no image here: its outline, or a COLR glyph, which
		// is no bitmap.
		return bitmapGlyph{}, false, nil
	}
	var c imageCapture
	if err := f.PaintGlyph(gid, shape.PaintOptions{PPEM: ppem}, &c); err != nil {
		return bitmapGlyph{}, false, fmt.Errorf("fonts: painting glyph %d: %w", gid, err)
	}
	if c.err != nil {
		return bitmapGlyph{}, false, fmt.Errorf("glyph %d: %w", gid, c.err)
	}
	if !c.got || c.img.Width <= 0 || c.img.Height <= 0 ||
		!(c.img.Box.XMax > c.img.Box.XMin) || !(c.img.Box.YMax > c.img.Box.YMin) {
		return bitmapGlyph{}, false, nil
	}
	g := bitmapGlyph{img: c.img}
	switch c.img.Format {
	case shape.ImagePNG:
		g.kind = glyphColour
	case shape.ImageMask:
		if len(c.img.Data) != c.img.Width*c.img.Height {
			return bitmapGlyph{}, false, fmt.Errorf("fonts: glyph %d's coverage is %d bytes for %d×%d pixels",
				gid, len(c.img.Data), c.img.Width, c.img.Height)
		}
		g.kind = glyphMask
		for _, v := range c.img.Data {
			if v != 0 && v != 0xFF {
				g.kind = glyphGrey
				break
			}
		}
	default:
		return bitmapGlyph{}, false, fmt.Errorf("%w: glyph %d is an SVG document", errUnpaintable, gid)
	}
	return g, true, nil
}

// strikeOfImage is the ppem of the strike an image was painted from: its
// pixels per em across, or down for an image with no width.
func (f *Face) strikeOfImage(img shape.Image) int {
	upem := float64(f.UnitsPerEm())
	if w := img.Box.XMax - img.Box.XMin; w > 0 {
		return int(math.Round(float64(img.Width) * upem / w))
	}
	return int(math.Round(float64(img.Height) * upem / (img.Box.YMax - img.Box.YMin)))
}

// strikeFor is the strike a requested ppem paints from: forme picks it, and the
// first glyph with an image there says which it is.
func (t *type3State) strikeFor(f *Face, ppem int) (int, error) {
	if s, ok := t.strikeOf[ppem]; ok {
		return s, nil
	}
	s := ppem
	for gid := range f.NumGlyphs() {
		g, ok, err := f.paint(gid, ppem)
		if err != nil {
			return 0, err
		}
		if ok {
			s = f.strikeOfImage(g.img)
			break
		}
	}
	t.strikeOf[ppem] = s
	return s, nil
}

// maxProbePPEM bounds the sizes asked about when a glyph is missing from the
// strike its text size picks. forme names no face's strikes, so they are found
// by asking for each size: every strike up to this size, and the largest,
// which a request past every strike paints from.
const maxProbePPEM = 256

// glyphAt is a glyph as strike paints it, or, when that strike has no image for
// it, as the nearest strike that has one does — the larger of two as near. A
// glyph no strike has an image for, such as a space, is empty.
func (t *type3State) glyphAt(f *Face, gid, strike int) (bitmapGlyph, error) {
	k := [2]int{gid, strike}
	if g, ok := t.glyphs[k]; ok {
		return g, nil
	}
	g, ok, err := f.paint(gid, strike)
	if err != nil {
		return bitmapGlyph{}, err
	}
	if !ok {
		g = bitmapGlyph{kind: glyphEmpty}
		if !f.BitmapOnly() {
			// A face with outlines whose licence permits only its bitmaps:
			// a glyph no strike has may be nothing, as a space is, or its
			// outline, which cannot be embedded. forme paints its colour
			// strikes at any size, so there is no other strike to try.
			// .notdef stands for a character the face does not have, and is
			// drawn as nothing rather than as its outline.
			if _, _, w, h, ok := f.GlyphExtents(gid); gid != 0 && ok && w != 0 && h != 0 {
				return bitmapGlyph{}, errOutlineOnly(gid)
			}
			t.glyphs[k] = g
			return g, nil
		}
		best := -1
		// 0 asks for the largest strike. Each size from 1 up asks for the
		// smallest strike at least that large, so once one is found the sizes
		// up to it ask for it again and are skipped.
		for p := 0; p <= maxProbePPEM; p++ {
			cand, ok, err := f.paint(gid, p)
			if err != nil {
				return bitmapGlyph{}, err
			}
			if !ok {
				continue
			}
			s := f.strikeOfImage(cand.img)
			if best < 0 || nearer(s, best, strike) {
				g, best = cand, s
			}
			if p > 0 && s > p {
				p = s
			}
		}
	}
	t.glyphs[k] = g
	return g, nil
}

// nearer reports whether strike a is nearer to want than b, the larger of two
// as near: a strike drawn smaller is sharper than one drawn larger.
func nearer(a, b, want int) bool {
	da, db := abs(a-want), abs(b-want)
	if da != db {
		return da < db
	}
	return a > b
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// colourKey is a fill colour as a sub-font key names it, and whether it is one
// a glyph can be painted in: a device colour, whose space needs no resource.
func colourKey(c content.Color, ok bool) (string, bool) {
	if !ok || !c.IsDevice() {
		return "", false
	}
	var b strings.Builder
	b.WriteString(string(c.Space))
	for _, v := range c.Components {
		b.WriteByte(' ')
		b.WriteString(strconv.FormatFloat(v, 'g', -1, 64))
	}
	return b.String(), true
}

// home is the home font: the largest strike, no colour. It is the one the
// caller's own name selects, and what Encode and Shape write codes in, having
// no size or colour to choose another by. Its first code is .notdef, so that
// it is never empty: a page names it whatever the face draws.
func (t *type3State) home(f *Face) (*subFont, error) {
	if len(t.subs) > 0 {
		return t.subs[0], nil
	}
	key := subKey{vector: true}
	if f.hasBitmaps() {
		s, err := t.strikeFor(f, 0)
		if err != nil {
			return nil, err
		}
		key = subKey{strike: s}
	}
	sub := t.newSub(key)
	t.assign(sub, 0)
	return sub, nil
}

func (t *type3State) newSub(key subKey) *subFont {
	sub := &subFont{key: key, index: len(t.subs), codes: map[int]byte{}}
	t.subs = append(t.subs, sub)
	t.byKey[key] = append(t.byKey[key], sub)
	return sub
}

func (t *type3State) assign(sub *subFont, gid int) byte {
	code := byte(len(sub.gids))
	sub.codes[gid] = code
	sub.gids = append(sub.gids, gid)
	t.assigned++
	return code
}

// in returns the sub-font of key that has gid, giving it a code in the first
// plane with room, or in a new plane.
func (t *type3State) in(key subKey, gid int) (*subFont, byte) {
	planes := t.byKey[key]
	for _, sub := range planes {
		if code, ok := sub.codes[gid]; ok {
			return sub, code
		}
	}
	for _, sub := range planes {
		if len(sub.gids) < maxCodes {
			return sub, t.assign(sub, gid)
		}
	}
	sub := t.newSub(key)
	return sub, t.assign(sub, gid)
}

// place is the sub-font and code a glyph is drawn with, at a size and in a
// fill colour, when the font current in the stream is current.
//
// An empty glyph paints nothing whatever its font, and is drawn in the current
// one when it has the strike, rather than switching for a space. A greyscale
// glyph in a colour that is not a device colour, or that the stream has not
// set, is drawn as a stencil of its coverage, which takes whatever colour the
// text is painted in and loses its anti-aliasing (a known limit; see the
// design record).
func (t *type3State) place(f *Face, gid int, size float64, fill content.Color, fillKnown bool, current *subFont) (*subFont, byte, error) {
	if _, err := t.home(f); err != nil {
		return nil, 0, err
	}
	if f.vectorGlyph(gid) {
		// A vector glyph is the same at every size, and carries the text
		// colour by not setting one, unless a gradient of it has to state
		// the colour (colr.go).
		key := subKey{vector: true}
		if t.foregroundStops(f, gid) {
			if c, ok := colourKey(fill, fillKnown); ok {
				key.colour = c
				t.colours[c] = fill
			}
		}
		sub, code := t.in(key, gid)
		return sub, code, nil
	}
	s, err := t.strikeFor(f, f.ppemFor(size))
	if err != nil {
		return nil, 0, err
	}
	g, err := t.glyphAt(f, gid, s)
	if err != nil {
		return nil, 0, err
	}
	key := subKey{strike: s}
	switch {
	case g.kind == glyphEmpty && current != nil && !current.key.vector && current.key.strike == s:
		key = current.key
	case g.kind == glyphGrey:
		if c, ok := colourKey(fill, fillKnown); ok {
			key.colour = c
			t.colours[c] = fill
		}
	}
	sub, code := t.in(key, gid)
	return sub, code, nil
}

// homeCode is the code gid has in the home font's first plane, for a caller
// writing bare codes, and false when that plane is full.
func (t *type3State) homeCode(f *Face, gid int) (byte, bool, error) {
	home, err := t.home(f)
	if err != nil {
		return 0, false, err
	}
	if code, ok := home.codes[gid]; ok {
		return code, true, nil
	}
	if len(home.gids) >= maxCodes {
		return 0, false, nil
	}
	return t.assign(home, gid), true, nil
}

// errType3NoFont is a bitmap face drawn into a stream that has not selected a
// font: the face switches between its Type 3 fonts by name, and the names are
// the caller's, derived from the one it selected the face under.
var errType3NoFont = errors.New("fonts: a face whose glyphs are only bitmaps is drawn as several Type 3 fonts " +
	"named after the font the stream selected, and this stream has selected none; select the face with SetFont first")

// homeCode is a glyph's code in the home font's first plane, for Encode and
// Shape, and false when it cannot be written there: the plane's 256 codes are
// given out, or the face's strikes cannot be read.
func (f *Face) homeCode(gid int) (byte, bool) {
	c, ok, err := f.type3().homeCode(f, gid)
	return c, ok && err == nil
}

// NumSubfonts is how many fonts a face whose glyphs are only bitmaps has drawn
// with beside its home font: SubfontName(name, 1) to SubfontName(name, n) are
// the names its drawing has selected, and Embedded.Subfonts their font
// dictionaries. It is 0 for every other face.
func (f *Face) NumSubfonts() int {
	f = f.Horizontal()
	if f.t3 == nil || len(f.t3.subs) == 0 {
		return 0
	}
	return len(f.t3.subs) - 1
}

// foregroundStops reports whether a vector glyph has a gradient stop in the
// text colour, which its shading has to state, so that its fonts are keyed by
// the colour. Asked once a glyph.
func (t *type3State) foregroundStops(f *Face, gid int) bool {
	if v, ok := t.fgStops[gid]; ok {
		return v
	}
	var d stopFinder
	_ = f.PaintGlyph(gid, shape.PaintOptions{}, &d)
	t.fgStops[gid] = d.found
	return d.found
}

// stopFinder is a painter that looks for a gradient stop in the foreground.
type stopFinder struct{ found bool }

func (d *stopFinder) line(l shape.ColorLine) {
	for _, s := range l.Stops {
		d.found = d.found || s.Foreground
	}
}

func (d *stopFinder) PushTransform(shape.Transform)         {}
func (d *stopFinder) PopTransform()                         {}
func (d *stopFinder) PushClipGlyph(int)                     {}
func (d *stopFinder) PushClipRect(shape.Rect)               {}
func (d *stopFinder) PopClip()                              {}
func (d *stopFinder) PushGroup()                            {}
func (d *stopFinder) PopGroup(shape.CompositeMode)          {}
func (d *stopFinder) Solid(shape.Color, bool)               {}
func (d *stopFinder) Image(shape.Image)                     {}
func (d *stopFinder) LinearGradient(g shape.LinearGradient) { d.line(g.Line) }
func (d *stopFinder) RadialGradient(g shape.RadialGradient) { d.line(g.Line) }
func (d *stopFinder) SweepGradient(g shape.SweepGradient)   { d.line(g.Line) }
