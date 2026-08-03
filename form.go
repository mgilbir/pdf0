package pdf0

import (
	"fmt"

	"github.com/mgilbir/pdf0/fonts"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// Form XObjects: a drawing stored once and painted many times.
//
// The content builder could already paint one with Do and nothing could make
// one. Two quite different needs are met by the same object:
//
//   - Reuse. A logo, a rule, a table cell background drawn on every page costs
//     its bytes once rather than once per use.
//   - Grouping. Translucency applied to a form applies to the form's result,
//     not to each mark in it — so two overlapping shapes at half opacity show
//     the page through both, rather than each through the other. That is what a
//     transparency group is for, and it is the only way to express what CSS
//     means by opacity on an element with children.

// Form is a reusable drawing.
type Form struct {
	// BBox bounds the drawing in its own coordinate space, as
	// [xMin, yMin, xMax, yMax]. Marks outside it are clipped away, so a box
	// smaller than the drawing is a silent crop.
	BBox [4]float64

	// Matrix maps the form's space into the space it is painted in, as the six
	// numbers of a PDF matrix. The zero value means the identity.
	Matrix *[6]float64

	// Content is the drawing.
	Content *content.Builder

	// Group makes the form a transparency group, so that opacity and blending
	// apply to its result as a whole rather than to each mark separately.
	Group bool

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

// AddForm writes a form XObject into the document and returns the reference to
// put in a page's /Resources /XObject.
//
// As with AddPage, a name the drawing used and no resource map defines is an
// error rather than a form that paints with something missing.
func (d *Document) AddForm(f Form) (object.IndirectRef, error) {
	if f.Content == nil {
		return object.IndirectRef{}, fmt.Errorf("pdf0: the form has no content")
	}
	drawn, err := f.Content.Bytes()
	if err != nil {
		return object.IndirectRef{}, err
	}
	if f.BBox[2] <= f.BBox[0] || f.BBox[3] <= f.BBox[1] {
		return object.IndirectRef{}, fmt.Errorf(
			"pdf0: the form's bounding box %v has no area; everything drawn would be clipped away", f.BBox)
	}
	embedded, err := d.embedFaces(f.Faces, f.Fonts)
	if err != nil {
		return object.IndirectRef{}, err
	}
	resources, err := Page{
		Content: f.Content, Fonts: embedded, XObjects: f.XObjects,
		ExtGStates: f.ExtGStates, ColorSpaces: f.ColorSpaces, Shadings: f.Shadings,
		Patterns: f.Patterns, Properties: f.Properties,
	}.resources()
	if err != nil {
		return object.IndirectRef{}, err
	}

	compressed := core.FlateEncode(drawn)
	form := &object.Stream{Dict: object.Dictionary{}, Data: compressed}
	form.Dict.Set("Type", object.Name("XObject"))
	form.Dict.Set("Subtype", object.Name("Form"))
	form.Dict.Set("BBox", object.Array{
		numberFor(f.BBox[0]), numberFor(f.BBox[1]),
		numberFor(f.BBox[2]), numberFor(f.BBox[3]),
	})
	if f.Matrix != nil {
		m := object.Array{}
		for _, v := range f.Matrix {
			m = append(m, numberFor(v))
		}
		form.Dict.Set("Matrix", m)
	}
	if f.Group {
		// An isolated, non-knockout group: the usual choice, and the one that
		// makes opacity behave the way a caller expects. Isolated means the
		// group composites against nothing rather than against the page beneath
		// it, so applying opacity to the result does not also fade what was
		// already there.
		group := &object.Dictionary{}
		group.Set("Type", object.Name("Group"))
		group.Set("S", object.Name("Transparency"))
		group.Set("CS", object.Name("DeviceRGB"))
		group.Set("I", object.Boolean(true))
		group.Set("K", object.Boolean(false))
		form.Dict.Set("Group", group)
	}
	form.Dict.Set("Resources", resources)
	form.Dict.Set("Filter", object.Name("FlateDecode"))
	form.Dict.Set("Length", object.Integer(len(compressed)))
	return d.Add(form), nil
}
