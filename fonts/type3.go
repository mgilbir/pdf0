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
// its bitmaps (OS/2 fsType 0x0200) and has bitmaps: any strike, CBDT, sbix,
// EBDT or bdat, which forme reads beside outlines (Face.StrikeImage; forme
// 0.9.0 for EBDT and bdat, forme#918). A face whose licence says so and that
// has no strike is refused with ErrBitmapEmbeddingOnly. The answer is the
// face's, fixed when it was loaded, and asked once.
func (f *Face) bitmapLicensed() bool {
	if f.licensed != 0 {
		return f.licensed > 0
	}
	f.licensed = -1
	fsType, stated := f.EmbeddingPermissions()
	if !stated || fsType&shape.FSTypeBitmapOnly == 0 || len(f.Strikes()) == 0 {
		return false
	}
	f.licensed = 1
	return true
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
	// strike is the strike the glyphs are drawn from: an index into the
	// face's strikes (strikeList), or noStrike.
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
	// strikes are the face's bitmap strikes (strikeList), and glyphs a
	// glyph as a strike draws it, falling back to the nearest strike that
	// has it.
	strikes []shape.Strike
	glyphs  map[[2]int]bitmapGlyph
	// fgStops says whether a vector glyph has a gradient stop in the text
	// colour, by glyph; arts are vector glyphs' paintings, by key and glyph.
	fgStops map[int]bool
	arts    map[builtKey]*colrArt
	// procs are the glyph procedures already compressed (procStream).
	procs map[procKey]compressedProc
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
			byKey:   map[subKey][]*subFont{},
			colours: map[string]content.Color{},
			glyphs:  map[[2]int]bitmapGlyph{},
			built:   map[builtKey]*capture{},
			fgStops: map[int]bool{},
			arts:    map[builtKey]*colrArt{},
			procs:   map[procKey]compressedProc{},
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

// strikeList is the face's bitmap strikes, smallest first, as forme lists
// them (shape.Face.Strikes): CBDT's, sbix's, and EBDT's or bdat's. A
// sub-font's strike is an index into it.
func (t *type3State) strikeList(f *Face) []shape.Strike {
	if t.strikes == nil {
		t.strikes = append([]shape.Strike{}, f.Strikes()...)
	}
	return t.strikes
}

// noStrike is the strike of a face that has none.
const noStrike = -1

// strikeFor is the strike a requested ppem paints from, as forme chooses it
// for PaintOptions.PPEM: the smallest at least that large, or, failing any,
// the largest; 0 asks for the largest.
func (t *type3State) strikeFor(f *Face, ppem int) int {
	list := t.strikeList(f)
	if len(list) == 0 {
		return noStrike
	}
	if ppem > 0 {
		for i, s := range list {
			if s.PPEM() >= ppem {
				return i
			}
		}
	}
	return len(list) - 1
}

// bitmapOf is a strike's image of a glyph as the procedure draws it.
func bitmapOf(gid int, img shape.Image) (bitmapGlyph, bool, error) {
	if img.Width <= 0 || img.Height <= 0 || !(img.Box.XMax > img.Box.XMin) || !(img.Box.YMax > img.Box.YMin) {
		return bitmapGlyph{}, false, nil
	}
	g := bitmapGlyph{img: img}
	switch img.Format {
	case shape.ImagePNG:
		g.kind = glyphColour
	case shape.ImageMask:
		if len(img.Data) != img.Width*img.Height {
			return bitmapGlyph{}, false, fmt.Errorf("fonts: glyph %d's coverage is %d bytes for %d×%d pixels",
				gid, len(img.Data), img.Width, img.Height)
		}
		g.kind = glyphMask
		for _, v := range img.Data {
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

// glyphAt is a glyph as a strike draws it (shape.Face.StrikeImage), or, when
// that strike has no image of it, as the nearest strike that has one does —
// the larger of two as near, a strike drawn smaller being sharper than one
// drawn larger. A glyph no strike has an image of, such as a space, is empty,
// unless the face's outlines draw it: in a face whose licence permits
// embedding only its bitmaps that outline cannot be embedded, and the glyph
// is refused. .notdef, which stands for a character the face does not have,
// is drawn as nothing.
func (t *type3State) glyphAt(f *Face, gid, strike int) (bitmapGlyph, error) {
	k := [2]int{gid, strike}
	if g, ok := t.glyphs[k]; ok {
		return g, nil
	}
	if f.GlyphColour(gid, 0) == shape.ColourSVG {
		return bitmapGlyph{}, fmt.Errorf("%w: glyph %d is an SVG document", errUnpaintable, gid)
	}
	list := t.strikeList(f)
	g := bitmapGlyph{kind: glyphEmpty}
	found := false
	if strike >= 0 && strike < len(list) {
		img, ok := f.StrikeImage(gid, list[strike])
		var err error
		if ok {
			if g, found, err = bitmapOf(gid, img); err != nil {
				return bitmapGlyph{}, err
			}
		}
	}
	if !found {
		best := -1
		for i, s := range list {
			if i == strike {
				continue
			}
			img, ok := f.StrikeImage(gid, s)
			if !ok {
				continue
			}
			cand, ok, err := bitmapOf(gid, img)
			if err != nil {
				return bitmapGlyph{}, err
			}
			if ok && (best < 0 || nearer(s.PPEM(), list[best].PPEM(), want(list, strike))) {
				g, best, found = cand, i, true
			}
		}
	}
	if !found {
		g = bitmapGlyph{kind: glyphEmpty}
		if !f.BitmapOnly() {
			if _, _, w, h, ok := f.GlyphExtents(gid); gid != 0 && ok && w != 0 && h != 0 {
				return bitmapGlyph{}, errOutlineOnly(gid)
			}
		}
	}
	t.glyphs[k] = g
	return g, nil
}

// want is the size a strike stands for in choosing the nearest other one.
func want(list []shape.Strike, strike int) int {
	if strike >= 0 && strike < len(list) {
		return list[strike].PPEM()
	}
	return 0
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
		key = subKey{strike: t.strikeFor(f, 0)}
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
	s := t.strikeFor(f, f.ppemFor(size))
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
