package pdf0

import (
	"fmt"

	"github.com/mgilbir/pdf0/object"
)

// The logical structure tree: what a tagged PDF is (ISO 32000-2 14.7).
//
// A content stream says where marks go on a page. It says nothing about what
// they *are* — which marks form a paragraph, which is a heading, what order to
// read them in, what a picture depicts. A screen reader given an untagged PDF
// can do no better than guess from position, and gets tables, columns and
// figures wrong. The structure tree is where that information lives, and
// without it PDF/UA and PDF/A level A are unreachable.
//
// # The two ends of the sentence
//
// Tagging is a statement with two halves that have to agree. The content stream
// marks a span with an identifier (content.Builder.BeginTagged), and a
// structure element names that identifier together with the page it is on. Both
// halves are the caller's to write, because only the caller knows which marks
// belong together — but everything that connects them is bookkeeping, and it is
// bookkeeping no one gets right by hand: each page needs an index into a number
// tree, and that tree needs, for every identifier on every page, the element
// that owns it. Get one index wrong and a reader silently reads the document in
// the wrong order.
//
// SetStructureTree does all of that from the tree the caller describes.

// StructElem is one node of the structure tree: what a piece of the document
// is, and which marks on which page make it up.
type StructElem struct {
	// Tag is the structure type: "P" for a paragraph, "H1" for a first-level
	// heading, "Figure", "Table", "TR", "TD" and the rest. It must be one of
	// the standard types, or a name the RoleMap passed to SetStructureTree maps
	// onto one — a reader that meets a tag it cannot interpret and cannot map
	// treats the element as meaningless.
	Tag string

	// Alt is alternate text: what the element says, for a reader that cannot
	// see it. A Figure without it is the single most common accessibility
	// failure in generated PDFs, and it is what a checker reports first.
	Alt string

	// ActualText is the text this element actually represents, replacing what
	// its marks would extract to. It is for content whose glyphs are not its
	// text: a ligature, a drop cap, a word broken across lines.
	ActualText string

	// Lang is a BCP 47 language tag for this element and everything under it,
	// where it differs from the document's.
	Lang string

	// Page is the page whose content stream carries this element's marks. It is
	// required when Content is non-empty, and inherited by children that do not
	// state their own.
	Page *object.IndirectRef

	// Content are the marked-content identifiers on Page that make up this
	// element — the numbers passed to content.Builder.BeginTagged.
	Content []int

	// Children are the elements nested inside this one.
	Children []StructElem
}

// SetStructureTree replaces the document's logical structure.
//
// roleMap maps any non-standard tag onto the standard type it behaves like. It
// may be nil when only standard tags are used, which is the usual case.
//
// The document is marked as tagged, every page that carries content gets its
// index into the parent tree, and the parent tree is built so that a reader
// starting from any mark on any page can find the element that owns it.
func (d *Document) SetStructureTree(root []StructElem, roleMap map[string]string) error {
	catalog := d.ResolveDict(d.Trailer.Get("Root"))
	if catalog == nil {
		return fmt.Errorf("pdf0: the document has no catalog to attach a structure tree to")
	}
	if len(root) == 0 {
		catalog.Delete("StructTreeRoot")
		catalog.Delete("MarkInfo")
		return nil
	}
	if err := checkStructure(root, roleMap, 0, nil); err != nil {
		return err
	}

	treeRoot := &object.Dictionary{}
	treeRoot.Set("Type", object.Name("StructTreeRoot"))
	rootRef := d.Add(treeRoot)

	// owners collects, per page, which element owns each identifier. It is
	// filled as the tree is written and turned into the parent tree afterwards,
	// because an element's reference does not exist until it is written.
	owners := newMCIDOwners()
	kids, err := d.writeStructLevel(root, rootRef, nil, owners)
	if err != nil {
		return err
	}
	treeRoot.Set("K", kids)
	if len(roleMap) > 0 {
		rm := &object.Dictionary{}
		for from, to := range roleMap {
			rm.Set(object.Name(from), object.Name(to))
		}
		treeRoot.Set("RoleMap", rm)
	}
	if err := d.writeParentTree(treeRoot, owners); err != nil {
		return err
	}

	catalog.Set("StructTreeRoot", rootRef)
	markInfo := &object.Dictionary{}
	markInfo.Set("Marked", object.Boolean(true))
	catalog.Set("MarkInfo", markInfo)
	return nil
}

// maxStructureDepth bounds the recursion. A document nested this deeply is a
// mistake or an attack; either way no reader would present it.
const maxStructureDepth = 64

// checkStructure validates the tree before anything is written, so a rejected
// tree leaves no half-written objects behind.
//
// It carries the inherited page down exactly as the writer does, because an
// element's page may come from an ancestor: a section states the page once and
// the paragraphs inside it do not repeat it. Checking without that inheritance
// would reject the ordinary shape of a tree.
func checkStructure(elems []StructElem, roleMap map[string]string, depth int,
	inheritedPage *object.IndirectRef) error {

	if depth > maxStructureDepth {
		return fmt.Errorf("pdf0: the structure tree is nested more than %d deep", maxStructureDepth)
	}
	for _, e := range elems {
		if e.Tag == "" {
			return fmt.Errorf("pdf0: a structure element has no tag")
		}
		page := e.Page
		if page == nil {
			page = inheritedPage
		}
		if !standardStructureTypes[e.Tag] {
			if _, mapped := roleMap[e.Tag]; !mapped {
				return fmt.Errorf(
					"pdf0: %q is not a standard structure type and the role map does not say what it is; "+
						"a reader would treat the element as meaningless", e.Tag)
			}
		}
		for _, mcid := range e.Content {
			if mcid < 0 {
				return fmt.Errorf("pdf0: structure element %q names a negative marked-content identifier", e.Tag)
			}
		}
		if len(e.Content) > 0 && page == nil {
			return fmt.Errorf(
				"pdf0: structure element %q names marked content but no page, and no ancestor states one; "+
					"an identifier is only unique within one page", e.Tag)
		}
		if err := checkStructure(e.Children, roleMap, depth+1, page); err != nil {
			return err
		}
	}
	for from, to := range roleMap {
		if !standardStructureTypes[to] {
			return fmt.Errorf("pdf0: the role map sends %q to %q, which is not a standard structure type", from, to)
		}
	}
	return nil
}

// writeStructLevel writes one level of the tree and returns what its parent's
// /K should hold.
func (d *Document) writeStructLevel(elems []StructElem, parent object.IndirectRef,
	inheritedPage *object.IndirectRef, owners *mcidOwners) (object.Object, error) {

	out := make(object.Array, 0, len(elems))
	for _, e := range elems {
		dict := &object.Dictionary{}
		dict.Set("Type", object.Name("StructElem"))
		dict.Set("S", object.Name(e.Tag))
		dict.Set("P", parent)

		page := e.Page
		if page == nil {
			page = inheritedPage
		}
		if page != nil {
			dict.Set("Pg", *page)
		}
		if e.Alt != "" {
			dict.Set("Alt", object.String{Value: encodePDFText(e.Alt)})
		}
		if e.ActualText != "" {
			dict.Set("ActualText", object.String{Value: encodePDFText(e.ActualText)})
		}
		if e.Lang != "" {
			dict.Set("Lang", object.String{Value: []byte(e.Lang)})
		}
		ref := d.Add(dict)

		// An element's own marks come first, then its children — which is the
		// reading order, and reading order is the whole point of the tree.
		kids := make(object.Array, 0, len(e.Content)+len(e.Children))
		for _, mcid := range e.Content {
			if err := owners.claim(*page, mcid, ref, e.Tag); err != nil {
				return nil, err
			}
			kids = append(kids, object.Integer(mcid))
		}
		if len(e.Children) > 0 {
			childKids, err := d.writeStructLevel(e.Children, ref, page, owners)
			if err != nil {
				return nil, err
			}
			if arr, ok := childKids.(object.Array); ok {
				kids = append(kids, arr...)
			} else {
				kids = append(kids, childKids)
			}
		}
		switch len(kids) {
		case 0:
			// No /K at all: an element with neither content nor children is
			// legal and describes a structure that is there but empty.
		case 1:
			// A single kid is written directly rather than in a one-element
			// array, which is what a reader expects to see.
			dict.Set("K", kids[0])
		default:
			dict.Set("K", kids)
		}
		out = append(out, ref)
	}
	if len(out) == 1 {
		return out[0], nil
	}
	return out, nil
}

// mcidOwners records which structure element owns each marked-content
// identifier on each page.
type mcidOwners struct {
	// byPage is keyed by the page's object number, because that is what
	// identifies a page and an IndirectRef is not comparable as a map key in a
	// way that ignores its generation.
	byPage map[int]map[int]object.IndirectRef
	// pageRefs keeps one reference per page so the page dictionary can be found
	// again when its index is assigned.
	pageRefs map[int]object.IndirectRef
	// order is the pages in the order they were first claimed, so the indices
	// assigned are stable rather than dependent on map iteration.
	order []int
}

func newMCIDOwners() *mcidOwners {
	return &mcidOwners{
		byPage:   map[int]map[int]object.IndirectRef{},
		pageRefs: map[int]object.IndirectRef{},
	}
}

// claim records that an element owns an identifier on a page, refusing a second
// claim on the same one.
//
// Two elements owning one identifier is not a redundancy, it is a contradiction:
// the parent tree has one slot per identifier, so the second claim would
// silently replace the first and a reader would attribute the marks to the wrong
// element.
func (o *mcidOwners) claim(page object.IndirectRef, mcid int, owner object.IndirectRef, tag string) error {
	on, seen := o.byPage[page.Number]
	if !seen {
		on = map[int]object.IndirectRef{}
		o.byPage[page.Number] = on
		o.pageRefs[page.Number] = page
		o.order = append(o.order, page.Number)
	}
	if _, taken := on[mcid]; taken {
		return fmt.Errorf(
			"pdf0: marked-content identifier %d on page %d is claimed twice; "+
				"the second claim is by %q, and an identifier belongs to one element",
			mcid, page.Number, tag)
	}
	on[mcid] = owner
	return nil
}

// writeParentTree builds the number tree that takes a page's index and an
// identifier back to the element that owns it, and stamps each page with its
// index.
func (d *Document) writeParentTree(treeRoot *object.Dictionary, owners *mcidOwners) error {
	nums := object.Array{}
	for index, pageNum := range owners.order {
		page := d.ResolveDict(owners.pageRefs[pageNum])
		if page == nil {
			return fmt.Errorf("pdf0: the structure tree names object %d as a page, and no such page exists", pageNum)
		}
		page.Set("StructParents", object.Integer(index))

		// The entry for a page is an array indexed by identifier, so it has to
		// be dense: a gap is a null, not a shorter array, because the position
		// *is* the identifier.
		on := owners.byPage[pageNum]
		highest := -1
		for mcid := range on {
			if mcid > highest {
				highest = mcid
			}
		}
		elems := make(object.Array, highest+1)
		for i := range elems {
			elems[i] = object.Null{}
		}
		for mcid, owner := range on {
			elems[mcid] = owner
		}
		nums = append(nums, object.Integer(index), d.Add(elems))
	}
	parentTree := &object.Dictionary{}
	parentTree.Set("Nums", nums)
	treeRoot.Set("ParentTree", d.Add(parentTree))
	treeRoot.Set("ParentTreeNextKey", object.Integer(len(owners.order)))
	return nil
}

// standardStructureTypes is the set a reader is required to understand
// (ISO 32000-2 14.8.4). Anything else needs a role map saying which of these it
// behaves like.
var standardStructureTypes = map[string]bool{
	// Grouping
	"Document": true, "DocumentFragment": true, "Part": true, "Sect": true,
	"Div": true, "Aside": true, "NonStruct": true, "Private": true,
	"Art": true, "BlockQuote": true, "Caption": true, "TOC": true, "TOCI": true,
	"Index": true,

	// Block-level
	"P": true, "H": true,
	"H1": true, "H2": true, "H3": true, "H4": true, "H5": true, "H6": true,
	"Title": true,
	"L":     true, "LI": true, "Lbl": true, "LBody": true,
	"Table": true, "TR": true, "TH": true, "TD": true,
	"THead": true, "TBody": true, "TFoot": true,

	// Inline
	"Span": true, "Quote": true, "Note": true, "Reference": true,
	"BibEntry": true, "Code": true, "Link": true, "Annot": true,
	"Em": true, "Strong": true, "Sub": true, "FENote": true,
	"Ruby": true, "RB": true, "RT": true, "RP": true,
	"Warichu": true, "WT": true, "WP": true,

	// Illustration
	"Figure": true, "Formula": true, "Form": true,

	// Artifact: content that is not part of the document's meaning — a running
	// header, a page number, a decorative rule.
	"Artifact": true,
}
