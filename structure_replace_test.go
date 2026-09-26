package pdf0

import (
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/internal/hostile"
	"github.com/mgilbir/pdf0/object"
)

// Replacing and clearing the structure tree, PDF 2.0 structure types, and the
// bound on marked-content identifiers (audit 2026-09-22 C91, C92, C135).

func structParents(d *Document) []object.Object {
	var out []object.Object
	for _, p := range d.PageList() {
		out = append(out, p.Get("StructParents"))
	}
	return out
}

// TestReplacingTheTreeLeavesNoStaleIndex is the audit's scenario. The first
// tree gives page A index 0 and page B index 1; the second tags only B, which
// gets index 0 — and A, left with its 0, would have its marks resolve to B's
// heading. A page the new tree does not tag has no index.
func TestReplacingTheTreeLeavesNoStaleIndex(t *testing.T) {
	d := NewDocument()
	var refs []object.IndirectRef
	for i := 0; i < 2; i++ {
		var b content.Builder
		b.BeginTagged("P", 0).EndMarked()
		r, err := d.AddPage(Page{Width: 100, Height: 100, Content: &b})
		if err != nil {
			t.Fatal(err)
		}
		refs = append(refs, r)
	}
	a, bb := refs[0], refs[1]
	if err := d.SetStructureTree([]StructElem{
		{Tag: "P", Page: &a, Content: []int{0}},
		{Tag: "H1", Page: &bb, Content: []int{0}},
	}, nil); err != nil {
		t.Fatal(err)
	}
	// An annotation on A that the old tree gave a parent-tree key, as a
	// document read from a file can have: it indexes the old tree too.
	annot := object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("Annot")},
		object.Entry{Key: "Subtype", Value: object.Name("Link")},
		object.Entry{Key: "Rect", Value: object.Array{object.Integer(0), object.Integer(0), object.Integer(1), object.Integer(1)}},
		object.Entry{Key: "StructParent", Value: object.Integer(1)},
	)
	d.ResolveDict(a).Set("Annots", object.Array{d.Add(annot)})
	// And a form XObject whose content the old tree tagged, drawn from a form
	// the page draws: its /StructParents is a key into the old tree as well.
	inner := object.NewStream(object.NewDictionary(
		object.Entry{Key: "Subtype", Value: object.Name("Form")},
		object.Entry{Key: "StructParents", Value: object.Integer(1)},
	), nil)
	outer := object.NewStream(object.NewDictionary(
		object.Entry{Key: "Subtype", Value: object.Name("Form")},
		object.Entry{Key: "Resources", Value: object.NewDictionary(object.Entry{Key: "XObject",
			Value: object.NewDictionary(object.Entry{Key: "In", Value: d.Add(inner)})})},
	), nil)
	d.ResolveDict(d.ResolveDict(a).Get("Resources")).Set("XObject",
		object.NewDictionary(object.Entry{Key: "Out", Value: d.Add(outer)}))

	if err := d.SetStructureTree([]StructElem{{Tag: "H1", Page: &bb, Content: []int{0}}}, nil); err != nil {
		t.Fatal(err)
	}
	got := structParents(d)
	if got[0] != nil {
		t.Errorf("page A kept /StructParents %v from the old tree; the new one does not tag it", got[0])
	}
	if got[1] != object.Integer(0) {
		t.Errorf("page B has /StructParents %v, want 0", got[1])
	}
	if sp := annot.Get("StructParent"); sp != nil {
		t.Errorf("the annotation kept /StructParent %v, a key into the tree that was replaced", sp)
	}
	if sp := inner.Dict.Get("StructParents"); sp != nil {
		t.Errorf("the nested form kept /StructParents %v, a key into the tree that was replaced", sp)
	}

	if err := d.SetStructureTree(nil, nil); err != nil {
		t.Fatal(err)
	}
	for i, sp := range structParents(d) {
		if sp != nil {
			t.Errorf("after clearing, page %d still has /StructParents %v", i, sp)
		}
	}
}

// TestPDF20StructureTypesCarryTheirNamespace pins ISO 32000-2 14.8.6.1: an
// element with no /NS is in the PDF 1.7 standard structure namespace, where
// Title, Aside, Em, Strong, Sub, FENote, DocumentFragment, Artifact and Hn for
// n > 6 do not exist (Annex M). Written bare, a Title is a non-standard type.
// In a PDF 2.0 document such an element names the PDF 2.0 namespace, which the
// tree root lists; everything else stays in the default namespace.
func TestPDF20StructureTypesCarryTheirNamespace(t *testing.T) {
	d := NewDocument() // PDF 2.0
	page := taggedPage(t, d)
	if err := d.SetStructureTree([]StructElem{{Tag: "Document", Page: &page, Children: []StructElem{
		{Tag: "Title", Content: []int{0}},
		{Tag: "P", Content: []int{1}, Children: []StructElem{{Tag: "Em"}, {Tag: "H7"}}},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	catalog := d.ResolveDict(d.Trailer.Get("Root"))
	root := d.ResolveDict(catalog.Get("StructTreeRoot"))
	spaces, _ := d.Resolve(root.Get("Namespaces")).(object.Array)
	if len(spaces) != 1 {
		t.Fatalf("/Namespaces = %v, want the one PDF 2.0 namespace", root.Get("Namespaces"))
	}
	ns := d.ResolveDict(spaces[0])
	if s, _ := ns.Get("NS").(object.String); string(s.Value) != "http://iso.org/pdf2/ssn" {
		t.Errorf("the namespace is %v, want http://iso.org/pdf2/ssn", ns.Get("NS"))
	}
	if ns.Get("Type") != object.Name("Namespace") {
		t.Errorf("the namespace /Type is %v", ns.Get("Type"))
	}
	byTag := map[object.Name]*object.Dictionary{}
	var walk func(o object.Object)
	walk = func(o object.Object) {
		if arr, ok := d.Resolve(o).(object.Array); ok {
			for _, k := range arr {
				walk(k)
			}
			return
		}
		e := d.ResolveDict(o)
		if e == nil || e.Get("S") == nil {
			return
		}
		byTag[e.Get("S").(object.Name)] = e
		walk(e.Get("K"))
	}
	walk(root.Get("K"))
	for _, tag := range []object.Name{"Title", "Em", "H7"} {
		e := byTag[tag]
		if e == nil {
			t.Fatalf("no %s element", tag)
		}
		if ref, ok := e.Get("NS").(object.IndirectRef); !ok || ref != spaces[0] {
			t.Errorf("%s has /NS %v, want the PDF 2.0 namespace %v", tag, e.Get("NS"), spaces[0])
		}
	}
	for _, tag := range []object.Name{"Document", "P"} {
		if ns := byTag[tag].Get("NS"); ns != nil {
			t.Errorf("%s names /NS %v; it is a PDF 1.7 type and belongs in the default namespace", tag, ns)
		}
	}

	// A tree of 1.7 types alone declares no namespace at all.
	if err := d.SetStructureTree([]StructElem{{Tag: "P", Page: &page, Content: []int{0}}}, nil); err != nil {
		t.Fatal(err)
	}
	root = d.ResolveDict(d.ResolveDict(d.Trailer.Get("Root")).Get("StructTreeRoot"))
	if root.Get("Namespaces") != nil {
		t.Errorf("a tree of PDF 1.7 types declared /Namespaces %v", root.Get("Namespaces"))
	}
}

// TestPDF20StructureTypesNeedAPDF20Document pins the other half: below PDF 2.0
// there are no namespaces, so a 2.0-only type can only be a non-standard one,
// and is refused. So is a role map onto one: the root /RoleMap maps into the
// default (PDF 1.7) namespace, where the type does not exist.
func TestPDF20StructureTypesNeedAPDF20Document(t *testing.T) {
	d := NewDocument()
	d.Version = "1.7"
	page := taggedPage(t, d)
	for _, tag := range []string{"Title", "Aside", "Em", "Strong", "Sub", "FENote", "DocumentFragment", "Artifact", "H7"} {
		err := d.SetStructureTree([]StructElem{{Tag: tag, Page: &page, Content: []int{0}}}, nil)
		if err == nil || !strings.Contains(err.Error(), "2.0") {
			t.Errorf("%s in a PDF 1.7 document: err = %v, want a refusal naming PDF 2.0", tag, err)
		}
	}
	// A catalog /Version of 2.0 makes it a PDF 2.0 document whatever the header says.
	d.ResolveDict(d.Trailer.Get("Root")).Set("Version", object.Name("2.0"))
	if err := d.SetStructureTree([]StructElem{{Tag: "Title", Page: &page, Content: []int{0}}}, nil); err != nil {
		t.Errorf("Title with catalog /Version 2.0 was refused: %v", err)
	}

	d2 := NewDocument()
	page2 := taggedPage(t, d2)
	err := d2.SetStructureTree([]StructElem{{Tag: "Heading", Page: &page2, Content: []int{0}}},
		map[string]string{"Heading": "Title"})
	if err == nil {
		t.Error("a role map onto Title, a PDF 2.0-only type, was accepted")
	}
}

// TestHugeMCIDIsRefused pins C135. The parent-tree entry for a page is an
// array indexed by identifier, so it is as long as the largest identifier; one
// of 1<<28 allocated gigabytes. It is refused, as is the same identifier in the
// content stream, before anything is allocated.
func TestHugeMCIDIsRefused(t *testing.T) {
	hostile.Run(t, hostile.Limits{MaxRSS: 256 << 20, Timeout: time.Minute}, func(t *testing.T) {
		d := NewDocument()
		page := taggedPage(t, d)
		before := docSnapshot(t, d)
		err := d.SetStructureTree([]StructElem{{Tag: "P", Page: &page, Content: []int{1 << 28}}}, nil)
		if err == nil || !strings.Contains(err.Error(), "marked-content identifier") {
			t.Fatalf("err = %v, want the identifier refused", err)
		}
		if docSnapshot(t, d) != before {
			t.Error("the refused tree changed the document")
		}
		var b content.Builder
		b.BeginTagged("P", 1<<28).EndMarked()
		if _, err := b.Bytes(); err == nil {
			t.Error("BeginTagged accepted an identifier SetStructureTree refuses")
		}
		// The largest identifier allowed is accepted by both.
		var ok content.Builder
		ok.BeginTagged("P", content.MaxMCID).EndMarked()
		if _, err := ok.Bytes(); err != nil {
			t.Errorf("BeginTagged(MaxMCID): %v", err)
		}
	})
}

// TestAStructureTreeRefusalChangesNothing pins that a tree refused by a check
// that used to run while it was being written — an identifier claimed twice —
// leaves no half-written tree behind (the C131 rule, for this builder).
func TestAStructureTreeRefusalChangesNothing(t *testing.T) {
	d := NewDocument()
	page := taggedPage(t, d)
	before := docSnapshot(t, d)
	err := d.SetStructureTree([]StructElem{{Tag: "Document", Page: &page, Children: []StructElem{
		{Tag: "H1", Content: []int{0}},
		{Tag: "P", Content: []int{0}},
	}}}, nil)
	if err == nil {
		t.Fatal("accepted")
	}
	if docSnapshot(t, d) != before {
		t.Error("the refused tree changed the document")
	}
}
