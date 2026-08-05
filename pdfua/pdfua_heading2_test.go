package pdfua

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"testing"
)

// heading adds a structure element with type s (and optional kids) to doc and
// returns a reference to it.
func heading(doc core.View, num int, s object.Name, kids ...object.Object) object.IndirectRef {
	e := &object.Dictionary{}
	e.Set("S", s)
	if len(kids) > 0 {
		e.Set("K", object.Array(kids))
	}
	doc.Objects[num] = &object.IndirectObject{Number: num, Value: e}
	return object.IndirectRef{Number: num}
}

// headingDoc wraps a structure tree with the given top-level /K under a catalog.
func headingDoc(doc core.View, k object.Object) *object.Dictionary {
	root := &object.Dictionary{}
	root.Set("Type", object.Name("StructTreeRoot"))
	root.Set("K", k)
	doc.Objects[2] = &object.IndirectObject{Number: 2, Value: root}
	cat := &object.Dictionary{}
	cat.Set("StructTreeRoot", object.IndirectRef{Number: 2})
	doc.Objects[1] = &object.IndirectObject{Number: 1, Value: cat}
	return cat
}

// TestUAFirstHeadingH1 flags a document whose first heading is not H1.
func TestUAFirstHeadingH1(t *testing.T) {
	doc := mkView(map[int]*object.IndirectObject{}, object.Dictionary{})
	cat := headingDoc(doc, object.Array{heading(doc, 10, "H2"), heading(doc, 11, "H3")})
	if !hasUAClause(checkUAHeadings(doc, cat), "7.4.2") {
		t.Error("first heading H2 not flagged")
	}
	doc = mkView(map[int]*object.IndirectObject{}, object.Dictionary{})
	cat = headingDoc(doc, object.Array{heading(doc, 10, "H1"), heading(doc, 11, "H2")})
	if hasUAClause(checkUAHeadings(doc, cat), "7.4.2") {
		t.Error("first heading H1 wrongly flagged")
	}
}

// TestUAOneHPerNode flags a node with two child H headings.
func TestUAOneHPerNode(t *testing.T) {
	doc := mkView(map[int]*object.IndirectObject{}, object.Dictionary{})
	sect := heading(doc, 12, "Sect", heading(doc, 10, "H"), heading(doc, 11, "H"))
	cat := headingDoc(doc, sect)
	if !hasUAClause(checkUAOneHPerNode(doc, cat), "7.4.4") {
		t.Error("two H children under one node not flagged")
	}
	doc = mkView(map[int]*object.IndirectObject{}, object.Dictionary{})
	single := heading(doc, 12, "Sect", heading(doc, 10, "H"))
	cat = headingDoc(doc, single)
	if hasUAClause(checkUAOneHPerNode(doc, cat), "7.4.4") {
		t.Error("single H child wrongly flagged")
	}
}

// TestUAHeadingsRoleMapResolved is the C29 guard: the heading-level rules key
// off the /RoleMap-resolved type, like the sibling heading checks, so a level
// skip through custom types (Titre1→H1, Titre3→H3) is caught.
func TestUAHeadingsRoleMapResolved(t *testing.T) {
	doc := mkView(nil, object.Dictionary{})
	cat := headingDoc(doc, object.Array{heading(doc, 10, "Titre1"), heading(doc, 11, "Titre3")})
	roleMap := &object.Dictionary{}
	roleMap.Set("Titre1", object.Name("H1"))
	roleMap.Set("Titre3", object.Name("H3"))
	doc.ResolveDict(cat.Get("StructTreeRoot")).Set("RoleMap", roleMap)
	if !hasUAClause(checkUAHeadings(doc, cat), "7.4") {
		t.Error("role-mapped heading skip (Titre1=H1 followed by Titre3=H3) was not flagged")
	}
}
