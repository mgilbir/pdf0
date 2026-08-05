package pdf0

import (
	"fmt"

	"github.com/mgilbir/pdf0/object"
)

// The document outline: the tree of bookmarks a reader shows beside the page.
//
// It is the only navigation a long document has that does not require reading
// it, and it is the natural destination for the heading structure of anything
// generated from a source that has headings. ISO 32000-2 12.3.3 gives it as a
// doubly linked tree, which is the part worth getting right once: every item
// carries its parent, its previous and next siblings, and its first and last
// children, and a reader that meets an inconsistent set of those shows a
// mangled outline rather than reporting anything.

// OutlineItem is one entry in the outline: a title, where it goes, and the
// entries nested under it.
type OutlineItem struct {
	// Title is the text shown. It may be any Unicode; it is written as a PDF
	// text string, so a reader displays it whatever script it is in.
	Title string

	// Page is the page to show when the entry is chosen.
	Page object.IndirectRef

	// To says where on that page to arrive. The zero value shows the whole
	// page. An outline generated from headings wants AtTop with the heading's y
	// coordinate: a contents entry that lands at the top of a long page has
	// pointed at the page rather than at the section.
	To Destination

	// Open makes the entry's children visible when the document is first
	// shown. It has no effect on an entry with no children.
	Open bool

	// Children are the entries nested under this one.
	Children []OutlineItem
}

// SetOutline replaces the document's outline.
//
// The tree is written with every link a reader needs — parent, siblings and
// children — and with the /Count values that say how many descendants each
// entry has and whether they start visible. A negative count means closed,
// which is how the format distinguishes "collapsed, with this many inside"
// from "open, with this many showing".
func (d *Document) SetOutline(items []OutlineItem) error {
	catalog := d.ResolveDict(d.Trailer.Get("Root"))
	if catalog == nil {
		return fmt.Errorf("pdf0: the document has no catalog to attach an outline to")
	}
	if len(items) == 0 {
		catalog.Delete("Outlines")
		return nil
	}
	if err := checkOutline(items, 0); err != nil {
		return err
	}

	root := &object.Dictionary{}
	root.Set("Type", object.Name("Outlines"))
	rootRef := d.Add(root)

	first, last, visible, err := d.writeOutlineLevel(items, rootRef)
	if err != nil {
		return err
	}
	root.Set("First", first)
	root.Set("Last", last)
	// The root's count is always the number of entries a reader will show, and
	// is never negative: the outline itself cannot be collapsed.
	root.Set("Count", object.Integer(visible))
	catalog.Set("Outlines", rootRef)
	return nil
}

// maxOutlineDepth bounds the recursion. An outline is a document's table of
// contents; one nested this deeply is a mistake or an attack, and either way
// the tree would be unusable.
const maxOutlineDepth = 32

func checkOutline(items []OutlineItem, depth int) error {
	if depth > maxOutlineDepth {
		return fmt.Errorf("pdf0: the outline is nested more than %d deep", maxOutlineDepth)
	}
	for i, it := range items {
		if it.Title == "" {
			return fmt.Errorf("pdf0: outline entry %d has no title", i)
		}
		if it.Page.Number <= 0 {
			return fmt.Errorf("pdf0: outline entry %q names no page", it.Title)
		}
		if err := checkOutline(it.Children, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// writeOutlineLevel writes one level of the tree and returns its first and last
// entries, together with the number of entries a reader will show for it —
// which is this level's entries plus the visible descendants of any that are
// open.
func (d *Document) writeOutlineLevel(items []OutlineItem, parent object.IndirectRef) (first, last object.Object, visible int, err error) {
	refs := make([]object.IndirectRef, len(items))
	dicts := make([]*object.Dictionary, len(items))
	for i, it := range items {
		dict := &object.Dictionary{}
		dict.Set("Title", object.String{Value: encodePDFText(it.Title)})
		dict.Set("Parent", parent)
		// A destination rather than an action: an action would be subject to
		// the restrictions PDF/A places on actions, and this is navigation
		// rather than behaviour.
		dest, destErr := it.To.destination(it.Page)
		if destErr != nil {
			return nil, nil, 0, fmt.Errorf("outline entry %q: %w", it.Title, destErr)
		}
		dict.Set("Dest", dest)
		dicts[i] = dict
		refs[i] = d.Add(dict)
	}
	for i := range items {
		if i > 0 {
			dicts[i].Set("Prev", refs[i-1])
		}
		if i+1 < len(items) {
			dicts[i].Set("Next", refs[i+1])
		}
		if kids := items[i].Children; len(kids) > 0 {
			kFirst, kLast, kVisible, kErr := d.writeOutlineLevel(kids, refs[i])
			if kErr != nil {
				return nil, nil, 0, kErr
			}
			dicts[i].Set("First", kFirst)
			dicts[i].Set("Last", kLast)
			if items[i].Open {
				dicts[i].Set("Count", object.Integer(kVisible))
				visible += kVisible
			} else {
				// Negative: this many descendants, none of them showing.
				dicts[i].Set("Count", object.Integer(-kVisible))
			}
		}
		visible++
	}
	return refs[0], refs[len(refs)-1], visible, nil
}

// encodePDFText writes a string as a PDF text string: ASCII stays as it is, and
// anything else becomes UTF-16BE behind a byte-order mark (ISO 32000-2 7.9.2.2).
//
// The two forms exist because the byte-order mark is what tells a reader which
// it is looking at. A title of "Introduction" written as UTF-16 is legal and
// twice the size; a title of "Введение" written as bytes is a different string.
func encodePDFText(s string) []byte {
	ascii := true
	for _, r := range s {
		if r > 0x7E || r < 0x20 {
			ascii = false
			break
		}
	}
	if ascii {
		return []byte(s)
	}
	out := []byte{0xFE, 0xFF}
	for _, r := range s {
		if r > 0xFFFF {
			r -= 0x10000
			hi, lo := 0xD800+(r>>10), 0xDC00+(r&0x3FF)
			out = append(out, byte(hi>>8), byte(hi), byte(lo>>8), byte(lo))
			continue
		}
		out = append(out, byte(r>>8), byte(r))
	}
	return out
}
