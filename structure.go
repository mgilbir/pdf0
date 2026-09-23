package pdf0

import (
	"fmt"
	"maps"
	"slices"
	"strconv"

	"github.com/mgilbir/pdf0/content"
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
	//
	// The types PDF 2.0 added (Annex M: DocumentFragment, Aside, Title,
	// FENote, Sub, Em, Strong, Artifact, and Hn for n > 6) exist only in the
	// PDF 2.0 standard structure namespace. In a PDF 2.0 document such an
	// element is written with an /NS naming that namespace; below PDF 2.0
	// there are no namespaces, and one is refused.
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
	// It must be one of PageList's pages.
	Page *object.IndirectRef

	// Content are the marked-content identifiers on Page that make up this
	// element — the numbers passed to content.Builder.BeginTagged. Each is at
	// most content.MaxMCID.
	Content []int

	// Children are the elements nested inside this one.
	Children []StructElem
}

// SetStructureTree replaces the document's logical structure.
//
// roleMap maps any non-standard tag onto the standard type it behaves like. It
// may be nil when only standard tags are used, which is the usual case. It is
// written as the tree root's /RoleMap, which maps into the default (PDF 1.7)
// standard structure namespace, so a role maps onto a PDF 1.7 type.
//
// The document is marked as tagged, every page that carries content gets its
// index into the parent tree, and the parent tree is built so that a reader
// starting from any mark on any page can find the element that owns it.
//
// It replaces: whatever indexed the previous tree is cleared first — the
// /StructParents of every page and of every XObject its resources reach, and
// the /StructParent of every annotation on a page. Left in place, an index into
// the old tree would name an entry of the new one, and a reader would attribute
// the marks to whichever element now has that slot. An empty root removes the
// tree, those indices and the /MarkInfo claim that the document is tagged.
//
// The tree is checked whole before anything is written, so a refused tree
// leaves the document as it was.
func (d *Document) SetStructureTree(root []StructElem, roleMap map[string]string) error {
	if d == nil {
		return errNilDocument
	}
	if d.Locked() {
		return errLockedTarget("setting the structure tree")
	}
	catalog := d.ResolveDict(d.Trailer.Get("Root"))
	if catalog == nil {
		return fmt.Errorf("pdf0: the document has no catalog to attach a structure tree to")
	}
	if len(root) == 0 {
		d.clearStructureIndices()
		catalog.Delete("StructTreeRoot")
		catalog.Delete("MarkInfo")
		return nil
	}
	check := structureCheck{roleMap: roleMap, pdf20: d.isPDF20(), claimed: map[int]map[int]bool{}}
	if err := check.elems(root, 0, nil); err != nil {
		return err
	}
	if err := check.roles(); err != nil {
		return err
	}
	if err := d.checkStructurePages(root); err != nil {
		return err
	}

	// Nothing past this point can be refused.
	d.clearStructureIndices()
	treeRoot := &object.Dictionary{}
	treeRoot.Set("Type", object.Name("StructTreeRoot"))
	rootRef := d.Add(treeRoot)

	// The PDF 2.0 namespace, declared once when an element needs it.
	var ns *object.IndirectRef
	if check.usesPDF20 {
		nsDict := object.NewDictionary(
			object.Entry{Key: "Type", Value: object.Name("Namespace")},
			object.Entry{Key: "NS", Value: object.String{Value: []byte(pdf20StructureNamespace)}},
		)
		ref := d.Add(nsDict)
		ns = &ref
		treeRoot.Set("Namespaces", object.Array{ref})
	}

	// owners collects, per page, which element owns each identifier. It is
	// filled as the tree is written and turned into the parent tree afterwards,
	// because an element's reference does not exist until it is written.
	owners := newMCIDOwners()
	kids, err := d.writeStructLevel(root, rootRef, nil, owners, ns)
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

// structureCheck validates the tree before anything is written, so a rejected
// tree leaves no half-written objects behind — including the checks that
// depend on the whole tree, such as an identifier claimed twice.
type structureCheck struct {
	roleMap map[string]string
	// pdf20 is whether the document is PDF 2.0, which is what makes the PDF
	// 2.0 structure namespace available; usesPDF20 whether an element needs it.
	pdf20, usesPDF20 bool
	// claimed is, per page object number, the identifiers already owned.
	claimed map[int]map[int]bool
}

// elems carries the inherited page down exactly as the writer does, because an
// element's page may come from an ancestor: a section states the page once and
// the paragraphs inside it do not repeat it. Checking without that inheritance
// would reject the ordinary shape of a tree.
func (c *structureCheck) elems(elems []StructElem, depth int, inheritedPage *object.IndirectRef) error {
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
		switch {
		case pdf17StructureTypes[e.Tag]:
		case isPDF20OnlyStructureType(e.Tag):
			if !c.pdf20 {
				return fmt.Errorf(
					"pdf0: %q is a PDF 2.0 structure type (ISO 32000-2 Annex M), and this is not a PDF 2.0 "+
						"document: without the PDF 2.0 namespace it is a non-standard type; "+
						"map a tag of your own onto a PDF 1.7 type instead", e.Tag)
			}
			c.usesPDF20 = true
		default:
			if _, mapped := c.roleMap[e.Tag]; !mapped {
				return fmt.Errorf(
					"pdf0: %q is not a standard structure type and the role map does not say what it is; "+
						"a reader would treat the element as meaningless", e.Tag)
			}
		}
		if len(e.Content) > 0 && page == nil {
			return fmt.Errorf(
				"pdf0: structure element %q names marked content but no page, and no ancestor states one; "+
					"an identifier is only unique within one page", e.Tag)
		}
		for _, mcid := range e.Content {
			if mcid < 0 || mcid > content.MaxMCID {
				return fmt.Errorf("pdf0: structure element %q names marked-content identifier %d, outside [0, %d]; "+
					"a page's parent-tree entry is an array as long as its largest identifier",
					e.Tag, mcid, content.MaxMCID)
			}
			on := c.claimed[page.Number]
			if on == nil {
				on = map[int]bool{}
				c.claimed[page.Number] = on
			}
			if on[mcid] {
				return fmt.Errorf(
					"pdf0: marked-content identifier %d on page %d is claimed twice; "+
						"the second claim is by %q, and an identifier belongs to one element",
					mcid, page.Number, e.Tag)
			}
			on[mcid] = true
		}
		if err := c.elems(e.Children, depth+1, page); err != nil {
			return err
		}
	}
	return nil
}

// roles checks the role map: every role lands on a type of the default
// namespace, which is the one a root /RoleMap maps into.
func (c *structureCheck) roles() error {
	for _, from := range slices.Sorted(maps.Keys(c.roleMap)) {
		to := c.roleMap[from]
		if !pdf17StructureTypes[to] {
			if isPDF20OnlyStructureType(to) {
				return fmt.Errorf("pdf0: the role map sends %q to %q, a PDF 2.0 structure type; "+
					"the tree root's /RoleMap maps into the PDF 1.7 namespace, where %q does not exist", from, to, to)
			}
			return fmt.Errorf("pdf0: the role map sends %q to %q, which is not a standard structure type", from, to)
		}
	}
	return nil
}

// checkStructurePages checks that every page an element names is a page of
// this document (audit 2026-09-22 C136): /Pg on the element and /StructParents
// on the page are both written from it, so a reference to anything else would
// put a page-only key on whatever object it happens to name. checkStructure has
// already bounded the depth.
func (d *Document) checkStructurePages(elems []StructElem) error {
	for _, e := range elems {
		if e.Page != nil {
			if err := d.requirePage(*e.Page, fmt.Sprintf("structure element %q", e.Tag)); err != nil {
				return err
			}
		}
		if err := d.checkStructurePages(e.Children); err != nil {
			return err
		}
	}
	return nil
}

// writeStructLevel writes one level of the tree and returns what its parent's
// /K should hold.
func (d *Document) writeStructLevel(elems []StructElem, parent object.IndirectRef,
	inheritedPage *object.IndirectRef, owners *mcidOwners, ns *object.IndirectRef) (object.Object, error) {

	out := make(object.Array, 0, len(elems))
	for _, e := range elems {
		dict := &object.Dictionary{}
		dict.Set("Type", object.Name("StructElem"))
		dict.Set("S", object.Name(e.Tag))
		dict.Set("P", parent)
		if !pdf17StructureTypes[e.Tag] && isPDF20OnlyStructureType(e.Tag) {
			dict.Set("NS", *ns)
		}

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
			childKids, err := d.writeStructLevel(e.Children, ref, page, owners, ns)
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

// pdf17StructureTypes is the standard structure namespace for PDF 1.7, the
// default namespace: the types an element with no /NS may have and a reader is
// required to understand (ISO 32000-1 14.8.4, and ISO 32000-2 Annex M).
// Anything else needs a role map saying which of these it behaves like, or —
// in a PDF 2.0 document — is one of the types only the PDF 2.0 namespace has.
var pdf17StructureTypes = map[string]bool{
	// Grouping
	"Document": true, "Part": true, "Sect": true,
	"Div": true, "NonStruct": true, "Private": true,
	"Art": true, "BlockQuote": true, "Caption": true, "TOC": true, "TOCI": true,
	"Index": true,

	// Block-level
	"P": true, "H": true,
	"H1": true, "H2": true, "H3": true, "H4": true, "H5": true, "H6": true,
	"L": true, "LI": true, "Lbl": true, "LBody": true,
	"Table": true, "TR": true, "TH": true, "TD": true,
	"THead": true, "TBody": true, "TFoot": true,

	// Inline
	"Span": true, "Quote": true, "Note": true, "Reference": true,
	"BibEntry": true, "Code": true, "Link": true, "Annot": true,
	"Ruby": true, "RB": true, "RT": true, "RP": true,
	"Warichu": true, "WT": true, "WP": true,

	// Illustration
	"Figure": true, "Formula": true, "Form": true,
}

// pdf20StructureNamespace is the namespace name of the PDF 2.0 standard
// structure namespace (ISO 32000-2 14.8.6.1).
const pdf20StructureNamespace = "http://iso.org/pdf2/ssn"

// pdf20OnlyStructureTypes are the types only the PDF 2.0 standard structure
// namespace defines (ISO 32000-2 Annex M), apart from Hn for n > 6, which
// isPDF20OnlyStructureType recognises. Artifact is content that is not part of
// the document's meaning — a running header, a page number, a decorative rule.
var pdf20OnlyStructureTypes = map[string]bool{
	"DocumentFragment": true, "Aside": true, "Title": true, "FENote": true,
	"Sub": true, "Em": true, "Strong": true, "Artifact": true,
}

// isPDF20OnlyStructureType reports whether tag exists only in the PDF 2.0
// standard structure namespace: an Annex M type, or Hn with n > 6.
func isPDF20OnlyStructureType(tag string) bool {
	if pdf20OnlyStructureTypes[tag] {
		return true
	}
	if len(tag) < 2 || tag[0] != 'H' || tag[1] == '0' {
		return false
	}
	n, err := strconv.Atoi(tag[1:])
	return err == nil && n > 6 && strconv.Itoa(n) == tag[1:]
}

// clearStructureIndices removes every key into the parent tree: /StructParents
// on each page and on each XObject its resources reach, and /StructParent on
// each of its annotations. It runs before a tree is replaced or removed, so
// that nothing indexes a tree that is gone.
func (d *Document) clearStructureIndices() {
	seen := map[*object.Dictionary]bool{}
	for _, page := range d.PageList() {
		page.Delete("StructParents")
		if annots, ok := d.Resolve(page.Get("Annots")).(object.Array); ok {
			for _, a := range annots {
				if ad := d.ResolveDict(a); ad != nil {
					ad.Delete("StructParent")
				}
			}
		}
		d.clearXObjectStructParents(d.ResolveDict(page.Get("Resources")), 0, seen)
	}
}

// clearXObjectStructParents clears /StructParents on the XObjects a resource
// dictionary names, and on those their own resources name, to the depth a
// content stream can nest.
func (d *Document) clearXObjectStructParents(res *object.Dictionary, depth int, seen map[*object.Dictionary]bool) {
	if res == nil || depth > content.MaxNestingDepth || seen[res] {
		return
	}
	seen[res] = true
	xobjects := d.ResolveDict(res.Get("XObject"))
	if xobjects == nil {
		return
	}
	for v := range xobjects.Values() {
		if st, ok := d.Resolve(v).(*object.Stream); ok {
			st.Dict.Delete("StructParents")
			d.clearXObjectStructParents(d.ResolveDict(st.Dict.Get("Resources")), depth+1, seen)
		}
	}
}

// isPDF20 reports whether the document is PDF 2.0 or later: by its header, or
// by its catalog's /Version when that is later (ISO 32000-2 7.7.2).
func (d *Document) isPDF20() bool {
	v, ok := parseVersion(d.declaredVersion())
	return ok && v[0] >= 2
}

// declaredVersion is the PDF version the document declares: its header, or
// its catalog's /Version when that is later.
func (d *Document) declaredVersion() string {
	v := d.Version
	if cat := d.ResolveDict(d.Trailer.Get("Root")); cat != nil {
		if n, ok := d.Resolve(cat.Get("Version")).(object.Name); ok {
			v = maxVersion(v, string(n))
		}
	}
	return v
}
