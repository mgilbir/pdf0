package pdf0

import (
	"errors"
	"fmt"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// Page-tree maintenance: the one place that adds pages to a document's tree.
//
// AddPage and the page importer (ExtractPages, AppendPages) both end by hanging
// new page dictionaries under the tree the catalog names. Doing that correctly
// on a tree this package did not build is three obligations, and each was
// missed at least once when every caller did it for itself:
//
//   - /Count is the number of *leaves* beneath a node, not the length of its
//     /Kids. On a nested tree the two differ, and a reader that trusts /Count
//     shows the wrong number of pages (audit 2026-09-22 C29).
//   - A new page inherits whatever its new ancestors state for the four
//     inheritable attributes (ISO 32000-2 7.7.3.4). A page that does not want
//     the root's /Rotate 90 has to say /Rotate 0 (C29, C30).
//   - The catalog's /Pages may be a direct dictionary. It reads fine, but no
//     /Parent can name it, so it is promoted to an indirect object before
//     anything is hung under it (C20).

// errNilDocument is what every page-tree operation returns for a nil
// *Document, instead of the nil dereference it would otherwise be.
var errNilDocument = errors.New("pdf0: the document is nil")

// errLockedTarget refuses to add plaintext to a document whose content is still
// ciphertext. Write passes a Locked document through verbatim under its
// original /Encrypt, so anything added in the clear would be "decrypted" by
// every reader into noise.
func errLockedTarget(op string) error {
	return fmt.Errorf("pdf0: %s: the document is encrypted and was not decrypted (Locked); "+
		"what was added would be written in the clear under its /Encrypt and read back as noise", op)
}

// pageTreeRoot returns the root of the catalog's page tree as an indirect
// reference, promoting a direct /Pages dictionary to an indirect object first.
func (d *Document) pageTreeRoot() (object.IndirectRef, *object.Dictionary, error) {
	catalog := d.ResolveDict(d.Trailer.Get("Root"))
	if catalog == nil {
		return object.IndirectRef{}, nil, fmt.Errorf("pdf0: the document has no catalog to add a page to")
	}
	raw := catalog.Get("Pages")
	if raw == nil {
		return object.IndirectRef{}, nil, fmt.Errorf("pdf0: the catalog names no page tree")
	}
	root := d.ResolveDict(raw)
	if root == nil {
		return object.IndirectRef{}, nil, fmt.Errorf("pdf0: the catalog's page tree is missing")
	}
	if typ, _ := d.Resolve(root.Get("Type")).(object.Name); typ != "Pages" {
		return object.IndirectRef{}, nil, fmt.Errorf("pdf0: the catalog's /Pages is not a page-tree node (its /Type is %v)", root.Get("Type"))
	}
	if ref, ok := raw.(object.IndirectRef); ok {
		return ref, root, nil
	}
	// A direct root: give it a number, and make its children's /Parent name it.
	// Their /Parent could not have named it before, so whatever they held was
	// wrong; this is the only value that makes the tree well formed.
	ref := d.Add(root)
	catalog.Set("Pages", ref)
	if kids, ok := d.Resolve(root.Get("Kids")).(object.Array); ok {
		for _, kid := range kids {
			if kd := d.ResolveDict(kid); kd != nil {
				kd.Set("Parent", ref)
			}
		}
	}
	return ref, root, nil
}

// appendToPageTree hangs the given page dictionaries, in order, at the end of
// the document's page tree: under the root, which is an ancestor of every page
// and so the node whose inherited attributes have to be shielded against.
//
// Each page must already carry /MediaBox and /Resources of its own. The
// optional inheritable attributes it does not state are written with their
// default value when the tree above it states one, so the page shows as it
// would have on its own. /Count grows by the number of pages added.
func (d *Document) appendToPageTree(refs []object.IndirectRef) error {
	rootRef, root, err := d.pageTreeRoot()
	if err != nil {
		return err
	}
	for _, ref := range refs {
		page := d.ResolveDict(ref)
		if page == nil {
			return fmt.Errorf("pdf0: page object %d does not exist", ref.Number)
		}
		shieldInherited(d.graph(), page, root)
		page.Set("Parent", rootRef)
	}
	// Count from the root's own claim when it makes one. Recounting would walk
	// the whole tree on every AddPage, turning a document built page by page
	// into quadratic work; a root with no usable /Count is recounted once.
	count, ok := d.Resolve(root.Get("Count")).(object.Integer)
	if !ok || count < 0 {
		count = object.Integer(len(d.graph().Pages(rootRef)))
	}
	kids, _ := d.Resolve(root.Get("Kids")).(object.Array)
	grown := make(object.Array, 0, len(kids)+len(refs))
	grown = append(grown, kids...)
	for _, ref := range refs {
		grown = append(grown, ref)
	}
	root.Set("Kids", grown)
	root.Set("Count", count+object.Integer(len(refs)))
	return nil
}

// shieldInherited writes onto page, for each optional inheritable attribute it
// does not state, the default value — when the node it is about to hang under
// (or an ancestor of that node) states one. Without it the page silently takes
// the tree's value: a page that is upright becomes rotated because the root of
// the document it joined says /Rotate 90.
//
// /Resources and /MediaBox are required on every page this package adds, so
// only /CropBox (default: the media box) and /Rotate (default: 0) can be
// missing here.
func shieldInherited(g core.View, page, parent *object.Dictionary) {
	inherited := func(key object.Name) bool {
		node := parent
		for hops := 0; node != nil && hops < 64; hops++ {
			if node.Get(key) != nil {
				return true
			}
			node = g.ResolveDict(node.Get("Parent"))
		}
		return false
	}
	if page.Get("Rotate") == nil && inherited("Rotate") {
		page.Set("Rotate", object.Integer(0))
	}
	if page.Get("CropBox") == nil && inherited("CropBox") {
		if box, ok := g.Resolve(page.Get("MediaBox")).(object.Array); ok {
			page.Set("CropBox", append(object.Array(nil), box...))
		}
	}
}

// requirePage checks that ref names a page of this document: one listed in its
// page tree, which is what PageList returns. A destination, an outline entry or
// a structure element that names anything else — the catalog, a font, an object
// that does not exist — is a link no reader can follow (audit 2026-09-22 C136).
//
// A well-formed tree is checked by walking up from the page, checking at each
// step that the parent lists the child, which costs the depth of the tree
// rather than its size; anything else falls back to the full walk.
func (d *Document) requirePage(ref object.IndirectRef, what string) error {
	if d.onPageTreePath(ref) {
		return nil
	}
	for _, p := range d.graph().Pages(d.graph().CatalogPages()) {
		if p.ObjNum == ref.Number && p.ObjNum != 0 {
			return nil
		}
	}
	return fmt.Errorf("pdf0: %s names object %d, which is not a page of this document", what, ref.Number)
}

// onPageTreePath reports whether ref is a /Page whose /Parent chain climbs,
// through nodes that each list the one below in /Kids, to the catalog's root.
// A true answer means PageList includes it; a false one means only that this
// shortcut could not tell.
func (d *Document) onPageTreePath(ref object.IndirectRef) bool {
	g := d.graph()
	page := g.ResolveDict(ref)
	if page == nil {
		return false
	}
	if typ, _ := g.ResolveName(page.Get("Type")); typ != "Page" {
		return false
	}
	rootRef, ok := g.CatalogPages().(object.IndirectRef)
	if !ok {
		return false
	}
	child := ref
	for hops := 0; hops < 64; hops++ {
		node := g.ResolveDict(child)
		if node == nil {
			return false
		}
		up, ok := node.Get("Parent").(object.IndirectRef)
		if !ok {
			return false
		}
		parent := g.ResolveDict(up)
		if parent == nil {
			return false
		}
		if typ, _ := g.ResolveName(parent.Get("Type")); typ != "Pages" {
			return false
		}
		kids, _ := g.Resolve(parent.Get("Kids")).(object.Array)
		listed := false
		for _, k := range kids {
			if kr, ok := k.(object.IndirectRef); ok && kr.Number == child.Number {
				listed = true
				break
			}
		}
		if !listed {
			return false
		}
		if up.Number == rootRef.Number {
			return true
		}
		child = up
	}
	return false
}
