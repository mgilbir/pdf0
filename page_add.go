package pdf0

import (
	"fmt"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// Adding a page to a document.
//
// This is the piece that turns a content stream into a document. Drawing
// produced bytes and embedding produced a font; a page is what holds them,
// together with the dictionary naming every resource the drawing referred to.
// Assembling that by hand is a dozen lines that are the same every time and
// wrong in one place when they are not — the /Resources subdictionary a name
// belongs in, the /Parent pointer, the /Kids and /Count of the tree above.

// Page is a page to add: its size, what was drawn on it, and what the drawing's
// names refer to.
//
// The resource maps are keyed by the names used in the content stream. A name
// the drawing used and no map defines is an error rather than a page that
// renders with something missing — which is the check Builder.Resources exists
// to make possible, and which nothing could perform until there was somewhere
// to perform it.
type Page struct {
	// Width and Height are the page size in points. A4 is 595 × 842; US Letter
	// is 612 × 792.
	Width, Height float64

	// Rotate turns the page clockwise when it is displayed, in degrees. It must
	// be a multiple of 90; zero is upright.
	//
	// It rotates the *view*, not the content: a landscape page may be drawn
	// upright on a portrait box and rotated, or drawn rotated on a landscape
	// box, and the two are different files that look the same. This is the
	// first.
	Rotate int

	// Content is the drawing. Its errors surface here, so a caller may draw
	// without checking and find out once.
	Content *content.Builder

	// Links are the annotations that make part of the page follow a reference.
	Links []Link

	// Group makes the page a transparency group.
	//
	// It matters when anything on the page is translucent or uses a blend mode.
	// Without a group, what a translucent mark composites against is left to the
	// reader — usually white, sometimes the paper, sometimes nothing — so the
	// same file prints differently from how it displays. With one, the page
	// states its own blending colour space and the result is defined.
	Group bool

	// Faces are fonts to embed and name, by the name the drawing used.
	//
	// This is the ordinary way to put a font on a page. Embedding a face
	// subsets it to the glyphs it was actually asked to set, so it can only
	// happen once the drawing is finished — which makes the correct order
	// draw, embed, then build the page, and makes embedding first produce a
	// font containing nothing but .notdef. That is an ordering a caller has to
	// know and cannot be reminded of.
	//
	// Naming the face here removes the question: the drawing is complete by the
	// time a page is added, so this embeds it then. Use Fonts instead only for
	// a font dictionary built some other way.
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

// AddPage appends a page to the document's page tree and returns the reference
// to it.
//
// The content stream is Flate-compressed, which is what a producer does and
// what every reader expects; StreamData reads it back. The page is appended to
// the tree the catalog names, so a document that has none is an error rather
// than a page nothing points at.
func (d *Document) AddPage(p Page) (object.IndirectRef, error) {
	if p.Content == nil {
		return object.IndirectRef{}, fmt.Errorf("pdf0: the page has no content")
	}
	drawn, err := p.Content.Bytes()
	if err != nil {
		return object.IndirectRef{}, err
	}
	if p.Width <= 0 || p.Height <= 0 {
		return object.IndirectRef{}, fmt.Errorf("pdf0: page size %g×%g has no area", p.Width, p.Height)
	}
	if p.Rotate%90 != 0 {
		return object.IndirectRef{}, fmt.Errorf("pdf0: page rotation %d is not a multiple of 90", p.Rotate)
	}
	// After the content is final, which is what makes subsetting correct, and
	// before the resources are checked, which is what the names have to satisfy.
	if p.Fonts, err = d.embedFaces(p.Faces, p.Fonts); err != nil {
		return object.IndirectRef{}, err
	}
	resources, err := p.resources()
	if err != nil {
		return object.IndirectRef{}, err
	}

	pagesRef, pages, err := d.pageTree()
	if err != nil {
		return object.IndirectRef{}, err
	}

	compressed := core.FlateEncode(drawn)
	stream := &object.Stream{Dict: object.Dictionary{}, Data: compressed}
	stream.Dict.Set("Filter", object.Name("FlateDecode"))
	stream.Dict.Set("Length", object.Integer(len(compressed)))
	contentRef := d.Add(stream)

	page := &object.Dictionary{}
	page.Set("Type", object.Name("Page"))
	page.Set("Parent", pagesRef)
	page.Set("MediaBox", object.Array{
		object.Integer(0), object.Integer(0),
		numberFor(p.Width), numberFor(p.Height),
	})
	page.Set("Resources", resources)
	page.Set("Contents", contentRef)
	if p.Rotate != 0 {
		page.Set("Rotate", object.Integer(p.Rotate))
	}
	if p.Group {
		// A page group is not isolated and not a knockout: the page composites
		// onto whatever the reader puts behind it, which is what a page does.
		// Naming the blending colour space is the point — it is what makes a
		// translucent mark composite the same way everywhere.
		group := &object.Dictionary{}
		group.Set("Type", object.Name("Group"))
		group.Set("S", object.Name("Transparency"))
		group.Set("CS", object.Name("DeviceRGB"))
		page.Set("Group", group)
	}
	if len(p.Links) > 0 {
		annots := make(object.Array, 0, len(p.Links))
		for i, l := range p.Links {
			a, err := l.annotation()
			if err != nil {
				return object.IndirectRef{}, fmt.Errorf("link %d: %w", i, err)
			}
			annots = append(annots, d.Add(a))
		}
		page.Set("Annots", annots)
	}
	pageRef := d.Add(page)

	kids, _ := d.Resolve(pages.Get("Kids")).(object.Array)
	pages.Set("Kids", append(kids, pageRef))
	pages.Set("Count", object.Integer(len(kids)+1))
	return pageRef, nil
}

// embedFaces writes each named face into the document and merges the references
// into the font map, which is what the resource dictionary is built from.
//
// It is called once the content stream is final: a face is subsetted to the
// glyphs it was asked to set, so embedding it any earlier produces a font that
// contains nothing the page uses.
func (d *Document) embedFaces(faces map[object.Name]*fonts.Face, refs map[object.Name]object.Object) (map[object.Name]object.Object, error) {
	if len(faces) == 0 {
		return refs, nil
	}
	// A fresh map: the caller's must not gain entries it did not put there,
	// least of all when the same Page value is used twice.
	merged := make(map[object.Name]object.Object, len(refs)+len(faces))
	for name, value := range refs {
		merged[name] = value
	}
	for name, face := range faces {
		if face == nil {
			return nil, fmt.Errorf("pdf0: the face named /%s is nil", name)
		}
		if _, clash := merged[name]; clash {
			return nil, fmt.Errorf(
				"pdf0: /%s names both a face to embed and a font dictionary; it can be one of them", name)
		}
		ref, err := face.Embed(d)
		if err != nil {
			return nil, fmt.Errorf("embedding the face named /%s: %w", name, err)
		}
		merged[name] = ref
	}
	return merged, nil
}

// pageTree finds the /Pages node the catalog names, which is what a new page is
// appended to.
func (d *Document) pageTree() (object.IndirectRef, *object.Dictionary, error) {
	catalog := d.ResolveDict(d.Trailer.Get("Root"))
	if catalog == nil {
		return object.IndirectRef{}, nil, fmt.Errorf("pdf0: the document has no catalog to add a page to")
	}
	ref, ok := catalog.Get("Pages").(object.IndirectRef)
	if !ok {
		return object.IndirectRef{}, nil, fmt.Errorf("pdf0: the catalog names no page tree")
	}
	pages := d.ResolveDict(ref)
	if pages == nil {
		return object.IndirectRef{}, nil, fmt.Errorf("pdf0: the catalog's page tree is missing")
	}
	// A page tree with its own /Kids of page-tree nodes would need the new page
	// placed in one of them; this appends to a flat tree, which is what
	// NewPDFADocument builds and what this package writes.
	return ref, pages, nil
}

// resources builds the page's /Resources, and reports any name the drawing used
// that nothing defines.
func (p Page) resources() (*object.Dictionary, error) {
	used := p.Content.Resources()
	groups := []struct {
		key   object.Name
		names []object.Name
		defs  map[object.Name]object.Object
	}{
		{"Font", used.Fonts, p.Fonts},
		{"XObject", used.XObjects, p.XObjects},
		{"ExtGState", used.ExtGStates, p.ExtGStates},
		{"ColorSpace", used.ColorSpaces, p.ColorSpaces},
		{"Shading", used.Shadings, p.Shadings},
		{"Pattern", used.Patterns, p.Patterns},
		{"Properties", used.Properties, p.Properties},
	}
	out := &object.Dictionary{}
	for _, g := range groups {
		if len(g.names) == 0 && len(g.defs) == 0 {
			continue
		}
		sub := &object.Dictionary{}
		for _, name := range g.names {
			value, ok := g.defs[name]
			if !ok {
				return nil, fmt.Errorf("pdf0: the content stream uses /%s but no /%s resource defines it",
					name, g.key)
			}
			sub.Set(name, value)
		}
		// A resource the caller defined and the drawing did not use is kept:
		// a page may legitimately carry one for an annotation appearance, and
		// dropping it silently would be a surprise.
		for name, value := range g.defs {
			if sub.Get(name) == nil {
				sub.Set(name, value)
			}
		}
		out.Set(g.key, sub)
	}
	return out, nil
}

// Opacity builds the graphics state dictionary that makes drawing translucent
// (ISO 32000-2 11.6.4.4). Both values are alpha in [0,1]: 1 is opaque.
//
// It is a document object rather than an operator, which is why it is here and
// not in the content package: the content stream names it with gs, and this is
// what the name has to refer to.
func Opacity(fill, stroke float64) (*object.Dictionary, error) {
	if fill < 0 || fill > 1 || stroke < 0 || stroke > 1 {
		return nil, fmt.Errorf("pdf0: opacity (%g, %g) is outside [0,1]", fill, stroke)
	}
	gs := &object.Dictionary{}
	gs.Set("Type", object.Name("ExtGState"))
	gs.Set("ca", numberFor(fill))
	gs.Set("CA", numberFor(stroke))
	return gs, nil
}

// BlendMode is how a mark's colour combines with what is already beneath it
// (ISO 32000-2 11.3.5).
//
// Normal simply replaces, and is what a document does without saying so. The
// rest are what CSS calls mix-blend-mode, and the names are the same because
// both took them from the same place.
type BlendMode string

// The separable blend modes of ISO 32000-2 Table 134, and the four
// non-separable ones of Table 135.
//
// They are listed rather than accepted as free text because a reader that meets
// a name it does not know is required to treat it as Normal — so a typo in a
// blend mode is not an error anywhere, it is a page that quietly loses its
// blending.
const (
	BlendNormal     BlendMode = "Normal"
	BlendMultiply   BlendMode = "Multiply"
	BlendScreen     BlendMode = "Screen"
	BlendOverlay    BlendMode = "Overlay"
	BlendDarken     BlendMode = "Darken"
	BlendLighten    BlendMode = "Lighten"
	BlendColorDodge BlendMode = "ColorDodge"
	BlendColorBurn  BlendMode = "ColorBurn"
	BlendHardLight  BlendMode = "HardLight"
	BlendSoftLight  BlendMode = "SoftLight"
	BlendDifference BlendMode = "Difference"
	BlendExclusion  BlendMode = "Exclusion"

	BlendHue        BlendMode = "Hue"
	BlendSaturation BlendMode = "Saturation"
	BlendColor      BlendMode = "Color"
	BlendLuminosity BlendMode = "Luminosity"
)

var blendModes = map[BlendMode]bool{
	BlendNormal: true, BlendMultiply: true, BlendScreen: true, BlendOverlay: true,
	BlendDarken: true, BlendLighten: true, BlendColorDodge: true, BlendColorBurn: true,
	BlendHardLight: true, BlendSoftLight: true, BlendDifference: true, BlendExclusion: true,
	BlendHue: true, BlendSaturation: true, BlendColor: true, BlendLuminosity: true,
}

// Blend builds the graphics state that selects a blend mode.
//
// An unknown mode is refused rather than written. A reader meeting a name it
// does not recognise falls back to Normal without complaining, so a misspelt
// mode produces a page that silently loses its blending — which is exactly the
// kind of fault that is noticed months later and never traced.
func Blend(mode BlendMode) (*object.Dictionary, error) {
	if !blendModes[mode] {
		return nil, fmt.Errorf("pdf0: %q is not a blend mode; a reader would silently treat it as Normal", mode)
	}
	gs := &object.Dictionary{}
	gs.Set("Type", object.Name("ExtGState"))
	gs.Set("BM", object.Name(mode))
	return gs, nil
}

// BlendWithOpacity is Blend and Opacity together, which is the common case: CSS
// applies opacity and a blend mode to the same element, and two graphics states
// would need two gs operators and two names for one effect.
func BlendWithOpacity(mode BlendMode, fill, stroke float64) (*object.Dictionary, error) {
	gs, err := Blend(mode)
	if err != nil {
		return nil, err
	}
	alpha, err := Opacity(fill, stroke)
	if err != nil {
		return nil, err
	}
	for i, key := range alpha.Keys {
		if key == "Type" {
			continue
		}
		gs.Set(key, alpha.Values[i])
	}
	return gs, nil
}

// numberFor writes a value as an integer when it is one, which keeps the file
// tidy and matches what a reader expects to see for a page size.
func numberFor(v float64) object.Object {
	if v == float64(int(v)) {
		return object.Integer(int(v))
	}
	return object.Real(v)
}
