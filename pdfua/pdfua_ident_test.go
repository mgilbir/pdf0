package pdfua

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"testing"
)

// uaXMPView is a document whose catalog carries the given XMP packet.
func uaXMPView(packet string) (core.View, *object.Dictionary) {
	doc := mkView(nil, nil)
	ms := object.NewStream(object.NewDictionary(), []byte(packet))
	doc.Objects[2] = &object.IndirectObject{Number: 2, Value: ms}
	cat := object.NewDictionary(object.Entry{Key: "Metadata", Value: object.IndirectRef{Number: 2}})
	doc.Objects[1] = &object.IndirectObject{Number: 1, Value: cat}
	doc.Trailer.Set("Root", object.IndirectRef{Number: 1})
	return doc, cat
}

func uaPacket(descAttrs, body string) string {
	return `<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">` +
		`<rdf:Description rdf:about=""` + descAttrs + `>` + body + `</rdf:Description></rdf:RDF></x:xmpmeta>`
}

// TestUAIdentifierReadByModel: pdfuaid:part is read through the XMP model, so
// every legal spelling is read as what it says and no text that merely looks
// like it is (audit C141).
func TestUAIdentifierReadByModel(t *testing.T) {
	const ns = ` xmlns:pdfuaid="http://www.aiim.org/pdfua/ns/id/"`
	cases := []struct {
		name, packet string
		wantClean    bool
	}{
		{"element", uaPacket(ns, `<pdfuaid:part>1</pdfuaid:part>`), true},
		{"attribute, double quotes", uaPacket(ns+` pdfuaid:part="1"`, ``), true},
		{"attribute, single quotes, spaces", uaPacket(ns+` pdfuaid:part = '1'`, ``), true},
		{"comment holding a stale value", uaPacket(ns, `<!-- <pdfuaid:part>2</pdfuaid:part> --><pdfuaid:part>1</pdfuaid:part>`), true},
		{"wrong part", uaPacket(ns, `<pdfuaid:part>2</pdfuaid:part>`), false},
		{"only in a comment", uaPacket(ns, `<!-- <pdfuaid:part>1</pdfuaid:part> -->`), false},
		{"prefix bound elsewhere", uaPacket(` xmlns:pdfuaid="urn:other"`, `<pdfuaid:part>1</pdfuaid:part>`), false},
		{"another prefix", uaPacket(` xmlns:ua="http://www.aiim.org/pdfua/ns/id/"`, `<ua:part>1</ua:part>`), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d, cat := uaXMPView(c.packet)
			got := checkUAIdentifier(d, cat, "1")
			if clean := len(got) == 0; clean != c.wantClean {
				t.Errorf("checkUAIdentifier = %v, want clean=%v", got, c.wantClean)
			}
		})
	}
}

func TestUAStructParent(t *testing.T) {
	mk := func(withP bool) core.View {
		doc := mkView(map[int]*object.IndirectObject{}, nil)
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
		doc := mkView(map[int]*object.IndirectObject{}, nil)
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
