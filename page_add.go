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
// than a page nothing points at. The tree may be any shape a file has: /Count
// stays the number of pages, and where the tree states a /Rotate or /CropBox
// the page would otherwise inherit, the page states its own.
//
// A link's Page must already be a page of this document, so a link can only
// lead backwards; a Locked document is refused, since Write passes its content
// through as ciphertext and the new page would be read back as noise.
func (d *Document) AddPage(p Page) (object.IndirectRef, error) {
	if d == nil {
		return object.IndirectRef{}, errNilDocument
	}
	if d.Locked() {
		return object.IndirectRef{}, errLockedTarget("adding a page")
	}
	// Everything is checked before anything is written, so a refusal leaves
	// the document as it was (audit 2026-09-22 C131).
	if p.Content == nil {
		return object.IndirectRef{}, fmt.Errorf("pdf0: the page has no content")
	}
	drawn, err := p.Content.Bytes()
	if err != nil {
		return object.IndirectRef{}, err
	}
	if err := checkPositive("the page width", p.Width); err != nil {
		return object.IndirectRef{}, err
	}
	if err := checkPositive("the page height", p.Height); err != nil {
		return object.IndirectRef{}, err
	}
	if p.Rotate%90 != 0 {
		return object.IndirectRef{}, fmt.Errorf("pdf0: page rotation %d is not a multiple of 90", p.Rotate)
	}
	// The tree too. (A direct /Pages root is promoted here; that is a repair,
	// not a partial page.)
	if _, _, err := d.pageTreeRoot(); err != nil {
		return object.IndirectRef{}, err
	}
	links := make([]*object.Dictionary, 0, len(p.Links))
	for i, l := range p.Links {
		if l.Page != nil {
			if err := d.requirePage(*l.Page, fmt.Sprintf("link %d", i)); err != nil {
				return object.IndirectRef{}, err
			}
		}
		a, err := l.annotation()
		if err != nil {
			return object.IndirectRef{}, fmt.Errorf("link %d: %w", i, err)
		}
		links = append(links, a)
	}
	res := p.resourceSet()
	if err := res.check(p.Content.Resources()); err != nil {
		return object.IndirectRef{}, err
	}
	// After the content is final, which is what makes subsetting correct. It
	// is the one step past this point that can fail, and it adds nothing to
	// the document when it does.
	faceRefs, err := d.embedFaces(p.Faces)
	if err != nil {
		return object.IndirectRef{}, err
	}
	resources := res.build(p.Content.Resources(), faceRefs)

	compressed := core.FlateEncode(drawn)
	stream := object.NewStream(object.NewDictionary(
		object.Entry{Key: "Filter", Value: object.Name("FlateDecode")},
		object.Entry{Key: "Length", Value: object.Integer(len(compressed))},
	), compressed)
	contentRef := d.Add(stream)

	page := &object.Dictionary{}
	page.Set("Type", object.Name("Page"))
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
	if len(links) > 0 {
		annots := make(object.Array, 0, len(links))
		for _, a := range links {
			annots = append(annots, d.Add(a))
		}
		page.Set("Annots", annots)
	}
	pageRef := d.Add(page)
	// The tree code sets /Parent, keeps /Count the number of pages, and writes
	// /Rotate 0 or a /CropBox when the tree above would otherwise lend the page
	// its own (audit 2026-09-22 C29).
	if err := d.appendToPageTree([]object.IndirectRef{pageRef}); err != nil {
		return object.IndirectRef{}, err
	}
	return pageRef, nil
}

// embedFaces writes each named face into the document and returns the
// reference each name is to resolve to.
//
// It is called once the content stream is final: a face is subsetted to the
// glyphs it was asked to set, so embedding it any earlier produces a font that
// contains nothing the page uses. Its objects are staged and added together,
// so a face that cannot be embedded leaves no other face's objects behind.
func (d *Document) embedFaces(faces map[object.Name]*fonts.Face) (map[object.Name]object.IndirectRef, error) {
	if len(faces) == 0 {
		return nil, nil
	}
	stage := d.stageAdds()
	refs := make(map[object.Name]object.IndirectRef, len(faces))
	for _, name := range sortedNames(faces) {
		ref, err := faces[name].Embed(stage)
		if err != nil {
			stage.abort()
			return nil, fmt.Errorf("embedding the face named %s: %w", name, err)
		}
		refs[name] = ref
	}
	stage.commit()
	return refs, nil
}

// resourceSet is the resources a page, form or pattern was given: the faces to
// embed and the maps naming everything else, by the /Resources subdictionary
// each belongs in.
type resourceSet struct {
	faces  map[object.Name]*fonts.Face
	groups []resourceGroup
}

type resourceGroup struct {
	key  object.Name
	defs map[object.Name]object.Object
}

func newResourceSet(faces map[object.Name]*fonts.Face, fonts, xobjects, extGStates, colorSpaces,
	shadings, patterns, properties map[object.Name]object.Object) resourceSet {
	return resourceSet{faces: faces, groups: []resourceGroup{
		{"Font", fonts}, {"XObject", xobjects}, {"ExtGState", extGStates},
		{"ColorSpace", colorSpaces}, {"Shading", shadings}, {"Pattern", patterns},
		{"Properties", properties},
	}}
}

func (p Page) resourceSet() resourceSet {
	return newResourceSet(p.Faces, p.Fonts, p.XObjects, p.ExtGStates, p.ColorSpaces, p.Shadings, p.Patterns, p.Properties)
}

// used lists a group's names in the drawing, in first-use order.
func (g resourceGroup) used(u content.Resources) []object.Name {
	switch g.key {
	case "Font":
		return u.Fonts
	case "XObject":
		return u.XObjects
	case "ExtGState":
		return u.ExtGStates
	case "ColorSpace":
		return u.ColorSpaces
	case "Shading":
		return u.Shadings
	case "Pattern":
		return u.Patterns
	case "Properties":
		return u.Properties
	}
	return nil
}

// check refuses what build could not write: a nil face, a name that is both a
// face and a font dictionary, an entry with no value, and a name the drawing
// used that nothing defines — which is the check Builder.Resources exists to
// make possible. A face defines its name as a font.
func (r resourceSet) check(used content.Resources) error {
	for _, name := range sortedNames(r.faces) {
		if r.faces[name] == nil {
			return fmt.Errorf("pdf0: the face named %s is nil", name)
		}
		if _, clash := r.groups[0].defs[name]; clash {
			return fmt.Errorf(
				"pdf0: %s names both a face to embed and a font dictionary; it can be one of them", name)
		}
	}
	for _, g := range r.groups {
		for _, name := range sortedNames(g.defs) {
			if g.defs[name] == nil {
				return fmt.Errorf("pdf0: the %s resource %s has no value", g.key, name)
			}
		}
		for _, name := range g.used(used) {
			if _, ok := g.defs[name]; ok {
				continue
			}
			if _, ok := r.faces[name]; ok && g.key == "Font" {
				continue
			}
			return fmt.Errorf("pdf0: the content stream uses %s but no %s resource defines it", name, g.key)
		}
	}
	return nil
}

// build writes the /Resources dictionary: the names the drawing used, in the
// order it used them, then the ones it did not, in name order. A resource the
// caller defined and the drawing did not use is kept: a page may legitimately
// carry one for an annotation appearance, and dropping it silently would be a
// surprise. check has passed, so every name resolves.
func (r resourceSet) build(used content.Resources, faceRefs map[object.Name]object.IndirectRef) *object.Dictionary {
	out := &object.Dictionary{}
	for _, g := range r.groups {
		defs := g.defs
		if g.key == "Font" && len(faceRefs) > 0 {
			defs = make(map[object.Name]object.Object, len(g.defs)+len(faceRefs))
			for name, v := range g.defs {
				defs[name] = v
			}
			for name, ref := range faceRefs {
				defs[name] = ref
			}
		}
		names := g.used(used)
		if len(names) == 0 && len(defs) == 0 {
			continue
		}
		sub := &object.Dictionary{}
		for _, name := range names {
			sub.Set(name, defs[name])
		}
		for _, name := range sortedNames(defs) {
			if !sub.Has(name) {
				sub.Set(name, defs[name])
			}
		}
		out.Set(g.key, sub)
	}
	return out
}

// Opacity builds the graphics state dictionary that makes drawing translucent
// (ISO 32000-2 11.6.4.4). Both values are alpha in [0,1]: 1 is opaque.
//
// It is a document object rather than an operator, which is why it is here and
// not in the content package: the content stream names it with gs, and this is
// what the name has to refer to.
func Opacity(fill, stroke float64) (*object.Dictionary, error) {
	if err := checkUnit("the fill opacity", fill); err != nil {
		return nil, err
	}
	if err := checkUnit("the stroke opacity", stroke); err != nil {
		return nil, err
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
	for key, aval := range alpha.All() {
		if key == "Type" {
			continue
		}
		gs.Set(key, aval)
	}
	return gs, nil
}
