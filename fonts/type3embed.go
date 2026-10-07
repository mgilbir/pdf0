package fonts

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"strconv"

	"github.com/mgilbir/forme/shape"
	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/images"
	"github.com/mgilbir/pdf0/object"
)

// Writing a bitmap face's sub-fonts as Type 3 fonts (ISO 32000-2 9.6.4). See
// type3.go for what a sub-font is.
//
// Glyph space is a thousandth of an em, as /FontMatrix [0.001 0 0 0.001 0 0]
// says, so a glyph's advance is the same number /W states for a composite
// face, and /Widths and each procedure's wx are both the face's own advance,
// which PDF/A checks against each other. Each glyph is one of:
//
//   - a stencil: wx 0 llx lly urx ury d1, then the coverage as an image mask,
//     which paints in the colour the text is shown in;
//   - a greyscale glyph in a colour: wx 0 d0, then that colour as an image
//     whose /SMask is the coverage;
//   - a colour bitmap: wx 0 d0, then the PNG decoded, with an /SMask for its
//     alpha;
//   - nothing: wx 0 0 0 0 0 d1, for a glyph with no image, such as a space.
//
// The image is drawn over the box the font places it in, scaled from font
// units to glyph space.

// capture is an Allocator that keeps what is added, under references of its
// own, so the objects can be added to a document later, and again.
type capture struct{ objs []object.Object }

func (c *capture) Add(o object.Object) object.IndirectRef {
	c.objs = append(c.objs, o)
	return object.IndirectRef{Number: -len(c.objs)}
}

// replay adds the captured objects to doc, in the order they were captured,
// each reference among them rewritten to the object's number in doc, and
// returns the reference the last one got. An object refers only to ones added
// before it, since a reference exists only once its object is added.
func (c *capture) replay(doc Allocator) object.IndirectRef {
	refs := c.replayAll(doc)
	if len(refs) == 0 {
		return object.IndirectRef{}
	}
	return refs[len(refs)-1]
}

// replayAll is replay, returning the reference each object got, in order.
func (c *capture) replayAll(doc Allocator) []object.IndirectRef {
	refs := make([]object.IndirectRef, 0, len(c.objs))
	for _, o := range c.objs {
		refs = append(refs, doc.Add(rebind(o, refs)))
	}
	return refs
}

// rebind is o with the capture's own references replaced by refs, the
// references the captured objects got in a document. Streams and dictionaries
// are copied, never changed: the cached objects are replayed again.
func rebind(o object.Object, refs []object.IndirectRef) object.Object {
	switch v := o.(type) {
	case object.IndirectRef:
		if n := -v.Number; n >= 1 && n <= len(refs) {
			return refs[n-1]
		}
		return v
	case *object.Stream:
		s := &object.Stream{Data: v.Data}
		s.Dict = *rebindDict(&v.Dict, refs)
		return s
	case *object.Dictionary:
		return rebindDict(v, refs)
	case object.Array:
		out := make(object.Array, len(v))
		for i, e := range v {
			out[i] = rebind(e, refs)
		}
		return out
	}
	return o
}

func rebindDict(d *object.Dictionary, refs []object.IndirectRef) *object.Dictionary {
	out := &object.Dictionary{}
	for k, v := range d.All() {
		out.Set(k, rebind(v, refs))
	}
	return out
}

// errNoType3Vertical is a vertical form asked of a bitmap face. A Type 3 font
// is a simple font, and writing mode belongs to a CIDFont's CMap; DrawUpright
// places its glyphs one by one, as it does a simple face's.
var errNoType3Vertical = errors.New("fonts: a face whose glyphs are only bitmaps is written as Type 3 fonts, " +
	"which have no vertical form; DrawUpright places its glyphs one by one")

// embedType3 writes every sub-font the face has drawn with: the home font as
// Embedded.Horizontal and the others in Embedded.Subfonts, in the order
// SubfontName numbers them.
func (f *Face) embedType3(doc Allocator, forms Forms) (Embedded, error) {
	if forms.Vertical {
		return Embedded{}, errNoType3Vertical
	}
	if err := type3Allowed(f.EmbeddingPermissions()); err != nil {
		return Embedded{}, err
	}
	if f.t3 == nil || len(f.t3.subs) == 0 {
		return Embedded{}, errEmbedBeforeUse
	}
	var out Embedded
	for _, sub := range f.t3.subs {
		ref, err := f.embedSubfont(doc, sub, forms.Opaque)
		if err != nil {
			return Embedded{}, err
		}
		if sub.index == 0 {
			out.Horizontal = ref
		} else {
			out.Subfonts = append(out.Subfonts, ref)
		}
	}
	return out, nil
}

// embedSubfont writes one sub-font: its glyph procedures and their images,
// the ToUnicode CMap, a descriptor and the font dictionary, which is the
// reference returned. opaque writes it without transparency (Forms.Opaque).
func (f *Face) embedSubfont(doc Allocator, sub *subFont, opaque bool) (object.IndirectRef, error) {
	t := f.t3
	toGlyph := 1000 / float64(f.UnitsPerEm())
	charProcs := &object.Dictionary{}
	xobjects := &object.Dictionary{}
	extGStates := &object.Dictionary{}
	shadings := &object.Dictionary{}
	differences := object.Array{object.Integer(0)}
	widths := make(object.Array, 0, len(sub.gids))
	bbox := [4]float64{}
	haveBox := false
	for code, gid := range sub.gids {
		var g bitmapGlyph
		if !f.vectorGlyph(gid) {
			var err error
			if g, err = t.glyphAt(f, gid, sub.key.strike); err != nil {
				return object.IndirectRef{}, err
			}
		}
		wx := f.GlyphAdvance(gid)
		name := object.Name("g" + strconv.Itoa(gid))
		differences = append(differences, name)
		widths = append(widths, widthNumber(wx))

		var proc []byte
		if f.vectorGlyph(gid) {
			pr, inked, err := t.vectorProc(f, doc, sub.key, gid, wx, opaque, map[string]*object.Dictionary{
				"XObject": xobjects, "ExtGState": extGStates, "Shading": shadings,
			})
			if err != nil {
				return object.IndirectRef{}, err
			}
			proc = pr
			if !inked.empty() {
				box := [4]float64{inked.x0 * toGlyph, inked.y0 * toGlyph, inked.x1 * toGlyph, inked.y1 * toGlyph}
				if !haveBox {
					bbox, haveBox = box, true
				} else {
					bbox = [4]float64{
						math.Min(bbox[0], box[0]), math.Min(bbox[1], box[1]),
						math.Max(bbox[2], box[2]), math.Max(bbox[3], box[3]),
					}
				}
			}
		} else if g.kind == glyphEmpty {
			proc = appendNums(nil, wx, 0, 0, 0, 0, 0)
			proc = append(proc, "d1\n"...)
		} else {
			box := [4]float64{
				g.img.Box.XMin * toGlyph, g.img.Box.YMin * toGlyph,
				g.img.Box.XMax * toGlyph, g.img.Box.YMax * toGlyph,
			}
			if !haveBox {
				bbox, haveBox = box, true
			} else {
				bbox = [4]float64{
					math.Min(bbox[0], box[0]), math.Min(bbox[1], box[1]),
					math.Max(bbox[2], box[2]), math.Max(bbox[3], box[3]),
				}
			}
			// A greyscale glyph is a stencil where it has no colour of its
			// own to carry, and where transparency is not allowed: its
			// sub-font is only ever shown in the colour it is keyed by,
			// which is the colour a stencil paints in.
			stencil := g.kind == glyphMask || (g.kind == glyphGrey && (sub.key.colour == "" || opaque))
			c, err := t.imageFor(f, sub.key, gid, g, stencil, opaque)
			if err != nil {
				return object.IndirectRef{}, fmt.Errorf("fonts: glyph %d: %w", gid, err)
			}
			imName := object.Name("I" + strconv.Itoa(code))
			xobjects.Set(imName, c.replay(doc))
			if stencil {
				proc = appendNums(nil, wx, 0, box[0], box[1], box[2], box[3])
				proc = append(proc, "d1\n"...)
			} else {
				proc = appendNums(nil, wx, 0)
				proc = append(proc, "d0\n"...)
			}
			proc = append(proc, "q\n"...)
			proc = appendNums(proc, box[2]-box[0], 0, 0, box[3]-box[1], box[0], box[1])
			proc = append(proc, "cm\n/"...)
			proc = append(proc, imName...)
			proc = append(proc, " Do\nQ\n"...)
		}
		charProcs.Set(name, doc.Add(flateStream(proc)))
	}

	entries := make([]toUnicodeEntry, 0, len(sub.gids))
	rec := f.record()
	for code, gid := range sub.gids {
		if gid == 0 {
			continue // .notdef stands for nothing
		}
		var runes []rune
		if s, ok := rec.byGID[gid]; ok {
			runes = []rune(s)
		} else if r, ok := f.canonical(gid); ok {
			runes = []rune{r}
		}
		if len(runes) > 0 {
			entries = append(entries, toUnicodeEntry{code: code, runes: runes})
		}
	}
	toUnicodeRef := doc.Add(flateStream(buildToUnicodeCMap(entries, "<00> <FF>")))

	box := object.Array{realNumber(bbox[0]), realNumber(bbox[1]), realNumber(bbox[2]), realNumber(bbox[3])}
	d := f.Descriptor()
	descriptor := &object.Dictionary{}
	descriptor.Set("Type", object.Name("FontDescriptor"))
	descriptor.Set("FontName", object.Name(subsetTag(sub.gids)+"+"+f.Name()))
	// Symbolic: the codes are this font's own, not characters in a standard
	// encoding.
	flags := flagSymbolic
	if d.Flags&flagFixedPitch != 0 {
		flags |= flagFixedPitch
	}
	if d.ItalicAngle != 0 {
		flags |= flagItalic
	}
	descriptor.Set("Flags", object.Integer(flags))
	descriptor.Set("FontBBox", box)
	descriptor.Set("ItalicAngle", object.Real(d.ItalicAngle))
	descriptor.Set("Ascent", object.Integer(int(f.scale(d.Ascent))))
	descriptor.Set("Descent", object.Integer(int(f.scale(d.Descent))))
	descriptorRef := doc.Add(descriptor)

	encoding := &object.Dictionary{}
	encoding.Set("Type", object.Name("Encoding"))
	encoding.Set("Differences", differences)

	fd := &object.Dictionary{}
	fd.Set("Type", object.Name("Font"))
	fd.Set("Subtype", object.Name("Type3"))
	fd.Set("FontBBox", box)
	fd.Set("FontMatrix", object.Array{object.Real(0.001), object.Integer(0), object.Integer(0),
		object.Real(0.001), object.Integer(0), object.Integer(0)})
	fd.Set("CharProcs", charProcs)
	fd.Set("Encoding", encoding)
	fd.Set("FirstChar", object.Integer(0))
	fd.Set("LastChar", object.Integer(len(sub.gids)-1))
	fd.Set("Widths", widths)
	resources := &object.Dictionary{}
	if xobjects.Len() > 0 {
		resources.Set("XObject", xobjects)
	}
	if extGStates.Len() > 0 {
		resources.Set("ExtGState", extGStates)
	}
	if shadings.Len() > 0 {
		resources.Set("Shading", shadings)
	}
	fd.Set("Resources", resources)
	fd.Set("FontDescriptor", descriptorRef)
	fd.Set("ToUnicode", toUnicodeRef)
	return doc.Add(fd), nil
}

// flagSymbolic is the FontDescriptor flag saying a font's codes are its own
// (ISO 32000-2 Table 121).
const flagSymbolic = 1 << 2

// imageFor is the image objects a glyph's procedure draws, written once per
// sub-font key, glyph and kind of writing, and kept.
func (t *type3State) imageFor(f *Face, key subKey, gid int, g bitmapGlyph, stencil, opaque bool) (*capture, error) {
	k := builtKey{key: key, gid: gid, opaque: opaque}
	if c, ok := t.built[k]; ok {
		return c, nil
	}
	c := &capture{}
	var err error
	switch {
	case g.kind == glyphColour:
		err = embedPNG(c, g.img, opaque)
	case !stencil:
		err = embedCoverage(c, t.colours[key.colour], g.img)
	default:
		// A stencil of the coverage: a pixel paints where it is at least half
		// covered, which for a 1-bit strike is exactly its own pixels.
		_, err = images.EmbedStencil(c, &image.Alpha{
			Pix: g.img.Data, Stride: g.img.Width,
			Rect: image.Rect(0, 0, g.img.Width, g.img.Height),
		})
	}
	if err != nil {
		return nil, err
	}
	t.built[k] = c
	return c, nil
}

// maxGlyphPixels bounds one bitmap glyph's image: 2048×2048. A strike is drawn
// for a size on a screen — Noto Color Emoji's is 136×128, Apple's largest sbix
// strike 160 ppem — and a font is untrusted input whose PNG header names the
// size decoding allocates, four bytes a pixel. The images package's own limit
// is for a page's pictures, and is 16 times this.
const maxGlyphPixels = 1 << 22

// embedPNG writes a colour bitmap's PNG as an image, after checking its size
// against the pixel limit before decoding it: the font is untrusted, and its
// header says how much decoding would allocate. opaque writes its alpha as a
// 1-bit /Mask, painted where it is at least half, instead of an /SMask.
func embedPNG(doc *capture, img shape.Image, opaque bool) error {
	cfg, err := png.DecodeConfig(bytes.NewReader(img.Data))
	if err != nil {
		return fmt.Errorf("its PNG cannot be read: %w", err)
	}
	if int64(cfg.Width)*int64(cfg.Height) > maxGlyphPixels {
		return fmt.Errorf("its PNG is %d×%d, past the %d pixels a glyph may have", cfg.Width, cfg.Height, maxGlyphPixels)
	}
	decoded, err := png.Decode(bytes.NewReader(img.Data))
	if err != nil {
		return fmt.Errorf("its PNG cannot be decoded: %w", err)
	}
	if !opaque {
		_, err = images.Embed(doc, decoded)
		return err
	}
	// The colour without its alpha, and the alpha as a stencil the image is
	// painted through (ISO 32000-2 8.9.6.3, explicit masking). The mask is
	// written first: an object refers only to ones written before it.
	b := decoded.Bounds()
	colour := image.NewNRGBA(b)
	alpha := image.NewAlpha(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := color.NRGBAModel.Convert(decoded.At(x, y)).(color.NRGBA)
			alpha.SetAlpha(x, y, color.Alpha{A: c.A})
			c.A = 0xFF
			colour.SetNRGBA(x, y, c)
		}
	}
	mask, err := images.EmbedStencil(doc, alpha)
	if err != nil {
		return err
	}
	ref, err := images.Embed(doc, colour)
	if err != nil {
		return err
	}
	doc.objs[-ref.Number-1].(*object.Stream).Dict.Set("Mask", mask)
	return nil
}

// embedCoverage writes a greyscale glyph in a colour: one pixel of the colour,
// in its own device space, whose /SMask is the coverage. The soft mask need not
// be the size of the image it masks (ISO 32000-2 11.6.5.3); both are mapped
// onto the same unit square.
func embedCoverage(doc Allocator, c content.Color, img shape.Image) error {
	if int64(img.Width)*int64(img.Height) > maxGlyphPixels {
		return fmt.Errorf("its coverage is %d×%d, past the %d pixels a glyph may have", img.Width, img.Height, maxGlyphPixels)
	}
	mask := imageXObject(img.Data, img.Width, img.Height, "DeviceGray")
	maskRef := doc.Add(mask)
	samples := make([]byte, len(c.Components))
	for i, v := range c.Components {
		samples[i] = byte(math.Round(v * 255))
	}
	base := imageXObject(samples, 1, 1, c.Space)
	base.Dict.Set("SMask", maskRef)
	doc.Add(base)
	return nil
}

// imageXObject is an 8-bit image over raw samples, Flate-compressed.
func imageXObject(samples []byte, w, h int, space object.Name) *object.Stream {
	s := flateStream(samples)
	s.Dict.Set("Type", object.Name("XObject"))
	s.Dict.Set("Subtype", object.Name("Image"))
	s.Dict.Set("Width", object.Integer(w))
	s.Dict.Set("Height", object.Integer(h))
	s.Dict.Set("ColorSpace", space)
	s.Dict.Set("BitsPerComponent", object.Integer(8))
	return s
}

// appendNums appends numbers as a content stream writes them, each followed by
// a space.
func appendNums(dst []byte, vs ...float64) []byte {
	for _, v := range vs {
		dst = strconv.AppendFloat(dst, cleanZero(v), 'f', -1, 64)
		dst = append(dst, ' ')
	}
	return dst
}

// cleanZero is v with negative zero made zero, which -0 would otherwise be
// written as.
func cleanZero(v float64) float64 {
	if v == 0 {
		return 0
	}
	return v
}

// realNumber writes a number as an integer when it is one.
func realNumber(v float64) object.Object { return widthNumber(cleanZero(v)) }

// errNoOpaqueColour is a colour glyph with no outline of its own, in a
// document that may not use transparency: there is nothing it can be drawn as
// there but its painting, which is.
var errNoOpaqueColour = errors.New("fonts: the colour glyph has no outline to draw in a document " +
	"that may not use transparency (PDF/A-1), and its painting uses it")

// vectorProc is a vector glyph's procedure (colr.go), with the resources it
// names added to res, and the box it inks in font units.
//
// Painted, it is d0 when it sets a colour or paints anything but the text
// colour, and d1 otherwise, a shape a reader may cache. opaque draws it as
// its outline in the text colour, as it was drawn before colour glyphs were.
func (t *type3State) vectorProc(f *Face, doc Allocator, key subKey, gid int, wx float64, opaque bool,
	res map[string]*object.Dictionary) ([]byte, box, error) {
	scale := 1000 / float64(f.UnitsPerEm())
	wrap := func(head []byte, body []byte) []byte {
		head = append(head, "q\n"...)
		head = appendNums(head, scale, 0, 0, scale, 0, 0)
		head = append(head, "cm\n"...)
		head = append(head, body...)
		return append(head, "Q\n"...)
	}
	d1 := func(b box) []byte {
		if b.empty() {
			return append(appendNums(nil, wx, 0, 0, 0, 0, 0), "d1\n"...)
		}
		return append(appendNums(nil, wx, 0, b.x0*scale, b.y0*scale, b.x1*scale, b.y1*scale), "d1\n"...)
	}
	if opaque {
		p := newColrPainter(f, &capture{}, gid, nil, box{})
		path, bb, err := p.outline(gid)
		if err != nil {
			return nil, box{}, err
		}
		if len(path) == 0 {
			if k := f.GlyphColour(gid, 0); k == shape.ColourLayers || k == shape.ColourPaint {
				return nil, box{}, fmt.Errorf("glyph %d: %w", gid, errNoOpaqueColour)
			}
			return d1(box{}), box{}, nil
		}
		return wrap(d1(bb), append(path, "f\n"...)), bb, nil
	}
	k := builtKey{key: key, gid: gid}
	art, ok := t.arts[k]
	if !ok {
		var fg *content.Color
		if key.colour != "" {
			c := t.colours[key.colour]
			fg = &c
		}
		var err error
		if art, err = f.paintColour(gid, fg, f.paintRoot()); err != nil {
			return nil, box{}, err
		}
		t.arts[k] = art
	}
	refs := art.cap.replayAll(doc)
	for _, r := range art.res {
		d := res[r.kind]
		if d == nil {
			return nil, box{}, fmt.Errorf("fonts: glyph %d names a %s resource", gid, r.kind)
		}
		d.Set(r.name, refs[art.refs[r]-1])
	}
	if art.coloured {
		return wrap(append(appendNums(nil, wx, 0), "d0\n"...), art.content), art.inked, nil
	}
	return wrap(d1(art.inked), art.content), art.inked, nil
}

// paintRoot is the box a colour glyph is painted within when nothing clips
// it: the face's own box with an em about it, in font units.
func (f *Face) paintRoot() box {
	d := f.Descriptor()
	em := float64(f.UnitsPerEm())
	return box{float64(d.BBox[0]) - em, float64(d.BBox[1]) - em, float64(d.BBox[2]) + em, float64(d.BBox[3]) + em}
}
