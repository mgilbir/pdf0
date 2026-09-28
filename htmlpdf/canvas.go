package htmlpdf

import (
	"fmt"
	"image"

	"github.com/mgilbir/forme/layout"
	"github.com/mgilbir/forme/shape"
	pdf0 "github.com/mgilbir/pdf0"
	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/images"
	"github.com/mgilbir/pdf0/object"
)

// writePage turns a display list into a one-page document.
//
// The document is made before the content stream rather than after, which is
// the one ordering constraint here: an image has to be written into the file as
// an object before the drawing can name it, and an object cannot be added to a
// document that does not exist yet. Fonts are the other way round — a face is
// subsetted to the glyphs it was asked to set, so it can only be embedded once
// the drawing is finished — which is why AddPage and AddForm take the faces and
// this passes the images.
//
// Every field of every operation is either drawn here or refused by
// checkDrawable before this runs; drawnFields lists which, and a test holds
// that list against the operations layout declares.
func writePage(ops []layout.Op, page layout.PageSize, scale float64) (*pdf0.Document, error) {
	// The one transform. Reading it right to left: layout units become points,
	// the y axis is inverted, the whole thing is scaled to fit, and the result
	// is placed inside the page's margin.
	//
	// A matrix [a b c d e f] maps (x, y) to (ax + cy + e, bx + dy + f). With
	// a = k and d = -k the x axis keeps its direction and the y axis reverses,
	// which is exactly the difference between the two coordinate systems.
	const pxToPt = 72.0 / 96.0
	k := pxToPt * scale
	tx := page.Margin.Left.Pt()
	ty := page.Height.Pt() - page.Margin.Top.Pt()
	w := &pageWriter{
		doc:    pdf0.NewDocument(),
		toPage: pageTransform{k: k, tx: tx, ty: ty},
		faces:  map[*shape.Face]*fonts.Face{},
		images: map[string]object.Object{},
	}
	// The sheet in layout's coordinates, which a form XObject drawn in them
	// takes as its bounding box: nothing drawn off the sheet is seen.
	w.sheet = [4]float64{-tx / k, (ty - page.Height.Pt()) / k, (page.Width.Pt() - tx) / k, ty / k}
	c := w.newCanvas([6]float64{k, 0, 0, -k, tx, ty})
	c.b.Save()
	c.b.Concat(k, 0, 0, -k, tx, ty)
	if err := c.drawOps(ops); err != nil {
		return nil, err
	}
	c.b.Restore()

	if _, err := w.doc.AddPage(pdf0.Page{
		Width:       page.Width.Pt(),
		Height:      page.Height.Pt(),
		Content:     c.b,
		Links:       w.links,
		Faces:       c.fonts,
		XObjects:    c.xobjects,
		Patterns:    c.patterns,
		Shadings:    c.shadings,
		ExtGStates:  c.states,
		ColorSpaces: nil,
		// A translucent mark composites against whatever is behind it, and
		// without a page group what that is is left to the reader.
		Group: w.transparent,
	}); err != nil {
		return nil, err
	}
	return w.doc, nil
}

// pageWriter is what the content streams of one page share: the document, one
// embedding per face and one XObject per image however many streams draw them,
// and the page's links.
type pageWriter struct {
	doc *pdf0.Document
	// toPage is the page transform, for what is written in the page's own
	// coordinates rather than drawn: a link annotation's /Rect.
	toPage pageTransform
	// sheet is the page in layout's coordinates, as [xMin yMin xMax yMax].
	sheet [4]float64
	// faces is the embedding wrapper of each shaping face the display list
	// names. Adopt does not copy: the two are the same font, and each records
	// the glyphs the other used, which is what makes the subset come out
	// right. One wrapper per face, whichever stream draws it, so that the face
	// is one font in the file.
	faces map[*shape.Face]*fonts.Face
	// images is the image XObject of each source, keyed by the source bytes
	// rather than by the decoded image, so a logo drawn on every row of a
	// table is one image XObject in the file.
	images map[string]object.Object
	links  []pdf0.Link
	// transparent is any stream of the page selecting transparency: an
	// alpha, a soft mask or a transparency group.
	transparent bool
}

// canvas is one content stream being drawn: the page's, or a form XObject's,
// and the resources it names.
//
// Every stream draws in layout's coordinates. The page's sets them up with its
// one "cm"; a form is painted where that transform is in force and has the
// identity matrix, so its own space is layout's too, and an operation draws the
// same numbers wherever it is.
type canvas struct {
	w *pageWriter
	b *content.Builder
	// base maps layout's coordinates into this stream's default coordinate
	// space: the page transform for the page, the identity for a form. A
	// pattern's /Matrix is stated against it (see tiling).
	base [6]float64

	fonts     map[object.Name]*fonts.Face
	fontNames map[*fonts.Face]object.Name
	xobjects  map[object.Name]object.Object
	imgNames  map[string]object.Name
	patterns  map[object.Name]object.Object
	shadings  map[object.Name]object.Object
	states    map[object.Name]object.Object
	alphas    *alphaStates
}

func (w *pageWriter) newCanvas(base [6]float64) *canvas {
	c := &canvas{
		w:         w,
		b:         &content.Builder{},
		base:      base,
		fonts:     map[object.Name]*fonts.Face{},
		fontNames: map[*fonts.Face]object.Name{},
		xobjects:  map[object.Name]object.Object{},
		imgNames:  map[string]object.Name{},
		patterns:  map[object.Name]object.Object{},
		shadings:  map[object.Name]object.Object{},
	}
	c.alphas = newAlphaStates()
	c.states = c.alphas.states
	c.alphas.onUse = func() { w.transparent = true }
	return c
}

// face is the name this stream shows a face under, adopting it on first use.
// vertical asks for its vertical form, which a face without one does not have;
// the horizontal form is returned then.
func (c *canvas) face(f *shape.Face, vertical bool) (object.Name, *fonts.Face) {
	face, ok := c.w.faces[f]
	if !ok {
		face = fonts.Adopt(f)
		c.w.faces[f] = face
	}
	prefix := "F"
	if vertical {
		// The horizontal form is named too, as it always has been: the two
		// are one embedding over one descendant (see fonts.Face.EmbedForms),
		// and a page that sets a face upright usually sets it across as well.
		c.face(f, false)
		// Written in the face's vertical form, an Identity-V font whose /W2
		// states each glyph's vertical metrics, where the face has one. A
		// standard face has none, and DrawUpright places its glyphs one by
		// one in the horizontal font.
		if vf, err := face.Vertical(); err == nil {
			face, prefix = vf, "V"
		}
	}
	name, ok := c.fontNames[face]
	if !ok {
		n := 0
		for g := range c.fontNames {
			if g.IsVertical() == face.IsVertical() {
				n++
			}
		}
		name = object.Name(fmt.Sprintf("%s%d", prefix, n+1))
		c.fontNames[face] = name
		c.fonts[name] = face
	}
	return name, face
}

// image is the name this stream shows a picture under, putting it in the file
// the first time the page draws it.
func (c *canvas) image(img image.Image, key string) (object.Name, error) {
	if name, ok := c.imgNames[key]; ok {
		return name, nil
	}
	ref, ok := c.w.images[key]
	if !ok {
		// images.Embed is the module's own encoder, and using it rather than
		// writing a second one is what keeps the two directions checking each
		// other: whatever it writes, the extraction side of that package reads
		// back, and the pixels have to survive the trip. It also brings its
		// own pixel cap.
		var err error
		if ref, err = images.Embed(c.w.doc, img); err != nil {
			return "", fmt.Errorf("embedding an image: %w", err)
		}
		c.w.images[key] = ref
	}
	name := object.Name(fmt.Sprintf("Im%d", len(c.imgNames)+1))
	c.imgNames[key] = name
	c.xobjects[name] = ref
	return name, nil
}

// drawOps draws operations in order.
func (c *canvas) drawOps(ops []layout.Op) error {
	for _, op := range ops {
		if err := c.drawOp(op); err != nil {
			return err
		}
	}
	return nil
}

func (c *canvas) drawOp(op layout.Op) error {
	b := c.b
	switch v := op.(type) {
	case layout.FillRect:
		// Overhang is about the page-overflow guard in layout and says
		// nothing about how the rectangle is painted.
		if v.Rect.Empty() || !(v.Color.A > 0) {
			return nil // no area, or no ink: nothing is painted
		}
		b.Save()
		c.alphas.use(b, v.Color.A)
		b.SetRGB(v.Color.R/255, v.Color.G/255, v.Color.B/255)
		// The rectangle is given in layout units and the transform above
		// converts them, so the numbers written here are layout's own.
		b.Rect(v.Rect.X.Px(), v.Rect.Y.Px(), v.Rect.W.Px(), v.Rect.H.Px())
		b.Fill()
		b.Restore()

	case layout.DrawText:
		if v.Face == nil || v.Text == "" {
			return nil
		}
		if undrawable(v) != "" {
			return nil // checkDrawable reported it, and the policy let the page through without it
		}
		c.text(v)

	case layout.DrawImage:
		if v.Image == nil || v.Rect.Empty() {
			return nil
		}
		name, err := c.image(v.Image, v.Key)
		if err != nil {
			return err
		}
		b.Save()
		clipTo(b, v.Clip)
		// An image XObject is painted into the unit square, so the matrix
		// *is* the placement. The negative vertical scale is not a flip: in
		// these coordinates y increases downwards, so the image's own
		// bottom edge — the one at v=0 — belongs at the rectangle's largest
		// y. Getting the sign wrong here draws the picture upside down
		// above the box rather than the right way up inside it.
		b.Concat(v.Rect.W.Px(), 0, 0, -v.Rect.H.Px(),
			v.Rect.X.Px(), v.Rect.Bottom().Px())
		b.Draw(name)
		b.Restore()

	case layout.TileImage:
		if v.Image == nil || v.Clip.Empty() || v.Tile.Empty() {
			return nil
		}
		name, err := c.image(v.Image, v.Key)
		if err != nil {
			return err
		}
		cols, rows := v.Tiles()
		if cols <= 0 || rows <= 0 {
			return nil
		}
		b.Save()
		b.Rect(v.Clip.X.Px(), v.Clip.Y.Px(), v.Clip.W.Px(), v.Clip.H.Px())
		b.Clip()
		b.EndPath()
		if cols == 1 && rows == 1 {
			// One tile, which is what "no-repeat" produces and what most
			// backgrounds are. A pattern for it would be a dictionary, a
			// stream and a resource to say what two operators already say.
			b.Concat(v.Tile.W.Px(), 0, 0, -v.Tile.H.Px(),
				v.Tile.X.Px(), v.Tile.Bottom().Px())
			b.Draw(name)
		} else {
			// A real tiling, drawn as PDF's own: one pattern object with a
			// step, painted over the clip in a single fill. The alternative
			// — a Do per tile — would put the tile count into the file, and
			// the tile count is the number this engine refuses to let a
			// stylesheet choose.
			pattern, err := tilingPattern(c.w.doc, name, c.xobjects[name], v, c.base)
			if err != nil {
				return err
			}
			pname := object.Name(fmt.Sprintf("Pt%d", len(c.patterns)+1))
			c.patterns[pname] = pattern
			b.SetColorSpace("Pattern")
			b.SetPattern(pname)
			b.Rect(v.Clip.X.Px(), v.Clip.Y.Px(), v.Clip.W.Px(), v.Clip.H.Px())
			b.Fill()
		}
		b.Restore()

	case layout.Link:
		if _, err := linkTarget(v.Href); err != nil {
			return nil // checkDrawable reported it, and the policy let the page through without it
		}
		// One annotation per area, in the order forme painted them, so
		// that where areas overlap the later — the inner of two nested
		// links — is on top, as forme's Link says a backend should make
		// it. Not one annotation with /QuadPoints: a reader that does
		// not read them (PDF 1.6, and optional) activates the /Rect,
		// which for a link broken across lines is a box over the middle
		// of every line between. The href goes to the builder as the
		// document wrote it; the builder normalises and encodes it.
		//
		// Every stream draws in layout's coordinates (see canvas), so an
		// area inside a group is placed by the page transform as one
		// outside it is.
		for _, r := range v.Rects {
			if r.Empty() {
				continue // forme drops these; nothing could activate one
			}
			c.w.links = append(c.w.links, pdf0.Link{Rect: c.w.toPage.rect(r), URI: v.Href})
		}

	default:
		// checkDrawable refused this document or the caller's policy let
		// it through knowing the operation is not drawn.
	}
	return nil
}

// text draws a run of text.
func (c *canvas) text(v layout.DrawText) {
	b := c.b
	name, face := c.face(v.Face, v.Upright)
	b.Save()
	clipTo(b, v.Clip)
	c.alphas.use(b, v.Color.A)
	b.SetRGB(v.Color.R/255, v.Color.G/255, v.Color.B/255)
	b.BeginText()
	b.SetFont(name, v.Size.Px())
	if !(v.Color.A > 0) {
		// Transparent text is not painted and is still text: CSS
		// "color: transparent" leaves it selectable, and so does this.
		b.SetTextRenderMode(content.InvisibleText)
	}
	// The text matrix puts the run's own axes — along the line, and up
	// the glyph — onto the page, in the flipped system the transform
	// above set up. See textMatrix.
	a, bb, cc, d := textMatrix(v)
	b.SetTextMatrix(a, bb, cc, d, v.At.X.Px(), v.At.Y.Px())
	if v.WidthScale > 0 {
		// A run squeezed across the direction it advances in, about At: a
		// text-combine-upright composition wider than its em (CSS Writing
		// Modes 9.1.3). Tz is a percentage and scales the glyphs and every
		// displacement along the run (ISO 32000-2 9.3.4), which is what
		// forme squeezes; see squeezeUndrawable for the run it cannot.
		b.SetHorizontalScale(100 * v.WidthScale)
	}
	// The glyphs layout measured, shaped with the run's direction, its
	// context either side and the features the document turned off —
	// layout.ShapedGlyphs is the pairing of all of them, and a backend
	// that reshapes the text alone draws a different run from the one
	// placed (a ligature measured as two letters, a kern turned back on,
	// an Arabic word in isolated forms).
	if v.Upright {
		// Upright: each glyph hung from its vertical origin, one
		// below the other. See uprightGlyphs.
		face.DrawUpright(b, v.Text, uprightGlyphs(v), v.Size.Px())
	} else {
		text := layout.ShapedText(v)
		glyphs, _ := layout.ShapedGlyphs(v)
		glyphs = withLetterSpacing(glyphs, text, v)
		face.Draw(b, text, glyphs, v.Size.Px())
	}
	b.EndText()
	b.Restore()
}
