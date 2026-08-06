package render

import (
	"strings"

	"github.com/mgilbir/pdf0/css"
	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/style"
)

// The display list: the sixth of §3's stages, and the one that has no PDF in it
// at all.
//
// Keeping this apart from the stage that writes a content stream is what makes
// the whole testing story of §7 possible. The display list is where a rasterizer
// attaches, and it separates "did we lay this out correctly" from "did we emit
// correct PDF" — two failure modes that are miserable to debug together, because
// each can produce a page that looks exactly like the other's symptom.
//
// It is also why the coordinates here are still CSS's: origin at the top left,
// y increasing downwards, lengths in layout units. The flip to PDF's bottom-left
// origin and the conversion to points happen once, in pdfout, and a coordinate
// system that changed halfway through would make every sign error plausible.

// Op is one primitive of the display list.
//
// The set is deliberately small. Anything a backend cannot draw directly is
// something this stage should have decomposed — a border is four filled bands
// rather than a "border" primitive, because a backend that had to understand
// border-collapse would be a second layout engine.
type Op interface{ isOp() }

// FillRect paints a rectangle in a solid colour.
type FillRect struct {
	Rect  Rect
	Color style.RGBA
}

// DrawText draws a run of text with the origin of its baseline at At.
//
// The position is the baseline rather than the top of the line box, because that
// is what a text-drawing backend takes and because converting between them needs
// the face's metrics — which this stage has and the backend may not.
type DrawText struct {
	At    Point
	Text  string
	Face  *fonts.Face
	Size  style.Unit
	Color style.RGBA
}

func (FillRect) isOp() {}
func (DrawText) isOp() {}

// Paint turns a fragment tree into a display list, in painting order.
//
// The order is CSS 2.1 Appendix E reduced to what this engine produces: for each
// box, its background, then its borders, then everything inside it. Without
// out-of-flow content there are no stacking contexts to interleave, so tree
// order is painting order — which will stop being true the moment z-index or
// positioning arrives, and is the reason this is a separate traversal rather
// than something layout emits as it goes.
func Paint(root *Fragment) []Op {
	if root == nil {
		return nil
	}
	p := &painter{colors: map[string]style.RGBA{}}
	p.fragment(root)
	return p.ops
}

type painter struct {
	ops []Op
	// colors memoizes parsing a computed colour, which is asked for once per
	// box per property and is almost always one of a handful of values.
	colors map[string]style.RGBA
}

func (p *painter) fragment(f *Fragment) {
	if f.Box == nil {
		return
	}

	// The background paints over the padding box — under the border, not under
	// the margin. A background that covered the margin would bleed into the gap
	// between two boxes, which is the space that is meant to show the page.
	if bg, ok := p.color(f.Box, "background-color"); ok && bg.A > 0 {
		p.ops = append(p.ops, FillRect{Rect: f.PaddingRect(), Color: bg})
	}

	p.borders(f)

	for _, child := range f.Children {
		p.fragment(child)
	}
	p.lines(f)
}

// borders paints the four edges as filled bands.
//
// Four rectangles rather than a stroked outline, because a stroke is centred on
// its path and a CSS border is not: it lies entirely inside the border box. A
// stroked border would be half a width out on every side, which is invisible at
// one pixel and obvious at ten.
//
// The corners are mitred by giving the top and bottom bands the full width and
// the sides only what is left, which is right for a solid border of one colour
// and is where a proper implementation of border-style would start.
func (p *painter) borders(f *Fragment) {
	r := f.BorderRect
	e := f.Border

	if e.Top > 0 {
		if c, ok := p.color(f.Box, "border-top-color"); ok && c.A > 0 {
			p.ops = append(p.ops, FillRect{
				Rect:  Rect{X: r.X, Y: r.Y, W: r.W, H: e.Top},
				Color: c,
			})
		}
	}
	if e.Bottom > 0 {
		if c, ok := p.color(f.Box, "border-bottom-color"); ok && c.A > 0 {
			p.ops = append(p.ops, FillRect{
				Rect:  Rect{X: r.X, Y: r.Bottom().Sub(e.Bottom), W: r.W, H: e.Bottom},
				Color: c,
			})
		}
	}
	inner := Rect{
		Y: r.Y.Add(e.Top),
		H: r.H.Sub(e.Top).Sub(e.Bottom),
	}
	if inner.H < 0 {
		inner.H = 0
	}
	if e.Left > 0 {
		if c, ok := p.color(f.Box, "border-left-color"); ok && c.A > 0 {
			p.ops = append(p.ops, FillRect{
				Rect:  Rect{X: r.X, Y: inner.Y, W: e.Left, H: inner.H},
				Color: c,
			})
		}
	}
	if e.Right > 0 {
		if c, ok := p.color(f.Box, "border-right-color"); ok && c.A > 0 {
			p.ops = append(p.ops, FillRect{
				Rect:  Rect{X: r.Right().Sub(e.Right), Y: inner.Y, W: e.Right, H: inner.H},
				Color: c,
			})
		}
	}
}

// lines paints the text of a block container.
func (p *painter) lines(f *Fragment) {
	if len(f.Lines) == 0 {
		return
	}
	content := f.ContentRect()
	for _, line := range f.Lines {
		baseline := content.Y.Add(line.Rect.Y).Add(line.Baseline)
		for _, run := range line.Runs {
			// Spaces are drawn, not skipped, and the reason is text extraction
			// rather than ink. A space glyph marks no paper, so skipping it
			// looks like a free optimisation — but then the words either side
			// are separate text operations with only a position jump between
			// them, and a reader copying the text gets them run together. That
			// was found by reading back a rendered page: "A heading" came out
			// as "Aheading".
			colour, ok := p.color(run.Box, "color")
			if !ok {
				colour = style.RGBA{A: 1}
			}
			p.ops = append(p.ops, DrawText{
				At:    Point{X: content.X.Add(line.Rect.X).Add(run.X), Y: baseline},
				Text:  run.Text,
				Face:  run.Face,
				Size:  run.Size,
				Color: colour,
			})
		}
	}
}

// color resolves a computed colour property.
//
// "currentcolor" is the one value that is not a colour but a reference to one —
// it means whatever "color" resolved to, which is what makes a border take the
// text's colour without the author repeating it.
func (p *painter) color(b *Box, property string) (style.RGBA, bool) {
	if b == nil {
		return style.RGBA{}, false
	}
	raw := strings.TrimSpace(b.Style[property])
	if raw == "" {
		return style.RGBA{}, false
	}
	if strings.EqualFold(raw, "currentcolor") {
		if property == "color" {
			// A "color: currentcolor" is circular; the initial value breaks it,
			// which is what the specification says to do.
			return style.RGBA{A: 1}, true
		}
		return p.color(b, "color")
	}
	if got, ok := p.colors[raw]; ok {
		return got, got.A >= 0
	}
	vals, _ := css.ParseComponentValues(raw)
	c, ok := style.ParseColor(vals)
	if !ok {
		return style.RGBA{}, false
	}
	p.colors[raw] = c
	return c, true
}
