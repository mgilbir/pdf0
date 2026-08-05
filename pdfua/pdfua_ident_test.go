package pdfua

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"testing"
)

func TestXMPPDFUAPart(t *testing.T) {
	cases := map[string]string{
		`<pdfuaid:part>1</pdfuaid:part>`:  "1",
		`<pdfuaid:part>2</pdfuaid:part>`:  "2",
		`pdfuaid:part="1"`:                "1",
		`pdfuaid:part='3'`:                "3",
		`rdf:about="" pdfuaid:part="1" x`: "1",
		`no part here`:                    "",
	}
	for in, want := range cases {
		if got := xmpPDFUAPart(in); got != want {
			t.Errorf("xmpPDFUAPart(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUAStructParent(t *testing.T) {
	mk := func(withP bool) core.View {
		doc := mkView(map[int]*object.IndirectObject{}, object.Dictionary{})
		e := &object.Dictionary{}
		e.Set("S", object.Name("P"))
		if withP {
			e.Set("P", object.IndirectRef{Number: 2})
		}
		doc.Objects[10] = &object.IndirectObject{Number: 10, Value: e}
		root := &object.Dictionary{}
		root.Set("Type", object.Name("StructTreeRoot"))
		root.Set("K", object.IndirectRef{Number: 10})
		doc.Objects[2] = &object.IndirectObject{Number: 2, Value: root}
		cat := &object.Dictionary{}
		cat.Set("StructTreeRoot", object.IndirectRef{Number: 2})
		doc.Objects[1] = &object.IndirectObject{Number: 1, Value: cat}
		return doc
	}
	if d := mk(false); len(checkUAStructParent(d, d.ResolveDict(object.IndirectRef{Number: 1}))) == 0 {
		t.Error("structure element without /P not flagged")
	}
	if d := mk(true); len(checkUAStructParent(d, d.ResolveDict(object.IndirectRef{Number: 1}))) != 0 {
		t.Error("structure element with /P wrongly flagged")
	}
}

func TestUARoleMapIntegrity(t *testing.T) {
	mk := func(roleMap *object.Dictionary) core.View {
		doc := mkView(map[int]*object.IndirectObject{}, object.Dictionary{})
		root := &object.Dictionary{}
		root.Set("Type", object.Name("StructTreeRoot"))
		root.Set("RoleMap", roleMap)
		doc.Objects[2] = &object.IndirectObject{Number: 2, Value: root}
		cat := &object.Dictionary{}
		cat.Set("StructTreeRoot", object.IndirectRef{Number: 2})
		doc.Objects[1] = &object.IndirectObject{Number: 1, Value: cat}
		return doc
	}
	// Remapping a standard type is flagged.
	remap := &object.Dictionary{}
	remap.Set("H1", object.Name("P"))
	if d := mk(remap); len(checkUARoleMapIntegrity(d, d.ResolveDict(object.IndirectRef{Number: 1}))) == 0 {
		t.Error("remapped standard type not flagged")
	}
	// A circular mapping is flagged.
	circ := &object.Dictionary{}
	circ.Set("Foo", object.Name("Bar"))
	circ.Set("Bar", object.Name("Foo"))
	if d := mk(circ); len(checkUARoleMapIntegrity(d, d.ResolveDict(object.IndirectRef{Number: 1}))) == 0 {
		t.Error("circular mapping not flagged")
	}
	// A clean custom mapping is accepted.
	ok := &object.Dictionary{}
	ok.Set("MyHeading", object.Name("H1"))
	if d := mk(ok); len(checkUARoleMapIntegrity(d, d.ResolveDict(object.IndirectRef{Number: 1}))) != 0 {
		t.Error("clean role map wrongly flagged")
	}
}
