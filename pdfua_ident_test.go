package pdf0

import "testing"

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
	mk := func(withP bool) *Document {
		doc := &Document{Objects: map[int]*IndirectObject{}}
		e := &Dictionary{}
		e.Set("S", Name("P"))
		if withP {
			e.Set("P", IndirectRef{Number: 2})
		}
		doc.Objects[10] = &IndirectObject{Number: 10, Value: e}
		root := &Dictionary{}
		root.Set("Type", Name("StructTreeRoot"))
		root.Set("K", IndirectRef{Number: 10})
		doc.Objects[2] = &IndirectObject{Number: 2, Value: root}
		cat := &Dictionary{}
		cat.Set("StructTreeRoot", IndirectRef{Number: 2})
		doc.Objects[1] = &IndirectObject{Number: 1, Value: cat}
		return doc
	}
	if d := mk(false); len(d.checkUAStructParent(d.ResolveDict(IndirectRef{Number: 1}))) == 0 {
		t.Error("structure element without /P not flagged")
	}
	if d := mk(true); len(d.checkUAStructParent(d.ResolveDict(IndirectRef{Number: 1}))) != 0 {
		t.Error("structure element with /P wrongly flagged")
	}
}

func TestUARoleMapIntegrity(t *testing.T) {
	mk := func(roleMap *Dictionary) *Document {
		doc := &Document{Objects: map[int]*IndirectObject{}}
		root := &Dictionary{}
		root.Set("Type", Name("StructTreeRoot"))
		root.Set("RoleMap", roleMap)
		doc.Objects[2] = &IndirectObject{Number: 2, Value: root}
		cat := &Dictionary{}
		cat.Set("StructTreeRoot", IndirectRef{Number: 2})
		doc.Objects[1] = &IndirectObject{Number: 1, Value: cat}
		return doc
	}
	// Remapping a standard type is flagged.
	remap := &Dictionary{}
	remap.Set("H1", Name("P"))
	if d := mk(remap); len(d.checkUARoleMapIntegrity(d.ResolveDict(IndirectRef{Number: 1}))) == 0 {
		t.Error("remapped standard type not flagged")
	}
	// A circular mapping is flagged.
	circ := &Dictionary{}
	circ.Set("Foo", Name("Bar"))
	circ.Set("Bar", Name("Foo"))
	if d := mk(circ); len(d.checkUARoleMapIntegrity(d.ResolveDict(IndirectRef{Number: 1}))) == 0 {
		t.Error("circular mapping not flagged")
	}
	// A clean custom mapping is accepted.
	ok := &Dictionary{}
	ok.Set("MyHeading", Name("H1"))
	if d := mk(ok); len(d.checkUARoleMapIntegrity(d.ResolveDict(IndirectRef{Number: 1}))) != 0 {
		t.Error("clean role map wrongly flagged")
	}
}
