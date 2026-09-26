package pdf0

import (
	"bytes"
	"fmt"
	"slices"
	"strconv"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// The page importer: the one mechanism that copies pages from one document into
// another, behind ExtractPages and AppendPages.
//
// A page is not "its reachable object graph minus /Parent". Copying it that way
// (the previous implementation) got six things wrong at once (audit 2026-09-22
// T4: C29, C30, C31, C89, C90, C129), because a page is a node in several
// structures at the same time, and the graph edges that connect those
// structures lead everywhere:
//
//   - A page takes attributes from its ancestors (ISO 32000-2 7.7.3.4). Cut off
//     from them it loses its size, its fonts and its rotation. The importer
//     materialises the four inheritable attributes on the copy.
//   - Annotations point back at pages (/P), links name other pages (/Dest,
//     GoTo actions), article beads chain through every page of an article (/B),
//     widgets hang from form fields whose /Kids reach widgets on other pages,
//     and structure elements name pages (/Pg). Following those copied the whole
//     source. The importer never copies a page-tree node it was not asked for:
//     a reference to one is remapped to the imported copy when the page was
//     imported, and treated as dangling otherwise.
//   - A page asked for twice is two pages, each with its own dictionary and its
//     own annotations; the content and resources they draw with are shared.
//   - A page held inline in its parent's /Kids is still a page.
//   - A Locked source holds ciphertext. Copying it into a document that is
//     written in the clear produces noise that nothing reports.
//
// What happens to the parts of a document that are not pages is a policy, and
// every part the importer does not carry is named in the returned ImportReport:
//
//   - Destinations to imported pages are rewritten to name the copy. A named
//     destination is resolved in the source and written out explicitly, so the
//     source's name table is not needed. A destination to a page that was not
//     imported cannot be followed: a link annotation whose only target it was
//     is removed, a GoTo action is removed from wherever it was (a widget's /A,
//     an /AA entry, a /Next chain, with the rest of its own /Next chain), and
//     both are counted in DestinationsDropped. Thread and GoToDp actions are
//     treated the same way, since articles and document parts are not carried.
//   - Form fields are carried: every widget on an imported page keeps its field
//     and the field's ancestors, with /Kids pruned to what was imported, and
//     the top-level fields join the target's /AcroForm. A field whose name the
//     target already uses is renamed (FieldsRenamed), since two fields with one
//     name are one field to a reader. An /XFA form is not carried.
//   - The logical structure tree is not carried: the imported pages are
//     untagged, and the /StructParent(s) keys that index into the source's
//     parent tree are removed so they cannot index into the target's.
//   - Optional content (/OCProperties) is carried, so a layer hidden in the
//     source stays hidden: into a document with none, as it was; into one with
//     its own, the imported groups are added with their default state.
//   - ExtractPages also carries /OutputIntents and /Lang, which describe the
//     pages rather than the document. AppendPages leaves the target's own in
//     force and reports the source's as omitted.
//   - Everything else in the source's catalog (outline, named-destination
//     tables, page labels, articles, open action, metadata, …) and its /Info is
//     reported as omitted.

// ImportReport describes what a page import did beyond copying pages, and what
// it could not carry.
type ImportReport struct {
	// Pages is the number of page dictionaries written into the target.
	Pages int

	// DestinationsRemapped counts destinations that named an imported page and
	// now name its copy (the first copy, for a page imported more than once).
	DestinationsRemapped int

	// DestinationsDropped counts link annotations and actions removed because
	// the page they led to was not imported.
	DestinationsDropped int

	// FieldsRenamed lists the top-level form fields renamed because the target
	// already had a field of that name.
	FieldsRenamed []FieldRename

	// Omitted lists the parts of the source that were not carried, each once.
	Omitted []Omission
}

// FieldRename is one form field renamed on import.
type FieldRename struct {
	From, To string
}

// Omission is one part of the source document that a page import did not
// carry, and why.
type Omission struct {
	// Entry names the part: a catalog key ("Outlines"), a page key ("B"), or
	// an entry inside one ("AcroForm/XFA").
	Entry string
	// Reason says what is lost with it.
	Reason string
}

func (r *ImportReport) omit(entry, reason string) {
	for _, o := range r.Omitted {
		if o.Entry == entry {
			return
		}
	}
	r.Omitted = append(r.Omitted, Omission{Entry: entry, Reason: reason})
}

// checkImportSource refuses a source whose pages cannot be copied faithfully.
func checkImportSource(src *Document) error {
	if src == nil {
		return fmt.Errorf("pdf0: the source document is nil")
	}
	if src.Locked() {
		return fmt.Errorf("pdf0: the source document is encrypted and was not decrypted (Locked), "+
			"so its pages are ciphertext; read it with the password first: %w", src.LockReason())
	}
	if err := src.missingObjectsErr("pdf0: the source document"); err != nil {
		return err
	}
	if len(src.decryptFailures) > 0 {
		return fmt.Errorf("pdf0: object(s) %v of the source could not be decrypted on read, "+
			"so their content is missing", src.decryptFailures)
	}
	return nil
}

// boundKey identifies the copy of a page-bound object — a page, an annotation,
// a form field — for one repetition of its page. A page imported twice gets
// ordinal 0 the first time and 1 the second, and everything bound to it is
// copied once per ordinal.
type boundKey struct{ num, ord int }

// pageInstance is one page to import: which source page, which repetition of
// it, and the number its copy will have.
type pageInstance struct {
	src core.PageWithAttrs
	ord int
	dst object.IndirectRef
}

type pageImporter struct {
	src, dst *Document
	sg       core.View
	report   *ImportReport

	// shared maps each source object copied once for the whole import (content,
	// resources, fonts, appearance streams, actions) to its copy.
	shared map[int]object.IndirectRef
	// bound maps a page-bound source object and an ordinal to its copy.
	bound map[boundKey]object.IndirectRef
	// promoted holds the copy of each direct inherited /Resources dictionary,
	// made indirect so the pages that inherited it share one copy.
	promoted map[*object.Dictionary]object.IndirectRef

	catalogNum int
	pageNums   map[int]bool               // every page of the source, by number
	firstCopy  map[int]object.IndirectRef // an imported source page → its first copy
	srcPages   []core.PageWithAttrs

	// Page-bound objects: annotations listed on an imported page, and the
	// form-field nodes above the widgets among them.
	boundAnnots map[int]bool
	fieldNodes  map[int]bool
	annotOwner  map[boundKey]object.IndirectRef // annotation → the copy of the page listing it
	keep        map[boundKey]bool               // fields and widgets kept, per ordinal
	fieldRoots  []boundKey                      // top-level fields, in page order

	droppedRef map[int]bool // memo: does this source object drop out?
	pending    []pendingCopy
	named      map[string]object.Object
	ord        int
}

func newPageImporter(src, dst *Document, report *ImportReport) *pageImporter {
	g := &pageImporter{
		src: src, dst: dst, sg: src.graph(), report: report,
		shared:      map[int]object.IndirectRef{},
		bound:       map[boundKey]object.IndirectRef{},
		promoted:    map[*object.Dictionary]object.IndirectRef{},
		pageNums:    map[int]bool{},
		firstCopy:   map[int]object.IndirectRef{},
		boundAnnots: map[int]bool{},
		fieldNodes:  map[int]bool{},
		annotOwner:  map[boundKey]object.IndirectRef{},
		keep:        map[boundKey]bool{},
		droppedRef:  map[int]bool{},
	}
	g.catalogNum = object.RefNum(src.Trailer.Get("Root"))
	g.srcPages = g.sg.PagesWithAttrs(g.sg.CatalogPages())
	for _, p := range g.srcPages {
		if p.ObjNum != 0 {
			g.pageNums[p.ObjNum] = true
		}
	}
	return g
}

// importPages copies the source pages at indices, in order, into dst and hangs
// them at the end of dst's page tree. fresh says dst is a new document made for
// this import, which is what lets it take the source's page-describing
// document-level entries.
func importPages(src, dst *Document, indices []int, fresh bool) (ImportReport, error) {
	var report ImportReport
	if err := checkImportSource(src); err != nil {
		return report, err
	}
	if dst.Locked() {
		return report, errLockedTarget("importing pages")
	}
	// Validate (and, for a direct root, repair) the target before anything is
	// copied, so a refusal leaves it as it was.
	if _, _, err := dst.pageTreeRoot(); err != nil {
		return report, err
	}
	g := newPageImporter(src, dst, &report)
	for _, idx := range indices {
		if idx < 0 || idx >= len(g.srcPages) {
			return report, errPageOutOfRange(idx, len(g.srcPages))
		}
	}

	// Every copy gets its number first, so a destination on one imported page
	// can name another however they are ordered.
	seen := map[int]int{}
	insts := make([]pageInstance, len(indices))
	for i, idx := range indices {
		insts[i] = pageInstance{src: g.srcPages[idx], ord: seen[idx], dst: dst.Add(object.Null{})}
		seen[idx]++
		if n := insts[i].src.ObjNum; n != 0 && insts[i].ord == 0 {
			g.firstCopy[n] = insts[i].dst
		}
	}
	for _, in := range insts {
		g.collectBound(in)
	}
	refs := make([]object.IndirectRef, len(insts))
	for i, in := range insts {
		g.ord = in.ord
		dst.Objects[in.dst.Number].Value = g.copyPage(in)
		refs[i] = in.dst
		g.drain()
	}
	if err := dst.appendToPageTree(refs); err != nil {
		return report, err
	}
	report.Pages = len(refs)

	g.mergeAcroForm()
	g.carryCatalog(fresh)
	g.drain()
	dst.Version = maxVersion(dst.Version, g.sourceVersion())
	return report, nil
}

// collectBound records, before anything is copied, which annotations and form
// fields belong to the page instance: a field's /Kids is pruned to what was
// imported, and that set has to be known in full the first time the field is
// reached, whichever imported page reaches it.
func (g *pageImporter) collectBound(in pageInstance) {
	annots, _ := g.sg.Resolve(in.src.Dict.Get("Annots")).(object.Array)
	for _, a := range annots {
		ref, ok := a.(object.IndirectRef)
		if !ok {
			continue
		}
		key := boundKey{ref.Number, in.ord}
		g.boundAnnots[ref.Number] = true
		if _, owned := g.annotOwner[key]; !owned {
			g.annotOwner[key] = in.dst
		}
		annot := g.sg.ResolveDict(ref)
		if annot == nil {
			continue
		}
		sub, _ := g.sg.ResolveName(annot.Get("Subtype"))
		if sub != "Widget" {
			continue
		}
		g.keep[key] = true
		root := -1
		if isField(annot) && annot.Get("Parent") == nil {
			root = ref.Number // a field and its only widget in one dictionary
		}
		visited := map[int]bool{ref.Number: true}
		node := annot
		for hops := 0; hops < 64; hops++ {
			up, ok := node.Get("Parent").(object.IndirectRef)
			if !ok || visited[up.Number] {
				break
			}
			visited[up.Number] = true
			parent := g.sg.ResolveDict(up)
			if parent == nil {
				break
			}
			g.keep[boundKey{up.Number, in.ord}] = true
			g.fieldNodes[up.Number] = true
			root = up.Number
			node = parent
		}
		if root >= 0 {
			rk := boundKey{root, in.ord}
			if !slices.Contains(g.fieldRoots, rk) {
				g.fieldRoots = append(g.fieldRoots, rk)
			}
		}
	}
}

// isField reports whether a dictionary is a form field (as opposed to a bare
// widget): it has a field type or a partial name of its own.
func isField(d *object.Dictionary) bool {
	return d.Get("FT") != nil || d.Get("T") != nil
}

// copyPage builds the copy of one page dictionary.
func (g *pageImporter) copyPage(in pageInstance) *object.Dictionary {
	page := in.src.Dict
	out := &object.Dictionary{}
	for key, val := range page.All() {
		switch key {
		case "Parent", "Annots", "Resources", "MediaBox", "CropBox", "Rotate":
			continue // rebuilt below, or by appendToPageTree
		case "StructParents":
			g.omitStructure()
			continue
		case "B":
			g.report.omit("B", "article threads are not carried; the beads on imported pages were removed")
			continue
		case "DPart":
			g.report.omit("DPart", "document parts (PDF/VT) are not carried; the imported pages belong to none")
			continue
		}
		if c, ok := g.copyValue(val); ok {
			out.Set(key, c)
		}
	}
	for i, key := range core.InheritablePageAttrs {
		val := in.src.Attrs[i]
		if val == nil {
			switch key {
			case "Resources":
				out.Set(key, &object.Dictionary{})
			case "MediaBox":
				// Required, and absent from the page and every ancestor. Readers
				// fall back to US Letter; say so rather than leave the copy to
				// inherit whatever the target's tree states.
				out.Set(key, object.Array{object.Integer(0), object.Integer(0), object.Integer(612), object.Integer(792)})
				g.report.omit("MediaBox", "a source page had no /MediaBox of its own or inherited; US Letter, which readers assume, was written")
			}
			continue
		}
		if dict, direct := val.(*object.Dictionary); direct && page.Get(key) == nil {
			// Inherited as a direct dictionary: one indirect copy, shared by
			// every page that inherited it.
			ref, ok := g.promoted[dict]
			if !ok {
				ref = g.dst.Add(g.copyDict(dict, 0))
				g.promoted[dict] = ref
			}
			out.Set(key, ref)
			continue
		}
		if c, ok := g.copyValue(val); ok {
			out.Set(key, c)
		}
	}
	if annots, ok := g.sg.Resolve(page.Get("Annots")).(object.Array); ok {
		var copied object.Array
		for _, a := range annots {
			switch v := a.(type) {
			case object.IndirectRef:
				if c, ok := g.copyRef(v); ok {
					copied = append(copied, c)
				}
			case *object.Dictionary:
				if g.dropsOut(v) {
					g.report.DestinationsDropped++
					continue
				}
				c := g.copyDict(v, 0)
				if v.Get("P") != nil {
					c.Set("P", in.dst)
				}
				copied = append(copied, c)
			}
		}
		if len(copied) > 0 {
			out.Set("Annots", copied)
		}
	}
	return out
}

func (g *pageImporter) omitStructure() {
	g.report.omit("StructTreeRoot", "the logical structure tree is not carried; the imported pages are untagged, "+
		"and their /StructParent(s) entries were removed")
}

// isPageEdge reports whether a reference leads into the source's page tree (a
// page or a /Pages node) or to its catalog: the places the copier never
// enters.
func (g *pageImporter) isPageEdge(num int) bool {
	if g.pageNums[num] || num == g.catalogNum {
		return true
	}
	if d := g.sg.ResolveDict(object.IndirectRef{Number: num}); d != nil {
		typ, _ := g.sg.ResolveName(d.Get("Type"))
		return typ == "Page" || typ == "Pages" || typ == "Catalog"
	}
	return false
}

// copyRef copies the object ref names and returns a reference to the copy. It
// reports false when the reference must not be carried: it leads to a page
// that was not imported, or to a link or action that drops out.
func (g *pageImporter) copyRef(ref object.IndirectRef) (object.Object, bool) {
	num := ref.Number
	if g.isPageEdge(num) {
		if c, ok := g.firstCopy[num]; ok {
			return c, true
		}
		return nil, false
	}
	isBound := g.boundAnnots[num] || g.fieldNodes[num]
	key := boundKey{num, g.ord}
	if isBound {
		if c, ok := g.bound[key]; ok {
			return c, true
		}
	} else if c, ok := g.shared[num]; ok {
		return c, true
	}
	iobj := g.src.Objects[num]
	if iobj != nil {
		if d, ok := iobj.Value.(*object.Dictionary); ok && g.dropsOut(d) {
			g.report.DestinationsDropped++
			return nil, false
		}
	}
	c := g.dst.Add(object.Null{})
	if isBound {
		g.bound[key] = c
	} else {
		g.shared[num] = c
	}
	if iobj != nil {
		// Filled in by drain, not here: recursing from one indirect object into
		// the next would need stack in proportion to the longest chain of
		// references in the file, which the file chooses.
		g.pending = append(g.pending, pendingCopy{num: num, dst: c, ord: g.ord})
	}
	return c, true // a reference to nothing reads as null, as it did in the source
}

// pendingCopy is an indirect object whose copy has a number but no value yet.
type pendingCopy struct {
	num, ord int
	dst      object.IndirectRef
}

// drain fills in every copy that has been numbered, including the ones that
// filling in the others numbers, until none is left.
func (g *pageImporter) drain() {
	saved := g.ord
	defer func() { g.ord = saved }()
	for len(g.pending) > 0 {
		p := g.pending[len(g.pending)-1]
		g.pending = g.pending[:len(g.pending)-1]
		g.ord = p.ord
		src := g.src.Objects[p.num].Value
		var value object.Object
		if d, ok := src.(*object.Dictionary); ok {
			cd := g.copyDict(d, p.num)
			if g.boundAnnots[p.num] && d.Get("P") != nil {
				if owner, ok := g.annotOwner[boundKey{p.num, p.ord}]; ok {
					cd.Set("P", owner)
				}
			}
			value = cd
		} else if v, ok := g.copyValue(src); ok {
			value = v
		} else {
			value = object.Null{}
		}
		g.dst.Objects[p.dst.Number].Value = value
	}
}

// copyValue copies a direct value, recursing into containers.
func (g *pageImporter) copyValue(o object.Object) (object.Object, bool) {
	switch v := o.(type) {
	case object.IndirectRef:
		return g.copyRef(v)
	case *object.Dictionary:
		if g.dropsOut(v) {
			g.report.DestinationsDropped++
			return nil, false
		}
		return g.copyDict(v, 0), true
	case object.Array:
		cp := make(object.Array, len(v))
		for i := range v {
			c, ok := g.copyValue(v[i])
			if !ok {
				c = object.Null{}
			}
			cp[i] = c
		}
		return cp, true
	case *object.Stream:
		d := g.copyDict(&v.Dict, 0)
		return object.NewStream(d, append([]byte(nil), v.Data...)), true
	}
	return o, true // scalars are immutable
}

// copyDict copies a dictionary. num is its source object number, or 0 when it
// is direct.
func (g *pageImporter) copyDict(d *object.Dictionary, num int) *object.Dictionary {
	isGoTo := actionType(g.sg, d) == "GoTo"
	out := &object.Dictionary{}
	for key, val := range d.All() {
		switch {
		case key == "StructParent" || key == "StructParents":
			g.omitStructure()
			continue
		case key == "Dest" || (key == "D" && isGoTo):
			if dest, ok := g.remapDest(val); ok {
				out.Set(key, dest)
			}
			continue
		case key == "SD" && isGoTo:
			continue // a structure destination; the structure tree is not carried
		case key == "Kids" && num != 0 && g.fieldNodes[num]:
			out.Set(key, g.keptKids(val))
			continue
		case key == "Next":
			if arr, ok := g.sg.Resolve(val).(object.Array); ok {
				var next object.Array
				for _, a := range arr {
					if c, ok := g.copyValue(a); ok {
						next = append(next, c)
					}
				}
				if len(next) > 0 {
					out.Set(key, next)
				}
				continue
			}
		}
		if c, ok := g.copyValue(val); ok {
			out.Set(key, c)
		}
	}
	return out
}

// keptKids copies a form field's /Kids, keeping only the widgets on imported
// pages and the fields above them.
func (g *pageImporter) keptKids(val object.Object) object.Array {
	kids, _ := g.sg.Resolve(val).(object.Array)
	var out object.Array
	for _, k := range kids {
		ref, ok := k.(object.IndirectRef)
		if !ok || !g.keep[boundKey{ref.Number, g.ord}] {
			continue
		}
		if c, ok := g.copyRef(ref); ok {
			out = append(out, c)
		}
	}
	return out
}

// actionType returns an action dictionary's /S, or "" for anything else.
func actionType(v core.View, d *object.Dictionary) object.Name {
	s, _ := v.ResolveName(d.Get("S"))
	return s
}

// dropsOut reports whether a dictionary is a link or an action whose only
// purpose was to lead to a page that is not being imported.
func (g *pageImporter) dropsOut(d *object.Dictionary) bool {
	switch actionType(g.sg, d) {
	case "GoTo":
		_, ok := g.resolveDest(d.Get("D"))
		return !ok
	case "Thread", "GoToDp":
		return true
	}
	if sub, _ := g.sg.ResolveName(d.Get("Subtype")); sub != "Link" {
		return false
	}
	if dest := d.Get("Dest"); dest != nil {
		_, ok := g.resolveDest(dest)
		return !ok
	}
	if a := d.Get("A"); a != nil {
		if ref, isRef := a.(object.IndirectRef); isRef {
			return g.refDropsOut(ref)
		}
		if ad, ok := a.(*object.Dictionary); ok {
			return g.dropsOut(ad)
		}
	}
	return false
}

func (g *pageImporter) refDropsOut(ref object.IndirectRef) bool {
	if v, ok := g.droppedRef[ref.Number]; ok {
		return v
	}
	g.droppedRef[ref.Number] = false // a cycle through /A is not a drop
	d := g.sg.ResolveDict(ref)
	drop := d != nil && g.dropsOut(d)
	g.droppedRef[ref.Number] = drop
	return drop
}

// remapDest rewrites a destination for the target, and reports false when it
// leads to a page that was not imported.
func (g *pageImporter) remapDest(val object.Object) (object.Object, bool) {
	arr, ok := g.resolveDest(val)
	if !ok {
		return nil, false
	}
	out := make(object.Array, len(arr))
	out[0] = arr[0]
	for i := 1; i < len(arr); i++ {
		c, ok := g.copyValue(arr[i])
		if !ok {
			c = object.Null{}
		}
		out[i] = c
	}
	g.report.DestinationsRemapped++
	return out, true
}

// resolveDest turns any form of destination — explicit, named, or a
// dictionary with /D — into an explicit array whose first element already
// names the imported copy of its page. It reports false when the destination
// names no page that was imported.
func (g *pageImporter) resolveDest(val object.Object) (object.Array, bool) {
	for hops := 0; hops < 8; hops++ {
		switch v := g.sg.Resolve(val).(type) {
		case object.Name:
			val = g.namedDest(string(v))
		case object.String:
			val = g.namedDest(string(v.Value))
		case *object.Dictionary:
			val = v.Get("D")
		case object.Array:
			if len(v) == 0 {
				return nil, false
			}
			var page object.IndirectRef
			switch first := v[0].(type) {
			case object.IndirectRef:
				c, ok := g.firstCopy[first.Number]
				if !ok {
					return nil, false
				}
				page = c
			case object.Integer:
				// A page index, which is the remote form; accept it for a local
				// destination rather than drop a link a reader would follow.
				i := int(first)
				if i < 0 || i >= len(g.srcPages) {
					return nil, false
				}
				c, ok := g.firstCopy[g.srcPages[i].ObjNum]
				if !ok || g.srcPages[i].ObjNum == 0 {
					return nil, false
				}
				page = c
			default:
				return nil, false
			}
			out := append(object.Array{page}, v[1:]...)
			return out, true
		default:
			return nil, false
		}
	}
	return nil, false
}

// namedDest looks a named destination up in the source: in the catalog's
// /Dests dictionary and in the /Dests name tree, which is read once.
func (g *pageImporter) namedDest(name string) object.Object {
	if g.named == nil {
		g.named = map[string]object.Object{}
		cat := g.sg.Catalog()
		if cat == nil {
			return nil
		}
		if names := g.sg.ResolveDict(cat.Get("Names")); names != nil {
			walkNameTree(g.sg, names.Get("Dests"), func(key []byte, v object.Object) {
				if _, dup := g.named[string(key)]; !dup {
					g.named[string(key)] = v
				}
			})
		}
		if dests := g.sg.ResolveDict(cat.Get("Dests")); dests != nil {
			for k, v := range dests.All() {
				if _, dup := g.named[string(k)]; !dup {
					g.named[string(k)] = v
				}
			}
		}
	}
	return g.named[name]
}

// walkNameTree visits every leaf of a name tree once. The tree comes from the
// file, so each node is entered at most once and the depth is bounded.
func walkNameTree(v core.View, root object.Object, visit func(key []byte, val object.Object)) {
	seen := map[*object.Dictionary]bool{}
	var walk func(node object.Object, depth int)
	walk = func(node object.Object, depth int) {
		d := v.ResolveDict(node)
		if d == nil || depth > 32 || seen[d] {
			return
		}
		seen[d] = true
		if names, ok := v.Resolve(d.Get("Names")).(object.Array); ok {
			for i := 0; i+1 < len(names); i += 2 {
				if k, ok := v.Resolve(names[i]).(object.String); ok {
					visit(k.Value, names[i+1])
				}
			}
		}
		if kids, ok := v.Resolve(d.Get("Kids")).(object.Array); ok {
			for _, k := range kids {
				walk(k, depth+1)
			}
		}
	}
	walk(root, 0)
}

// mergeAcroForm adds the imported top-level fields to the target's /AcroForm,
// creating it when the target has none.
func (g *pageImporter) mergeAcroForm() {
	if len(g.fieldRoots) == 0 {
		return
	}
	var srcForm *object.Dictionary
	if cat := g.sg.Catalog(); cat != nil {
		srcForm = g.sg.ResolveDict(cat.Get("AcroForm"))
	}
	dstCat := g.dst.ResolveDict(g.dst.Trailer.Get("Root"))
	dstForm := g.dst.ResolveDict(dstCat.Get("AcroForm"))
	created := dstForm == nil
	if created {
		dstForm = &object.Dictionary{}
		dstCat.Set("AcroForm", dstForm)
	}
	fields, _ := g.dst.Resolve(dstForm.Get("Fields")).(object.Array)
	fields = slices.Clone(fields)
	taken := map[string]bool{}
	for _, f := range fields {
		if fd := g.dst.ResolveDict(f); fd != nil {
			if t, ok := g.dst.Resolve(fd.Get("T")).(object.String); ok {
				taken[string(t.Value)] = true
			}
		}
	}
	for _, rk := range g.fieldRoots {
		ref, ok := g.bound[rk]
		if !ok {
			continue
		}
		fd := g.dst.ResolveDict(ref)
		if fd == nil {
			continue
		}
		if t, ok := g.dst.Resolve(fd.Get("T")).(object.String); ok {
			name := t.Value
			if taken[string(name)] {
				renamed := uniqueFieldName(name, taken)
				fd.Set("T", object.String{Value: renamed})
				g.report.FieldsRenamed = append(g.report.FieldsRenamed,
					FieldRename{From: core.DecodePDFTextString(name), To: core.DecodePDFTextString(renamed)})
				name = renamed
			}
			taken[string(name)] = true
		}
		// A default appearance or quadding the field inherited from the
		// source's form, where the target's form says something else, moves
		// onto the field so it keeps the source's.
		if srcForm != nil && g.src != g.dst {
			for _, key := range []object.Name{"DA", "Q"} {
				sv := g.sg.Resolve(srcForm.Get(key))
				if sv == nil || fd.Get(key) != nil {
					continue
				}
				if !created && object.Equal(sv, g.dst.Resolve(dstForm.Get(key))) {
					continue
				}
				fd.Set(key, sv)
			}
		}
		fields = append(fields, ref)
	}
	dstForm.Set("Fields", fields)
	if srcForm == nil {
		return
	}
	if g.src != g.dst {
		g.mergeDR(srcForm, dstForm)
		for _, key := range []object.Name{"NeedAppearances", "SigFlags"} {
			if v := g.sg.Resolve(srcForm.Get(key)); v != nil && dstForm.Get(key) == nil {
				dstForm.Set(key, v)
			}
		}
		if srcForm.Get("XFA") != nil {
			g.report.omit("AcroForm/XFA", "the source's XFA form is not carried; its AcroForm fields are")
		}
	}
	if co, ok := g.sg.Resolve(srcForm.Get("CO")).(object.Array); ok {
		dstCO, _ := g.dst.Resolve(dstForm.Get("CO")).(object.Array)
		dstCO = slices.Clone(dstCO)
		grown := false
		for _, c := range co {
			ref, ok := c.(object.IndirectRef)
			if !ok {
				continue
			}
			for ord := 0; ; ord++ {
				copied, ok := g.bound[boundKey{ref.Number, ord}]
				if !ok {
					break
				}
				dstCO = append(dstCO, copied)
				grown = true
			}
		}
		if grown {
			dstForm.Set("CO", dstCO)
		}
	}
}

// mergeDR adds the source form's default resources to the target's, by name.
// A name the target already defines is kept as the target's, and reported.
func (g *pageImporter) mergeDR(srcForm, dstForm *object.Dictionary) {
	srcDR := g.sg.ResolveDict(srcForm.Get("DR"))
	if srcDR == nil {
		return
	}
	dstDR := g.dst.ResolveDict(dstForm.Get("DR"))
	if dstDR == nil {
		if c, ok := g.copyValue(srcForm.Get("DR")); ok {
			dstForm.Set("DR", c)
		}
		return
	}
	for cat, sv := range srcDR.All() {
		srcSub := g.sg.ResolveDict(sv)
		if srcSub == nil {
			continue
		}
		dstSub := g.dst.ResolveDict(dstDR.Get(cat))
		if dstSub == nil {
			if c, ok := g.copyValue(sv); ok {
				dstDR.Set(cat, c)
			}
			continue
		}
		for name, v := range srcSub.All() {
			if dstSub.Get(name) != nil {
				g.report.omit("AcroForm/DR/"+string(cat)+"/"+string(name),
					"the target's form already defines this default resource; the imported fields use the target's")
				continue
			}
			if c, ok := g.copyValue(v); ok {
				dstSub.Set(name, c)
			}
		}
	}
}

// uniqueFieldName appends _2, _3, … to a field name until it is unused. A
// UTF-16 name gets a UTF-16 suffix.
func uniqueFieldName(name []byte, taken map[string]bool) []byte {
	utf16 := bytes.HasPrefix(name, []byte{0xFE, 0xFF})
	for n := 2; ; n++ {
		suffix := []byte("_" + strconv.Itoa(n))
		if utf16 {
			wide := make([]byte, 0, 2*len(suffix))
			for _, b := range suffix {
				wide = append(wide, 0, b)
			}
			suffix = wide
		}
		cand := append(slices.Clone(name), suffix...)
		if !taken[string(cand)] {
			return cand
		}
	}
}

// carryCatalog carries the document-level entries that describe the imported
// pages, and reports every other entry of the source's catalog as omitted.
func (g *pageImporter) carryCatalog(fresh bool) {
	srcCat := g.sg.Catalog()
	if srcCat == nil || g.src == g.dst {
		return // appending a document to itself loses nothing at this level
	}
	dstCat := g.dst.ResolveDict(g.dst.Trailer.Get("Root"))
	g.carryOptionalContent(srcCat, dstCat)
	for key := range srcCat.All() {
		switch key {
		case "Type", "Pages", "AcroForm", "OCProperties", "Version":
			continue
		case "OutputIntents", "Lang":
			if fresh {
				if c, ok := g.copyValue(srcCat.Get(key)); ok {
					dstCat.Set(key, c)
				}
				continue
			}
		}
		g.report.omit(string(key), catalogOmissionReason(key, fresh))
	}
	if fresh && g.src.Trailer.Get("Info") != nil {
		g.report.omit("Info", "the source's document information dictionary describes the source, not the extract")
	}
	if g.src.Encrypted {
		g.report.omit("Encrypt", "the source's encryption is not carried: the pages are written the way the target is "+
			"(in the clear, unless it has encryption of its own); call SetEncryption on the target to protect them")
	}
}

func catalogOmissionReason(key object.Name, fresh bool) string {
	switch key {
	case "StructTreeRoot", "MarkInfo":
		return "the logical structure tree is not carried; the imported pages are untagged"
	case "Outlines":
		return "the source's outline (bookmarks) is not carried"
	case "Dests", "Names":
		return "the source's name dictionary (named destinations, embedded files, scripts, …) is not carried; " +
			"named destinations used on imported pages were written out explicitly"
	case "OutputIntents":
		return "the target's own output intents apply to the imported pages"
	case "Lang":
		return "the target's /Lang applies to the imported pages"
	case "Threads":
		return "article threads are not carried"
	case "PageLabels":
		return "page labels are not carried; the imported pages are numbered as the target numbers them"
	}
	return "a document-level entry of the source; a page import does not carry it"
}

// carryOptionalContent carries the source's optional-content groups so that a
// layer that was hidden stays hidden on the imported pages.
func (g *pageImporter) carryOptionalContent(srcCat, dstCat *object.Dictionary) {
	srcOC := g.sg.ResolveDict(srcCat.Get("OCProperties"))
	if srcOC == nil {
		return
	}
	dstOC := g.dst.ResolveDict(dstCat.Get("OCProperties"))
	if dstOC == nil {
		if c, ok := g.copyValue(srcCat.Get("OCProperties")); ok {
			dstCat.Set("OCProperties", c)
		}
		return
	}
	srcD := g.sg.ResolveDict(srcOC.Get("D"))
	dstD := g.dst.ResolveDict(dstOC.Get("D"))
	if dstD == nil {
		dstD = &object.Dictionary{}
		dstOC.Set("D", dstD)
	}
	srcOff := func(ref object.IndirectRef) bool {
		if srcD == nil {
			return false
		}
		base, _ := g.sg.ResolveName(srcD.Get("BaseState"))
		off := base == "OFF"
		for _, list := range []struct {
			key object.Name
			val bool
		}{{"ON", false}, {"OFF", true}} {
			arr, _ := g.sg.Resolve(srcD.Get(list.key)).(object.Array)
			for _, a := range arr {
				if object.RefNum(a) == ref.Number {
					off = list.val
				}
			}
		}
		return off
	}
	dstBase, _ := g.dst.Resolve(dstD.Get("BaseState")).(object.Name)
	dstOffByDefault := dstBase == "OFF"
	ocgs, _ := g.dst.Resolve(dstOC.Get("OCGs")).(object.Array)
	ocgs = slices.Clone(ocgs)
	var on, off object.Array
	srcOCGs, _ := g.sg.Resolve(srcOC.Get("OCGs")).(object.Array)
	for _, s := range srcOCGs {
		ref, ok := s.(object.IndirectRef)
		if !ok {
			continue
		}
		c, ok := g.copyRef(ref)
		if !ok {
			continue
		}
		ocgs = append(ocgs, c)
		switch isOff := srcOff(ref); {
		case isOff && !dstOffByDefault:
			off = append(off, c)
		case !isOff && dstOffByDefault:
			on = append(on, c)
		}
	}
	dstOC.Set("OCGs", ocgs)
	for _, list := range []struct {
		key  object.Name
		refs object.Array
	}{{"ON", on}, {"OFF", off}} {
		if len(list.refs) == 0 {
			continue
		}
		cur, _ := g.dst.Resolve(dstD.Get(list.key)).(object.Array)
		dstD.Set(list.key, append(slices.Clone(cur), list.refs...))
	}
	if srcD != nil {
		if order, ok := g.dst.Resolve(dstD.Get("Order")).(object.Array); ok {
			if srcOrder := srcD.Get("Order"); srcOrder != nil {
				if c, ok := g.copyValue(srcOrder); ok {
					if arr, ok := c.(object.Array); ok {
						dstD.Set("Order", append(slices.Clone(order), arr...))
					}
				}
			}
		}
		for _, key := range []object.Name{"RBGroups", "Locked", "AS", "Intent", "ListMode"} {
			if srcD.Get(key) != nil {
				g.report.omit("OCProperties/D/"+string(key),
					"the imported optional-content groups keep their default visibility; the rest of the source's configuration is not merged")
			}
		}
	}
	if srcOC.Get("Configs") != nil {
		g.report.omit("OCProperties/Configs", "alternate optional-content configurations of the source are not carried")
	}
}

// sourceVersion is the PDF version the source declares: its header, or its
// catalog's /Version when that is later (ISO 32000-2 7.7.2).
func (g *pageImporter) sourceVersion() string {
	return g.src.declaredVersion()
}

// maxVersion returns the later of two "major.minor" PDF versions. A value that
// does not parse loses to one that does.
func maxVersion(a, b string) string {
	pa, oka := parseVersion(a)
	pb, okb := parseVersion(b)
	switch {
	case !oka:
		return b
	case !okb:
		return a
	case pb[0] > pa[0] || (pb[0] == pa[0] && pb[1] > pa[1]):
		return b
	}
	return a
}

func parseVersion(s string) ([2]int, bool) {
	major, minor, ok := bytes.Cut([]byte(s), []byte("."))
	if !ok {
		return [2]int{}, false
	}
	ma, err1 := strconv.Atoi(string(major))
	mi, err2 := strconv.Atoi(string(minor))
	if err1 != nil || err2 != nil {
		return [2]int{}, false
	}
	return [2]int{ma, mi}, true
}
