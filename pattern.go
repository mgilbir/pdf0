package pdf0

import (
	"fmt"
	"math"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// Tiling patterns: a drawing repeated across an area (ISO 32000-2 8.7.3).
//
// This is what a repeating background is. A page can of course paint the same
// image a hundred times, and for a background that must fill an arbitrary shape
// that is the wrong answer twice over: the drawing's bytes are repeated once per
// tile, and the caller has to work out how many tiles the shape needs and clip
// the overhang. A pattern states the cell once and lets the reader repeat it,
// which is both smaller and correct at the edges.
//
// # Coloured and uncoloured
//
// A coloured pattern carries its own colours. An uncoloured one carries only
// shape, and takes its colour from wherever it is painted — the same cell in
// black on one element and red on another, stated once. The distinction is not
// a hint: the contents of an uncoloured pattern's cell *may not* set a colour,
// and a file that does so is undefined rather than wrong, which means it looks
// different in different readers. AddTilingPattern refuses it.

// TilingPattern is a drawing that repeats to fill whatever is painted with it.
type TilingPattern struct {
	// BBox is the cell in pattern space, as [xMin, yMin, xMax, yMax]. Marks
	// outside it are clipped, so a box smaller than the drawing is a silent
	// crop.
	BBox [4]float64

	// XStep and YStep are the distance between the origins of neighbouring
	// cells. Zero means the width and height of BBox, which is the usual case:
	// tiles that abut.
	//
	// They are deliberately separate from BBox rather than derived from it. A
	// step larger than the box leaves gaps between tiles; a step smaller makes
	// them overlap; a brick pattern is a box and a step that disagree. Tying
	// them together would remove the only way to express any of that.
	XStep, YStep float64

	// Matrix maps pattern space into the default space of the page, as the six
	// numbers of a PDF matrix. Nil means the identity.
	//
	// This is where a pattern is positioned and scaled, and it is anchored to
	// the *page*, not to the shape being filled: two shapes filled with one
	// pattern show a continuous tiling across both, which is what a repeating
	// background of a page means and is occasionally a surprise.
	Matrix *[6]float64

	// Content is the drawing of one cell.
	Content *content.Builder

	// Uncolored makes the pattern carry shape without colour, taking its colour
	// from wherever it is painted. Its Content may then not set any colour.
	Uncolored bool

	// Spacing chooses how a reader may adjust the step to the device's pixel
	// grid. The zero value is the usual one; see the TilingSpacing constants.
	Spacing TilingSpacing

	// Faces are fonts to embed and name, as for Page.
	Faces map[object.Name]*fonts.Face

	// The resources the drawing named, by the name it used.
	Fonts       map[object.Name]object.Object
	XObjects    map[object.Name]object.Object
	ExtGStates  map[object.Name]object.Object
	ColorSpaces map[object.Name]object.Object
	Shadings    map[object.Name]object.Object
	Patterns    map[object.Name]object.Object
	Properties  map[object.Name]object.Object
}

// TilingSpacing is how a reader may round a pattern's step to whole pixels
// (ISO 32000-2 Table 75, /TilingType).
type TilingSpacing int

const (
	// ConstantSpacing keeps the distance between tiles uniform by distorting
	// each cell slightly to fit the pixel grid. It is the right default: an
	// uneven gap between tiles is far more visible than a cell a fraction of a
	// pixel out of shape.
	ConstantSpacing TilingSpacing = iota
	// NoDistortion draws every cell identically and lets the spacing between
	// them vary by up to a pixel. Use it when the cell is a glyph or a logo
	// whose shape must not be touched.
	NoDistortion
	// FasterConstantSpacing is ConstantSpacing with the cell allowed to be
	// distorted more, which some readers draw faster.
	FasterConstantSpacing
)

// AddTilingPattern writes a tiling pattern into the document and returns the
// reference to put in a page's /Resources /Pattern.
//
// As with AddPage, a name the drawing used and no resource map defines is an
// error rather than a pattern that paints with something missing.
func (d *Document) AddTilingPattern(p TilingPattern) (object.IndirectRef, error) {
	if p.Content == nil {
		return object.IndirectRef{}, fmt.Errorf("pdf0: the pattern has no content")
	}
	drawn, err := p.Content.Bytes()
	if err != nil {
		return object.IndirectRef{}, err
	}
	if p.BBox[2] <= p.BBox[0] || p.BBox[3] <= p.BBox[1] {
		return object.IndirectRef{}, fmt.Errorf(
			"pdf0: the pattern's cell %v has no area; every tile would be clipped away", p.BBox)
	}
	for i, v := range p.BBox {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return object.IndirectRef{}, fmt.Errorf("pdf0: the pattern's cell has a non-finite bound at %d: %v", i, v)
		}
	}

	// A step of zero means "tiles that abut", which is the common case and the
	// only sensible reading: a literal zero step would place every tile on top
	// of the last, and a reader asked to fill an area that way does not
	// terminate.
	xStep, yStep := p.XStep, p.YStep
	if xStep == 0 {
		xStep = p.BBox[2] - p.BBox[0]
	}
	if yStep == 0 {
		yStep = p.BBox[3] - p.BBox[1]
	}
	if err := checkStep("XStep", xStep); err != nil {
		return object.IndirectRef{}, err
	}
	if err := checkStep("YStep", yStep); err != nil {
		return object.IndirectRef{}, err
	}

	if p.Uncolored && p.Content.SetsColor() {
		return object.IndirectRef{}, fmt.Errorf(
			"pdf0: an uncoloured pattern takes its colour from where it is painted, " +
				"so its cell may not set one (ISO 32000-2 8.7.3.1)")
	}
	if p.Spacing < ConstantSpacing || p.Spacing > FasterConstantSpacing {
		return object.IndirectRef{}, fmt.Errorf("pdf0: unknown tiling spacing %d", p.Spacing)
	}

	embedded, err := d.embedFaces(p.Faces, p.Fonts)
	if err != nil {
		return object.IndirectRef{}, err
	}
	resources, err := Page{
		Content: p.Content, Fonts: embedded, XObjects: p.XObjects,
		ExtGStates: p.ExtGStates, ColorSpaces: p.ColorSpaces, Shadings: p.Shadings,
		Patterns: p.Patterns, Properties: p.Properties,
	}.resources()
	if err != nil {
		return object.IndirectRef{}, err
	}

	compressed := core.FlateEncode(drawn)
	pattern := &object.Stream{Dict: object.Dictionary{}, Data: compressed}
	pattern.Dict.Set("Type", object.Name("Pattern"))
	pattern.Dict.Set("PatternType", object.Integer(1)) // 1 = tiling
	paintType := 1
	if p.Uncolored {
		paintType = 2
	}
	pattern.Dict.Set("PaintType", object.Integer(paintType))
	pattern.Dict.Set("TilingType", object.Integer(int(p.Spacing)+1))
	pattern.Dict.Set("BBox", object.Array{
		numberFor(p.BBox[0]), numberFor(p.BBox[1]),
		numberFor(p.BBox[2]), numberFor(p.BBox[3]),
	})
	pattern.Dict.Set("XStep", numberFor(xStep))
	pattern.Dict.Set("YStep", numberFor(yStep))
	pattern.Dict.Set("Resources", resources)
	if p.Matrix != nil {
		m := object.Array{}
		for _, v := range p.Matrix {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return object.IndirectRef{}, fmt.Errorf("pdf0: the pattern's matrix has a non-finite entry: %v", v)
			}
			m = append(m, numberFor(v))
		}
		pattern.Dict.Set("Matrix", m)
	}
	pattern.Dict.Set("Filter", object.Name("FlateDecode"))
	pattern.Dict.Set("Length", object.Integer(len(compressed)))
	return d.Add(pattern), nil
}

// checkStep refuses a step that would make a reader repeat for ever or not at
// all. The sign is free — a negative step tiles in the other direction — but
// zero is not a direction and neither is a non-finite number.
func checkStep(name string, v float64) error {
	if v == 0 {
		return fmt.Errorf("pdf0: the pattern's %s is zero; every tile would land on the last", name)
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return fmt.Errorf("pdf0: the pattern's %s is %v, which is not a distance", name, v)
	}
	return nil
}

// UncoloredPatternSpace is the colour space a page needs in order to paint with
// an uncoloured pattern: /Pattern with an underlying space that says what the
// colour components mean.
//
// A coloured pattern needs no such thing — it is selected with /Pattern alone,
// which SetColorSpace can name directly. An uncoloured one is selected with a
// colour *and* a pattern name, and the underlying space is what the colour is
// expressed in. Getting this wrong produces a pattern painted in an
// unpredictable colour rather than an error, which is why it is offered rather
// than left to the caller to assemble.
func UncoloredPatternSpace(underlying object.Name) object.Array {
	return object.Array{object.Name("Pattern"), underlying}
}
