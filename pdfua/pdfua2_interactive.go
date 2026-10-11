package pdfua

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// This file holds the PDF/UA-2 rules about navigation, annotations, form
// widgets and embedded files that PDF/UA-1 does not have: structure
// destinations (ISO 14289-2 8.8), invisible annotations as artifacts
// (8.9.2.2), the file attachment's AFRelationship (8.9.2.4.10), a widget's
// label or /Contents (8.10.2.3), and an embedded file's /Desc (8.14.1). Each
// follows what the veraPDF 1.30.3 model reads for the property its PDFUA-2
// profile tests. An annotation's enclosing structure element is the one whose
// OBJR names it (annotContext), where veraPDF looks the annotation's
// /StructParent up in /ParentTree; in a file whose two agree the answers do.

// checkUA2StructDestinations is ISO 14289-2 8.8: every destination whose
// target lies within the document is a structure destination (ISO 32000-2
// 12.3.2.3). It reads the destinations veraPDF reads (8.8-t01, t02):
//
//   - an outline item's /Dest and a Link annotation's /Dest, each when the
//     dictionary has no /A, and the catalog's /OpenAction when it is an array —
//     each must be a structure destination (destResolver.isStruct);
//   - every GoTo action: one with /SD names a structure destination; one
//     without must name one through /D, by a name or a string. An explicit /D
//     array is a page destination, whatever its first element: a GoTo action
//     carries its structure destination in /SD.
//
// The actions are the ones veraPDF's model reaches: an outline item's and an
// annotation's /A, the catalog's /OpenAction, the additional actions (/AA) of
// the catalog, a page, an annotation and a form field, and each action's /Next
// chain (actionSet). An object is an action because of where it is
// referenced, not its shape: a named destination written as a dictionary that
// happens to carry /S /GoTo is a destination, judged where it is used.
func checkUA2StructDestinations(d core.View, cat *object.Dictionary) []Violation {
	dests := newDestResolver(d, cat)
	acts := newActionSet(d)
	var v []Violation
	report := func(where string, num int) {
		v = append(v, Violation{Clause: "8.8", Message: "the destination of " + where + " is not a structure destination", Object: num})
	}

	// Outline items, walked iteratively: the file decides how deep and how
	// long the outline is, and a /First or /Next loop is visited once.
	if ol := d.ResolveDict(cat.Get("Outlines")); ol != nil {
		seen := map[*object.Dictionary]bool{}
		stack := []object.Object{ol.Get("First")}
		for len(stack) > 0 {
			ref := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			item := d.ResolveDict(ref)
			if item == nil || seen[item] {
				continue
			}
			d.Charge(1)
			seen[item] = true
			if dest, ok := item.Lookup("Dest"); ok && !item.Has("A") && !dests.isStruct(dest) {
				report("an outline item", object.RefNum(ref))
			}
			acts.add(item.Get("A"))
			stack = append(stack, item.Get("Next"), item.Get("First"))
		}
	}

	// The open action: a destination, or an action.
	switch oa := d.Resolve(cat.Get("OpenAction")).(type) {
	case object.Array:
		if !dests.isStruct(oa) {
			report("the open action", 0)
		}
	case *object.Dictionary:
		acts.add(oa)
	}
	acts.addAA(cat.Get("AA"))
	for _, pg := range d.Pages(d.CatalogPages()) {
		acts.addAA(pg.Dict.Get("AA"))
	}
	forEachField(d, cat, func(field *object.Dictionary) { acts.addAA(field.Get("AA")) })

	// The annotations on the pages: a Link's /Dest, every one's actions.
	for _, a := range newAnnotContext(d, cat).annots {
		if st, _ := d.ResolveName(a.Get("Subtype")); st == "Link" {
			if dest, ok := a.Lookup("Dest"); ok && !a.Has("A") && !dests.isStruct(dest) {
				report("a Link annotation", d.ObjNumOf(a))
			}
		}
		acts.add(a.Get("A"))
		acts.addAA(a.Get("AA"))
	}

	for _, act := range acts.all() {
		if s, _ := d.ResolveName(act.Get("S")); s != "GoTo" || act.Has("SD") {
			continue
		}
		dest := d.Resolve(act.Get("D"))
		switch dest.(type) {
		case object.Name, object.String:
			if dests.isStruct(dest) {
				continue
			}
		}
		v = append(v, Violation{Clause: "8.8", Message: "a GoTo action has no structure destination (/SD, or a named destination that is one)", Object: d.ObjNumOf(act)})
	}
	return v
}

// actionSet gathers action dictionaries, each once, with the actions their
// /Next entries chain to (a dictionary or an array of them). The chains are
// the file's to make, so they are followed iteratively, every action charged
// to the run's work meter, and a loop ends where it closes.
type actionSet struct {
	d       core.View
	seen    map[*object.Dictionary]bool
	seenRef map[int]bool // indirect objects followed, arrays included
	order   []*object.Dictionary
}

func newActionSet(d core.View) *actionSet {
	return &actionSet{d: d, seen: map[*object.Dictionary]bool{}, seenRef: map[int]bool{}}
}

// add records the action o and its /Next chain.
func (s *actionSet) add(o object.Object) {
	stack := []object.Object{o}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		s.d.Charge(1)
		if ref, ok := cur.(object.IndirectRef); ok {
			if s.seenRef[ref.Number] {
				continue // an array that holds itself, or an action met before
			}
			s.seenRef[ref.Number] = true
		}
		if arr, ok := s.d.Resolve(cur).(object.Array); ok {
			for i := len(arr) - 1; i >= 0; i-- {
				stack = append(stack, arr[i])
			}
			continue
		}
		act := s.d.ResolveDict(cur)
		if act == nil || s.seen[act] {
			continue
		}
		s.seen[act] = true
		s.order = append(s.order, act)
		stack = append(stack, act.Get("Next"))
	}
}

// addAA records every action of an additional-actions dictionary.
func (s *actionSet) addAA(o object.Object) {
	aa := s.d.ResolveDict(o)
	if aa == nil {
		return
	}
	for v := range aa.Values() {
		s.add(v)
	}
}

// all is the actions recorded, in the order they were first reached.
func (s *actionSet) all() []*object.Dictionary { return s.order }

// destResolver answers whether a destination is a structure destination,
// looking named destinations up in the catalog's /Dests dictionary (a name)
// and its /Names /Dests name tree (a string). The name tree is read once.
type destResolver struct {
	d      core.View
	cat    *object.Dictionary
	byName map[string]object.Object
	read   bool
}

func newDestResolver(d core.View, cat *object.Dictionary) *destResolver {
	return &destResolver{d: d, cat: cat}
}

// isStruct is veraPDF's PDDestination.getIsStructDestination: a named
// destination is replaced by what it names (and one that names nothing is no
// structure destination); then a destination dictionary is one when it has
// /SD, and an array when its first element is a structure element — a
// dictionary with /S, where a page destination names a page.
//
// A string name in a document that was not decrypted is ciphertext, and so
// are the name tree's keys: they cannot be matched, and the destination is
// not reported.
func (r *destResolver) isStruct(dest object.Object) bool {
	d := r.d
	switch name := d.Resolve(dest).(type) {
	case object.String:
		if d.Locked {
			return true
		}
		r.load()
		target, ok := r.byName[string(name.Value)]
		if !ok {
			return false
		}
		dest = target
	case object.Name:
		dests := d.ResolveDict(r.cat.Get("Dests"))
		if dests == nil {
			return false
		}
		target, ok := dests.Lookup(name)
		if !ok {
			return false
		}
		dest = target
	}
	switch dv := d.Resolve(dest).(type) {
	case *object.Dictionary:
		return dv.Has("SD")
	case object.Array:
		if len(dv) == 0 {
			return false
		}
		first := d.ResolveDict(dv[0])
		return first != nil && first.Has("S")
	}
	return false
}

// load reads the /Names /Dests name tree into byName, the first entry for a
// key winning, each entry charged to the run's work meter.
func (r *destResolver) load() {
	if r.read {
		return
	}
	r.read = true
	r.byName = map[string]object.Object{}
	names := r.d.ResolveDict(r.cat.Get("Names"))
	if names == nil {
		return
	}
	entries, _ := r.d.NameTreeEntries(names.Get("Dests"))
	for _, e := range entries {
		r.d.Charge(1)
		if _, dup := r.byName[string(e.Key)]; !dup {
			r.byName[string(e.Key)] = e.Value
		}
	}
}

// annotStructIndex maps each annotation on the pages to the index, in the
// flattened tree, of the structure element enclosing it through an OBJR. An
// annotation not in the tree is absent.
func annotStructIndex(d core.View, cat *object.Dictionary, ctx annotContext) map[*object.Dictionary]int {
	nodes := structTree(d, cat)
	idx := map[*object.Dictionary]int{}
	for i, n := range nodes {
		if _, dup := idx[n.Elem]; n.HasS && !dup {
			idx[n.Elem] = i
		}
	}
	out := map[*object.Dictionary]int{}
	for _, a := range ctx.annots {
		if se := ctx.parent[a]; se != nil {
			if i, ok := idx[se]; ok {
				out[a] = i
			}
		}
	}
	return out
}

// inArtifacts reports, for each node of the flattened tree, whether it or an
// ancestor is an Artifact structure element (the PDF 2.0 type): veraPDF's
// PDAnnot.isArtifact, which walks the enclosing element's parents. Pre-order
// makes it one pass.
func inArtifacts(d core.View, nodes []structNode) []bool {
	in := make([]bool, len(nodes))
	for i, n := range nodes {
		d.Charge(1)
		in[i] = isStdType(n, "Artifact") || (n.Parent >= 0 && in[n.Parent])
	}
	return in
}

// checkUA2InvisibleAnnots is ISO 14289-2 8.9.2.2: an annotation whose
// Invisible flag is set (ISO 32000-2 Table 167, bit 1) is an artifact (veraPDF
// 8.9.2.2-t01). One that is enclosed by a structure element is reported,
// unless that element is, or is inside, an Artifact element.
func checkUA2InvisibleAnnots(d core.View, cat *object.Dictionary) []Violation {
	ctx := newAnnotContext(d, cat)
	at := annotStructIndex(d, cat, ctx)
	if len(at) == 0 {
		return nil
	}
	artifact := inArtifacts(d, structTree(d, cat))
	var v []Violation
	for _, a := range ctx.annots {
		i, tagged := at[a]
		if !tagged || artifact[i] {
			continue
		}
		if f, _ := d.Resolve(a.Get("F")).(object.Integer); f&1 != 0 {
			v = append(v, Violation{Clause: "8.9.2.2", Message: "an annotation with the Invisible flag is in the logical structure and is not an artifact", Object: d.ObjNumOf(a)})
		}
	}
	return v
}

// checkUA2FileAttachments is ISO 14289-2 8.9.2.4.10: a file attachment
// annotation's file specification includes /AFRelationship (veraPDF
// 8.9.2.4.10-t01). veraPDF reads the entry from /FS when /FS is a dictionary,
// and reports an annotation with an /FS that is not one too — a string file
// specification has no AFRelationship to give — and so does this.
func checkUA2FileAttachments(d core.View, cat *object.Dictionary) []Violation {
	var v []Violation
	for _, a := range newAnnotContext(d, cat).annots {
		if st, _ := d.ResolveName(a.Get("Subtype")); st != "FileAttachment" || !a.Has("FS") {
			continue
		}
		if fs := d.ResolveDict(a.Get("FS")); fs != nil {
			if _, ok := d.ResolveName(fs.Get("AFRelationship")); ok {
				continue
			}
		}
		v = append(v, Violation{Clause: "8.9.2.4.10", Message: "the file specification of a file attachment annotation has no /AFRelationship", Object: d.ObjNumOf(a)})
	}
	return v
}

// checkUA2WidgetDescription is ISO 14289-2 8.10.2.3: a widget annotation with
// no label has a /Contents entry, to describe it (veraPDF 8.10.2.3-t01). It
// applies, as veraPDF's does, to a field's widget (one with /T, or a /Parent)
// that is in the structure tree outside any Artifact element; a label is an
// Lbl child of the enclosing element, with content. Any /Contents string is
// one, the empty string included (veraPDF: Contents != null).
func checkUA2WidgetDescription(d core.View, cat *object.Dictionary) []Violation {
	ctx := newAnnotContext(d, cat)
	at := annotStructIndex(d, cat, ctx)
	if len(at) == 0 {
		return nil
	}
	nodes := structTree(d, cat)
	artifact := inArtifacts(d, nodes)
	labelled := map[int]bool{} // element index -> it has an Lbl child with content
	for _, n := range nodes {
		if n.Parent >= 0 && isStdType(n, "Lbl") && hasKids(d, n.Elem) {
			labelled[n.Parent] = true
		}
	}
	var v []Violation
	for _, a := range ctx.annots {
		i, tagged := at[a]
		if !tagged || artifact[i] || labelled[i] {
			continue
		}
		if st, _ := d.ResolveName(a.Get("Subtype")); st != "Widget" {
			continue
		}
		if !a.Has("T") && d.ResolveDict(a.Get("Parent")) == nil {
			continue // a widget that is no field's
		}
		if _, r := d.StringValue(a.Get("Contents")); r != core.ReasonAbsent {
			continue
		}
		v = append(v, Violation{Clause: "8.10.2.3", Message: "a widget annotation has neither a label (an Lbl beside it in its enclosing structure element) nor /Contents", Object: d.ObjNumOf(a)})
	}
	return v
}

// hasKids reports whether a structure element has any child — an element, a
// marked-content reference or an object reference.
func hasKids(d core.View, elem *object.Dictionary) bool {
	switch k := d.Resolve(elem.Get("K")).(type) {
	case nil, object.Null:
		return false
	case object.Array:
		return len(k) > 0
	}
	return true
}

// checkUA2EmbeddedFileDesc is ISO 14289-2 8.14.1: a file specification in the
// catalog's EmbeddedFiles name tree has a /Desc entry (veraPDF 8.14.1-t01,
// which asks only that the key be present).
func checkUA2EmbeddedFileDesc(d core.View, cat *object.Dictionary) []Violation {
	names := d.ResolveDict(cat.Get("Names"))
	if names == nil {
		return nil
	}
	entries, _ := d.NameTreeEntries(names.Get("EmbeddedFiles"))
	var v []Violation
	for _, e := range entries {
		d.Charge(1)
		fs := d.ResolveDict(e.Value)
		if fs == nil || fs.Has("Desc") {
			continue
		}
		v = append(v, Violation{Clause: "8.14.1", Message: "an embedded file's file specification has no /Desc", Object: object.RefNum(e.Value)})
	}
	return v
}
