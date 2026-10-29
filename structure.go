package pdf0

import (
	"fmt"
	"maps"
	"slices"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/internal/core"
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

	// ID names the element, unique in the tree, so that another can refer to
	// it: a table cell's Headers attribute lists the IDs of its header cells
	// (ISO 32000-2 14.8.5.7). It is written as the element's /ID and entered
	// in the tree root's /IDTree.
	ID string

	// Page is the page whose content stream carries this element's marks, and
	// whose /Annots holds its annotations. It is required when Kids name either,
	// and inherited by child elements that do not state their own. It must be
	// one of PageList's pages.
	Page *object.IndirectRef

	// Attributes are the element's attributes (ISO 32000-2 14.8.5), written as
	// one attribute dictionary per owner in the order the owners first appear.
	Attributes []StructAttr

	// Kids are what the element contains, in reading order: spans of marked
	// content on Page, child elements and annotations, interleaved as the
	// document reads them. A paragraph with an emphasised word in it is three
	// kids — the marks before the word, an Em element, the marks after —
	// because the order of the kids is the order a reader reads them in.
	Kids []StructKid
}

// StructKid is one thing a structure element contains: a span of marked
// content, a child element or an annotation. Make one with MCIDKid, ElemKid or
// AnnotKid; the zero StructKid is none of them, and SetStructureTree refuses it.
type StructKid struct {
	kind  structKidKind
	mcid  int
	elem  *StructElem
	annot object.IndirectRef // the annotation, or the form of a kidForm
}

type structKidKind uint8

const (
	kidNone structKidKind = iota
	kidMCID
	kidElem
	kidAnnot
	kidForm
)

// MCIDKid is the span of the element's page's content stream that
// content.Builder.BeginTagged opened with identifier mcid.
func MCIDKid(mcid int) StructKid { return StructKid{kind: kidMCID, mcid: mcid} }

// ElemKid is a child element.
func ElemKid(e StructElem) StructKid { return StructKid{kind: kidElem, elem: &e} }

// FormMCIDKid is the span of a form XObject's content stream that
// content.Builder.BeginTagged opened with identifier mcid: content drawn in a
// form, such as a transparency group, which the page draws with Do. It is
// written as a marked-content reference naming the form's stream (/MCR with
// /Stm, ISO 32000-2 14.7.5.2), and the form is given the /StructParents that
// leads a reader from its marks back to their elements. The form must be one
// the element's page draws; its identifiers are its own, apart from the page's.
func FormMCIDKid(form object.IndirectRef, mcid int) StructKid {
	return StructKid{kind: kidForm, mcid: mcid, annot: form}
}

// AnnotKid is an annotation on the element's page: a link's annotation is the
// kid of its Link element, beside the marks that draw the link's text (ISO
// 32000-2 14.7.5.3). It is written as an object reference (/OBJR), and the
// annotation is given the /StructParent that leads a reader from it back to
// the element. The annotation must be in the page's /Annots.
func AnnotKid(annot object.IndirectRef) StructKid { return StructKid{kind: kidAnnot, annot: annot} }

// StructAttr is one attribute of a structure element: Key, with Value, in the
// attribute dictionary Owner says how to read (ISO 32000-2 14.8.5): "Table"
// for a cell's Scope, Headers, RowSpan and ColSpan, "List" for a list's
// ListNumbering, "Layout" and "PrintField" for theirs, and under PDF 2.0
// "Artifact". A Table attribute named Headers is an array of strings, each
// the ID of an element in the same tree.
type StructAttr struct {
	Owner string
	Key   string
	Value object.Object
}

// structAttrOwners are the attribute owners SetStructureTree writes: the
// standard ones whose attributes describe structure (ISO 32000-2 Table 376),
// with whether they need PDF 2.0. The format-specific owners (XML-1.00,
// HTML-*, CSS-*, …) and UserProperties are not offered.
var structAttrOwners = map[string]bool{
	"Layout": false, "List": false, "PrintField": false, "Table": false,
	"Artifact": true,
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
// In a document that declares PDF/UA-2 (DeclarePDFUA), a top-level Document
// element is written in the PDF 2.0 standard structure namespace, which
// ISO 14289-2 8.2.5.2 requires of it; everywhere else Document, like every
// type both standard namespaces define, is in the default one.
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
	check := structureCheck{roleMap: roleMap, pdf20: d.isPDF20(), claimed: map[int]map[int]bool{},
		annots: map[int]bool{}, ids: map[string]bool{}, formClaimed: map[int]map[int]bool{}}
	if err := check.elems(root, 0, nil); err != nil {
		return err
	}
	if err := check.references(); err != nil {
		return err
	}
	if err := check.roles(); err != nil {
		return err
	}
	if err := d.checkStructurePages(root, nil); err != nil {
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
			object.Entry{Key: "NS", Value: object.String{Value: []byte(core.NSPDF20)}},
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
		// In key order: map iteration order must not reach the file (C95).
		rm := &object.Dictionary{}
		for _, from := range slices.Sorted(maps.Keys(roleMap)) {
			rm.Set(object.Name(from), object.Name(roleMap[from]))
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
	if d.declaresPDFUA2() {
		// The declaration may have come first (DeclarePDFUA).
		d.placeDocumentInPDF20Namespace(catalog)
	}
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
	// annots is the annotations already owned, by object number.
	annots map[int]bool
	// formClaimed is, per form object number, the identifiers already owned.
	formClaimed map[int]map[int]bool
	// ids is the element IDs, and headers the IDs Headers attributes name,
	// with the tag of the element naming each, checked against ids once the
	// whole tree is seen.
	ids     map[string]bool
	headers []namedID
}

type namedID struct{ id, by string }

// elems carries the inherited page down exactly as the writer does, because an
// element's page may come from an ancestor: a section states the page once and
// the paragraphs inside it do not repeat it. Checking without that inheritance
// would reject the ordinary shape of a tree.
func (c *structureCheck) elems(elems []StructElem, depth int, inheritedPage *object.IndirectRef) error {
	if depth > maxStructureDepth {
		return fmt.Errorf("pdf0: the structure tree is nested more than %d deep", maxStructureDepth)
	}
	for i := range elems {
		e := &elems[i]
		if e.Tag == "" {
			return fmt.Errorf("pdf0: a structure element has no tag")
		}
		page := e.Page
		if page == nil {
			page = inheritedPage
		}
		switch {
		case core.StandardStructTypes[object.Name(e.Tag)]:
		case core.IsPDF20OnlyStructType(object.Name(e.Tag)):
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
		if e.ID != "" {
			if c.ids[e.ID] {
				return fmt.Errorf("pdf0: structure element ID %q is given twice; an ID names one element", e.ID)
			}
			c.ids[e.ID] = true
		}
		if err := c.attributes(e); err != nil {
			return err
		}
		var children []StructElem
		for _, k := range e.Kids {
			switch k.kind {
			case kidMCID:
				if err := c.mcid(e.Tag, page, k.mcid); err != nil {
					return err
				}
			case kidAnnot:
				if page == nil {
					return fmt.Errorf(
						"pdf0: structure element %q names an annotation but no page, and no ancestor states one; "+
							"an annotation is the kid of an element on its page", e.Tag)
				}
				if c.annots[k.annot.Number] {
					return fmt.Errorf("pdf0: annotation %d is the kid of two structure elements; "+
						"the second is %q, and an annotation has one parent", k.annot.Number, e.Tag)
				}
				c.annots[k.annot.Number] = true
			case kidForm:
				if err := c.formMCID(e.Tag, page, k.annot, k.mcid); err != nil {
					return err
				}
			case kidElem:
				children = append(children, *k.elem)
			default:
				return fmt.Errorf("pdf0: structure element %q has a kid that is none of MCIDKid, FormMCIDKid, ElemKid and AnnotKid", e.Tag)
			}
		}
		if err := c.elems(children, depth+1, page); err != nil {
			return err
		}
	}
	return nil
}

// mcid checks one marked-content identifier an element names.
func (c *structureCheck) mcid(tag string, page *object.IndirectRef, mcid int) error {
	if page == nil {
		return fmt.Errorf(
			"pdf0: structure element %q names marked content but no page, and no ancestor states one; "+
				"an identifier is only unique within one page", tag)
	}
	if mcid < 0 || mcid > content.MaxMCID {
		return fmt.Errorf("pdf0: structure element %q names marked-content identifier %d, outside [0, %d]; "+
			"a page's parent-tree entry is an array as long as its largest identifier",
			tag, mcid, content.MaxMCID)
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
			mcid, page.Number, tag)
	}
	on[mcid] = true
	return nil
}

// formMCID checks one marked-content identifier in a form an element names.
func (c *structureCheck) formMCID(tag string, page *object.IndirectRef, form object.IndirectRef, mcid int) error {
	if page == nil {
		return fmt.Errorf(
			"pdf0: structure element %q names marked content in form %d but no page, and no ancestor states one; "+
				"a form's marks are on the page that draws it", tag, form.Number)
	}
	if mcid < 0 || mcid > content.MaxMCID {
		return fmt.Errorf("pdf0: structure element %q names marked-content identifier %d in form %d, outside [0, %d]",
			tag, mcid, form.Number, content.MaxMCID)
	}
	on := c.formClaimed[form.Number]
	if on == nil {
		on = map[int]bool{}
		c.formClaimed[form.Number] = on
	}
	if on[mcid] {
		return fmt.Errorf("pdf0: marked-content identifier %d in form %d is claimed twice; the second claim is by %q",
			mcid, form.Number, tag)
	}
	on[mcid] = true
	return nil
}

// attributes checks an element's attributes: a known owner, a key, a value,
// and a Table Headers that is a list of IDs.
func (c *structureCheck) attributes(e *StructElem) error {
	for _, a := range e.Attributes {
		needs20, known := structAttrOwners[a.Owner]
		if !known {
			return fmt.Errorf("pdf0: structure element %q has an attribute owned by %q, which is not one of "+
				"Layout, List, PrintField, Table and Artifact", e.Tag, a.Owner)
		}
		if needs20 && !c.pdf20 {
			return fmt.Errorf("pdf0: structure element %q has an attribute owned by %q, which PDF 2.0 added; "+
				"this is not a PDF 2.0 document", e.Tag, a.Owner)
		}
		if a.Key == "" || a.Value == nil {
			return fmt.Errorf("pdf0: structure element %q has a %s attribute with no key or no value", e.Tag, a.Owner)
		}
		if a.Owner == "Table" && a.Key == "Headers" {
			arr, ok := a.Value.(object.Array)
			if !ok {
				return fmt.Errorf("pdf0: structure element %q has a Headers attribute that is not an array of IDs", e.Tag)
			}
			for _, v := range arr {
				id, ok := v.(object.String)
				if !ok {
					return fmt.Errorf("pdf0: structure element %q has a Headers attribute that is not an array of IDs", e.Tag)
				}
				c.headers = append(c.headers, namedID{id: string(id.Value), by: e.Tag})
			}
		}
	}
	return nil
}

// references checks, once every ID is known, that each one a Headers
// attribute names is an element's.
func (c *structureCheck) references() error {
	for _, h := range c.headers {
		if !c.ids[h.id] {
			return fmt.Errorf("pdf0: structure element %q lists header %q, which is no element's ID", h.by, h.id)
		}
	}
	return nil
}

// roles checks the role map: every role lands on a type of the default
// namespace, which is the one a root /RoleMap maps into.
func (c *structureCheck) roles() error {
	for _, from := range slices.Sorted(maps.Keys(c.roleMap)) {
		to := c.roleMap[from]
		if !core.StandardStructTypes[object.Name(to)] {
			if core.IsPDF20OnlyStructType(object.Name(to)) {
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
// put a page-only key on whatever object it happens to name. An annotation kid
// must be in its page's /Annots, which is what makes it that page's. The
// structure check (structureCheck.elems) has already bounded the depth and
// required a page wherever an annotation is named.
func (d *Document) checkStructurePages(elems []StructElem, inheritedPage *object.IndirectRef) error {
	for _, e := range elems {
		page := e.Page
		if page == nil {
			page = inheritedPage
		}
		if e.Page != nil {
			if err := d.requirePage(*e.Page, fmt.Sprintf("structure element %q", e.Tag)); err != nil {
				return err
			}
		}
		var children []StructElem
		for _, k := range e.Kids {
			switch k.kind {
			case kidElem:
				children = append(children, *k.elem)
			case kidAnnot:
				if !d.pageHasAnnot(*page, k.annot) {
					return fmt.Errorf("pdf0: structure element %q names annotation %d, which is not in the /Annots of page %d",
						e.Tag, k.annot.Number, page.Number)
				}
			case kidForm:
				st, ok := d.Resolve(k.annot).(*object.Stream)
				if !ok || st.Dict.Get("Subtype") != object.Name("Form") {
					return fmt.Errorf("pdf0: structure element %q names marked content in object %d, which is not a form XObject",
						e.Tag, k.annot.Number)
				}
			}
		}
		if err := d.checkStructurePages(children, page); err != nil {
			return err
		}
	}
	return nil
}

// pageHasAnnot reports whether annot is in the page's /Annots, by reference.
func (d *Document) pageHasAnnot(page, annot object.IndirectRef) bool {
	pd := d.ResolveDict(page)
	if pd == nil {
		return false
	}
	arr, _ := d.Resolve(pd.Get("Annots")).(object.Array)
	for _, v := range arr {
		if r, ok := v.(object.IndirectRef); ok && r.Number == annot.Number && r.Generation == annot.Generation {
			return d.ResolveDict(r) != nil
		}
	}
	return false
}

// writeStructLevel writes one level of the tree and returns what its parent's
// /K should hold.
func (d *Document) writeStructLevel(elems []StructElem, parent object.IndirectRef,
	inheritedPage *object.IndirectRef, owners *mcidOwners, ns *object.IndirectRef) (object.Object, error) {

	out := make(object.Array, 0, len(elems))
	for i := range elems {
		ref, err := d.writeStructElem(&elems[i], parent, inheritedPage, owners, ns)
		if err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	if len(out) == 1 {
		return out[0], nil
	}
	return out, nil
}

// writeStructElem writes one element and, in reading order, its kids.
func (d *Document) writeStructElem(e *StructElem, parent object.IndirectRef,
	inheritedPage *object.IndirectRef, owners *mcidOwners, ns *object.IndirectRef) (object.IndirectRef, error) {

	dict := &object.Dictionary{}
	dict.Set("Type", object.Name("StructElem"))
	dict.Set("S", object.Name(e.Tag))
	dict.Set("P", parent)
	if !core.StandardStructTypes[object.Name(e.Tag)] && core.IsPDF20OnlyStructType(object.Name(e.Tag)) {
		dict.Set("NS", *ns)
	}

	page := e.Page
	if page == nil {
		page = inheritedPage
	}
	if page != nil {
		dict.Set("Pg", *page)
	}
	if e.ID != "" {
		dict.Set("ID", object.String{Value: []byte(e.ID)})
	}
	if a := structAttributes(e.Attributes); a != nil {
		dict.Set("A", a)
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
	if e.ID != "" {
		owners.ids[e.ID] = ref
	}

	// The kids in the order given, which is the reading order, and reading
	// order is the whole point of the tree.
	kids := make(object.Array, 0, len(e.Kids))
	for _, k := range e.Kids {
		switch k.kind {
		case kidMCID:
			if err := owners.claim(*page, k.mcid, ref, e.Tag); err != nil {
				return ref, err
			}
			kids = append(kids, object.Integer(k.mcid))
		case kidElem:
			child, err := d.writeStructElem(k.elem, ref, page, owners, ns)
			if err != nil {
				return ref, err
			}
			kids = append(kids, child)
		case kidForm:
			if err := owners.claimForm(k.annot, k.mcid, ref); err != nil {
				return ref, err
			}
			kids = append(kids, object.NewDictionary(
				object.Entry{Key: "Type", Value: object.Name("MCR")},
				object.Entry{Key: "Pg", Value: *page},
				object.Entry{Key: "Stm", Value: k.annot},
				object.Entry{Key: "MCID", Value: object.Integer(k.mcid)},
			))
		case kidAnnot:
			// An object reference names the page it is on, which is the
			// element's own; /Pg is optional then and written anyway, so
			// that the reference reads alone.
			objr := object.NewDictionary(
				object.Entry{Key: "Type", Value: object.Name("OBJR")},
				object.Entry{Key: "Pg", Value: *page},
				object.Entry{Key: "Obj", Value: k.annot},
			)
			owners.annots = append(owners.annots, annotOwner{annot: k.annot, owner: ref, page: *page})
			kids = append(kids, d.Add(objr))
		}
	}
	switch len(kids) {
	case 0:
		// No /K at all: an element with no kids is legal and describes a
		// structure that is there but empty.
	case 1:
		// A single kid is written directly rather than in a one-element
		// array, which is what a reader expects to see.
		dict.Set("K", kids[0])
	default:
		dict.Set("K", kids)
	}
	return ref, nil
}

// structAttributes is an element's /A: one attribute dictionary per owner, in
// the order the owners first appear, a single one written alone.
func structAttributes(attrs []StructAttr) object.Object {
	var dicts []*object.Dictionary
	byOwner := map[string]*object.Dictionary{}
	for _, a := range attrs {
		dict, ok := byOwner[a.Owner]
		if !ok {
			dict = object.NewDictionary(object.Entry{Key: "O", Value: object.Name(a.Owner)})
			byOwner[a.Owner] = dict
			dicts = append(dicts, dict)
		}
		dict.Set(object.Name(a.Key), a.Value)
	}
	switch len(dicts) {
	case 0:
		return nil
	case 1:
		return dicts[0]
	}
	arr := make(object.Array, len(dicts))
	for i, dict := range dicts {
		arr[i] = dict
	}
	return arr
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
	// annots is each annotation kid and the element that owns it, in the
	// order the tree names them.
	annots []annotOwner
	// ids is each element ID and its element.
	ids map[string]object.IndirectRef
	// byForm, formRefs and formOrder are byPage, pageRefs and order for the
	// forms whose marks the tree names.
	byForm    map[int]map[int]object.IndirectRef
	formRefs  map[int]object.IndirectRef
	formOrder []int
}

// claimForm records that an element owns an identifier in a form. The
// structure check has refused a second claim already.
func (o *mcidOwners) claimForm(form object.IndirectRef, mcid int, owner object.IndirectRef) error {
	on, seen := o.byForm[form.Number]
	if !seen {
		on = map[int]object.IndirectRef{}
		o.byForm[form.Number] = on
		o.formRefs[form.Number] = form
		o.formOrder = append(o.formOrder, form.Number)
	}
	if _, taken := on[mcid]; taken {
		return fmt.Errorf("pdf0: marked-content identifier %d in form %d is claimed twice", mcid, form.Number)
	}
	on[mcid] = owner
	return nil
}

type annotOwner struct{ annot, owner, page object.IndirectRef }

func newMCIDOwners() *mcidOwners {
	return &mcidOwners{
		byPage:   map[int]map[int]object.IndirectRef{},
		pageRefs: map[int]object.IndirectRef{},
		ids:      map[string]object.IndirectRef{},
		byForm:   map[int]map[int]object.IndirectRef{},
		formRefs: map[int]object.IndirectRef{},
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
		nums = append(nums, object.Integer(index), d.Add(denseOwners(owners.byPage[pageNum])))
	}
	// A form's entry is an array by identifier, as a page's is, keyed by the
	// form's /StructParents after the pages' keys.
	next := len(owners.order)
	for _, formNum := range owners.formOrder {
		form, ok := d.Resolve(owners.formRefs[formNum]).(*object.Stream)
		if !ok {
			return fmt.Errorf("pdf0: the structure tree names object %d as a form, and no such form exists", formNum)
		}
		form.Dict.Set("StructParents", object.Integer(next))
		nums = append(nums, object.Integer(next), d.Add(denseOwners(owners.byForm[formNum])))
		next++
	}
	// An annotation's entry is the element itself, keyed by the annotation's
	// /StructParent (ISO 32000-2 14.7.5.4), after the pages' and forms' keys.
	for _, ao := range owners.annots {
		annot := d.ResolveDict(ao.annot)
		if annot == nil {
			return fmt.Errorf("pdf0: the structure tree names object %d as an annotation, and no such annotation exists", ao.annot.Number)
		}
		annot.Set("StructParent", object.Integer(next))
		nums = append(nums, object.Integer(next), ao.owner)
		next++
		// Tabbing through a page whose annotations are in the tree goes in
		// the tree's order (ISO 32000-2 12.5, /Tabs /S), which is the order the
		// tree reads them in; PDF/UA requires it of every page with
		// annotations (ISO 14289-1 7.18.3).
		if page := d.ResolveDict(ao.page); page != nil {
			page.Set("Tabs", object.Name("S"))
		}
	}
	parentTree := &object.Dictionary{}
	parentTree.Set("Nums", nums)
	treeRoot.Set("ParentTree", d.Add(parentTree))
	treeRoot.Set("ParentTreeNextKey", object.Integer(next))
	if len(owners.ids) > 0 {
		// A name tree's keys are in byte order (ISO 32000-2 7.9.6).
		names := object.Array{}
		for _, id := range slices.Sorted(maps.Keys(owners.ids)) {
			names = append(names, object.String{Value: []byte(id)}, owners.ids[id])
		}
		treeRoot.Set("IDTree", d.Add(object.NewDictionary(object.Entry{Key: "Names", Value: names})))
	}
	return nil
}

// denseOwners is a stream's parent-tree entry: an array indexed by
// identifier, a gap a null rather than a shorter array, because the position
// is the identifier.
func denseOwners(on map[int]object.IndirectRef) object.Array {
	highest := -1
	for mcid := range on {
		highest = max(highest, mcid)
	}
	elems := make(object.Array, highest+1)
	for i := range elems {
		elems[i] = object.Null{}
	}
	for mcid, owner := range on {
		elems[mcid] = owner
	}
	return elems
}

// The builder and the validators read one structure vocabulary: the PDF 1.7
// standard types (core.StandardStructTypes, the default namespace, which an
// element with no /NS is in), the types only the PDF 2.0 namespace has
// (core.IsPDF20OnlyStructType, ISO 32000-2 Annex M) and that namespace's name
// (core.NSPDF20). They were written twice, once here and once in core, and
// the validator's copy did not know the PDF 2.0 namespace at all.

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
	maj, _, ok := core.ParsePDFVersion(d.declaredVersion())
	return ok && maj >= 2
}

// declaredVersion is the PDF version the document declares: its header, or
// its catalog's /Version when that is later.
func (d *Document) declaredVersion() string { return d.view().DeclaredVersion() }
