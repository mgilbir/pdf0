package pdf0

import (
	"fmt"

	"github.com/mgilbir/pdf0/content"
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

	// Content is the drawing. Its errors surface here, so a caller may draw
	// without checking and find out once.
	Content *content.Builder

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
	pageRef := d.Add(page)

	kids, _ := d.Resolve(pages.Get("Kids")).(object.Array)
	pages.Set("Kids", append(kids, pageRef))
	pages.Set("Count", object.Integer(len(kids)+1))
	return pageRef, nil
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

// numberFor writes a value as an integer when it is one, which keeps the file
// tidy and matches what a reader expects to see for a page size.
func numberFor(v float64) object.Object {
	if v == float64(int(v)) {
		return object.Integer(int(v))
	}
	return object.Real(v)
}
