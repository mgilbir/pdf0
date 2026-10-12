package pdfua

import (
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
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

// TestUAFirstHeadingH1 flags a document whose first heading is not H1. ISO
// 14289-1 7.4.2 asks it of documents that are not strongly structured
// (numbered headings are what make one weakly structured), so the message
// must not say "strongly structured".
func TestUAFirstHeadingH1(t *testing.T) {
	doc := mkView(map[int]*object.IndirectObject{}, nil)
	cat := headingDoc(doc, object.Array{heading(doc, 10, "H2"), heading(doc, 11, "H3")})
	got := findings(checkUAHeadings(doc, cat), "7.4.2", "first heading is H2")
	if len(got) != 1 {
		t.Errorf("first heading H2: want one 7.4.2 finding, got %v", got)
	} else if strings.Contains(got[0], "strongly") {
		t.Errorf("the first-heading rule is about documents that are not strongly structured, got %q", got[0])
	}
	doc = mkView(map[int]*object.IndirectObject{}, nil)
	cat = headingDoc(doc, object.Array{heading(doc, 10, "H1"), heading(doc, 11, "H2")})
	if got := findings(checkUAHeadings(doc, cat), "7.4.2", "first heading"); len(got) != 0 {
		t.Errorf("first heading H1 wrongly flagged: %v", got)
	}
}

// TestUAOneHPerNode flags a node with two child H headings.
func TestUAOneHPerNode(t *testing.T) {
	doc := mkView(map[int]*object.IndirectObject{}, nil)
	sect := heading(doc, 12, "Sect", heading(doc, 10, "H"), heading(doc, 11, "H"))
	cat := headingDoc(doc, sect)
	if !hasUAClause(checkUAOneHPerNode(doc, cat), "7.4.4") {
		t.Error("two H children under one node not flagged")
	}
	doc = mkView(map[int]*object.IndirectObject{}, nil)
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
	doc := mkView(nil, nil)
	cat := headingDoc(doc, object.Array{heading(doc, 10, "Titre1"), heading(doc, 11, "Titre3")})
	roleMap := &object.Dictionary{}
	roleMap.Set("Titre1", object.Name("H1"))
	roleMap.Set("Titre3", object.Name("H3"))
	doc.ResolveDict(cat.Get("StructTreeRoot")).Set("RoleMap", roleMap)
	if len(findings(checkUAHeadings(doc, cat), "7.4.2", "skipping a level")) == 0 {
		t.Error("role-mapped heading skip (Titre1=H1 followed by Titre3=H3) was not flagged")
	}
}
