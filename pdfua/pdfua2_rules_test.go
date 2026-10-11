package pdfua

import (
	"testing"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// The tests in this file pin the PDF/UA-2 rules that only ISO 14289-2 has
// (pdfua2_ident.go, pdfua2_struct.go, pdfua2_interactive.go). Each case is the
// shape of the veraPDF PDF_UA-2 fail file that tests the rule, reduced to the
// entries the rule reads, beside the conforming shapes that must stay clean:
// the boundaries the veraPDF 1.30.3 model draws (its PDFUA-2 profile states the
// rule; the model classes behind the profile's properties say what each one
// reads). Every case runs the whole validator for the part, so a rule moved or
// gated by part is still found.

// pdf2NS puts the fixture's Document element in the PDF 2.0 structure
// namespace, as ISO 14289-2 8.2.5.2 requires, and returns the namespace.
func pdf2NS(f *uaFixture) object.IndirectRef {
	ns := object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("Namespace")},
		object.Entry{Key: "NS", Value: str("http://iso.org/pdf2/ssn")},
	)
	ref := f.add(ns)
	f.root.Set("Namespaces", object.Array{ref})
	f.docEl.Set("NS", ref)
	return ref
}

// ua2Fixture is a one-page PDF/UA-2 document clean of everything this file
// tests: the Document element is in the PDF 2.0 namespace and the metadata
// declares pdfuaid:part 2 and pdfuaid:rev 2024.
func ua2Fixture() *uaFixture {
	f := newUAFixture("")
	f.cat.Set("Lang", str("en"))
	pdf2NS(f)
	f.cat.Set("Metadata", f.add(&object.Stream{Data: []byte(uaPacket(
		` xmlns:pdfuaid="http://www.aiim.org/pdfua/ns/id/" xmlns:dc="http://purl.org/dc/elements/1.1/" pdfuaid:part="2" pdfuaid:rev="2024"`,
		`<dc:title><rdf:Alt><rdf:li xml:lang="x-default">T</rdf:li></rdf:Alt></dc:title>`))}))
	return f
}

// TestUA2FixtureIsClean: the fixture the other tests start from reports
// nothing under any clause they test, so each test's finding is the defect it
// plants.
func TestUA2FixtureIsClean(t *testing.T) {
	vs := ua2Fixture().run("2")
	for _, c := range []string{"5", "8.2.5.2", "8.2.5.12", "8.2.5.25", "8.8", "8.9.2.2", "8.9.2.4.10", "8.10.1", "8.10.2.3", "8.14.1"} {
		if got := findings(vs, c, ""); len(got) != 0 {
			t.Errorf("clean fixture: clause %s reported %v", c, got)
		}
	}
}

// TestUA2Revision is ISO 14289-2 clause 5 (veraPDF 5-t04, 5-t05): pdfuaid:rev
// is required, must be the four-digit year 2024, and must be written with the
// pdfuaid prefix (corpus 5-t04-fail-a writes pdfuadd:rev, 5-t05-fail-a
// "2024a", 5-t05-fail-b "2018"). PDF/UA-1 has no rev.
func TestUA2Revision(t *testing.T) {
	const ns = ` xmlns:pdfuaid="http://www.aiim.org/pdfua/ns/id/"`
	for _, tc := range []struct {
		name, attrs string
		want        int
	}{
		{"2024", ns + ` pdfuaid:part="2" pdfuaid:rev="2024"`, 0},
		{"missing", ns + ` pdfuaid:part="2"`, 1},
		{"2024a", ns + ` pdfuaid:part="2" pdfuaid:rev="2024a"`, 1},
		{"2018", ns + ` pdfuaid:part="2" pdfuaid:rev="2018"`, 1},
		{"prefix", ns + ` xmlns:pdfuadd="http://www.aiim.org/pdfua/ns/id/" pdfuaid:part="2" pdfuadd:rev="2024"`, 1},
	} {
		f := ua2Fixture()
		f.cat.Set("Metadata", f.add(&object.Stream{Data: []byte(uaPacket(tc.attrs, ""))}))
		if got := findings(f.run("2"), "5", "rev"); len(got) != tc.want {
			t.Errorf("%s: want %d pdfuaid:rev finding(s), got %v", tc.name, tc.want, got)
		}
	}
	// PDF/UA-1 identification has no rev.
	f := ua2Fixture()
	f.cat.Set("Metadata", f.add(&object.Stream{Data: []byte(uaPacket(ns+` pdfuaid:part="1"`, ""))}))
	if got := findings(f.run("1"), "5", "rev"); len(got) != 0 {
		t.Errorf("PDF/UA-1: want no pdfuaid:rev finding, got %v", got)
	}
}

// TestUA2DocumentRoot is ISO 14289-2 8.2.5.2 (veraPDF 8.2.5.2-t01, -t02): the
// structure tree root holds one Document element, in the PDF 2.0 namespace.
// veraPDF looks through the grouping types NonStruct, Div and Part, and counts
// only elements whose type resolves to a standard one.
func TestUA2DocumentRoot(t *testing.T) {
	t.Run("no element", func(t *testing.T) { // corpus 8.2.5.2-t01-fail-a
		f := ua2Fixture()
		f.root.Delete("K")
		if got := findings(f.run("2"), "8.2.5.2", "single Document"); len(got) != 1 {
			t.Errorf("want 1 finding, got %v", got)
		}
	})
	t.Run("PDF 1.7 namespace", func(t *testing.T) { // corpus 8.2.5.2-t02-fail-a
		f := ua2Fixture()
		f.docEl.Delete("NS")
		if got := findings(f.run("2"), "8.2.5.2", "PDF 2.0 namespace"); len(got) != 1 {
			t.Errorf("want 1 finding, got %v", got)
		}
	})
	t.Run("two elements", func(t *testing.T) {
		f := ua2Fixture()
		_, _ = f.elem(f.root, f.structRef, "Sect")
		if got := findings(f.run("2"), "8.2.5.2", "single Document"); len(got) != 1 {
			t.Errorf("want 1 finding, got %v", got)
		}
	})
	t.Run("Sect at the root", func(t *testing.T) {
		f := ua2Fixture()
		f.docEl.Set("S", object.Name("Sect"))
		if got := findings(f.run("2"), "8.2.5.2", "single Document"); len(got) != 1 {
			t.Errorf("want 1 finding, got %v", got)
		}
	})
	t.Run("through a Part", func(t *testing.T) {
		f := ua2Fixture()
		ns := f.docEl.Get("NS")
		f.docEl.Set("S", object.Name("Part"))
		doc, _ := f.elem(f.docEl, f.docElRef, "Document")
		doc.Set("NS", ns)
		if got := findings(f.run("2"), "8.2.5.2", ""); len(got) != 0 {
			t.Errorf("Part > Document: want no finding, got %v", got)
		}
	})
	t.Run("role mapped into the PDF 2.0 namespace", func(t *testing.T) {
		f := ua2Fixture()
		mine := object.NewDictionary(
			object.Entry{Key: "Type", Value: object.Name("Namespace")},
			object.Entry{Key: "NS", Value: str("urn:example:mine")},
			object.Entry{Key: "RoleMapNS", Value: object.NewDictionary(
				object.Entry{Key: "MyDoc", Value: object.Array{object.Name("Document"), f.docEl.Get("NS")}})},
		)
		f.docEl.Set("S", object.Name("MyDoc"))
		f.docEl.Set("NS", f.add(mine))
		if got := findings(f.run("2"), "8.2.5.2", ""); len(got) != 0 {
			t.Errorf("MyDoc mapped to the PDF 2.0 Document: want no finding, got %v", got)
		}
	})
	t.Run("an unmapped element before the Document", func(t *testing.T) {
		// It is no standard type, so the Document is still the only one; but
		// it is the first child, and it is in no namespace.
		f := ua2Fixture()
		_, mr := f.elem(f.root, f.structRef, "Mystery")
		f.root.Set("K", object.Array{mr, f.docElRef})
		vs := f.run("2")
		if got := findings(vs, "8.2.5.2", "first child"); len(got) != 1 {
			t.Errorf("want 1 first-child finding, got %v", findings(vs, "8.2.5.2", ""))
		}
	})
	t.Run("PDF/UA-1", func(t *testing.T) {
		f := ua2Fixture()
		f.root.Delete("K")
		if got := findings(f.run("1"), "8.2.5.2", ""); len(got) != 0 {
			t.Errorf("PDF/UA-1: want no 8.2.5.2 finding, got %v", got)
		}
	})
}

// TestUA2NoH is ISO 14289-2 8.2.5.12 (veraPDF 8.2.5.12-t01): the H structure
// type is not used, whether written or reached through a role map.
func TestUA2NoH(t *testing.T) {
	f := ua2Fixture()
	f.elem(f.docEl, f.docElRef, "H1")
	if got := findings(f.run("2"), "8.2.5.12", ""); len(got) != 0 {
		t.Errorf("H1: want no finding, got %v", got)
	}
	f.elem(f.docEl, f.docElRef, "H") // corpus 8.2.5.12-t01-fail-a
	if got := findings(f.run("2"), "8.2.5.12", ""); len(got) != 1 {
		t.Errorf("H: want 1 finding, got %v", got)
	}
	g := ua2Fixture()
	g.root.Set("RoleMap", object.NewDictionary(object.Entry{Key: "Heading", Value: object.Name("H")}))
	g.elem(g.docEl, g.docElRef, "Heading")
	if got := findings(g.run("2"), "8.2.5.12", ""); len(got) != 1 {
		t.Errorf("Heading role mapped to H: want 1 finding, got %v", got)
	}
	if got := findings(g.run("1"), "8.2.5.12", ""); len(got) != 0 {
		t.Errorf("PDF/UA-1: want no 8.2.5.12 finding, got %v", got)
	}
}

// TestUA2ListNumbering is ISO 14289-2 8.2.5.25 (veraPDF 8.2.5.25-t01): a list
// whose items carry Lbl elements has a ListNumbering attribute other than
// None. veraPDF reads the attribute from /A, then the class map, then the
// parent element, and an absent one is None.
func TestUA2ListNumbering(t *testing.T) {
	type list struct {
		f  *uaFixture
		l  *object.Dictionary
		lr object.IndirectRef
	}
	build := func(withLbl bool) list {
		f := ua2Fixture()
		l, lr := f.elem(f.docEl, f.docElRef, "L")
		li, lir := f.elem(l, lr, "LI")
		if withLbl {
			f.elem(li, lir, "Lbl", object.Integer(0))
		}
		f.elem(li, lir, "LBody", object.Integer(1))
		return list{f, l, lr}
	}
	listAttr := func(v string) *object.Dictionary {
		return object.NewDictionary(object.Entry{Key: "O", Value: object.Name("List")}, object.Entry{Key: "ListNumbering", Value: object.Name(v)})
	}
	count := func(f *uaFixture) int { return len(findings(f.run("2"), "8.2.5.25", "ListNumbering")) }

	if b := build(true); count(b.f) != 1 {
		t.Error("Lbl without ListNumbering: want 1 finding")
	}
	b := build(true) // corpus 8.2.5.25-t01-fail-a
	b.l.Set("A", listAttr("None"))
	if count(b.f) != 1 {
		t.Error("Lbl with ListNumbering None: want 1 finding")
	}
	b.l.Set("A", object.Array{object.NewDictionary(object.Entry{Key: "O", Value: object.Name("Layout")}), listAttr("Decimal")})
	if count(b.f) != 0 {
		t.Error("Lbl with ListNumbering Decimal in an /A array: want no finding")
	}
	b = build(true)
	b.l.Set("A", object.NewDictionary(object.Entry{Key: "O", Value: object.Name("Layout")}, object.Entry{Key: "ListNumbering", Value: object.Name("Decimal")}))
	if count(b.f) != 1 {
		t.Error("ListNumbering under an owner other than List: want 1 finding")
	}
	b = build(true)
	b.f.root.Set("ClassMap", object.NewDictionary(object.Entry{Key: "num", Value: listAttr("Disc")}))
	b.l.Set("C", object.Name("num"))
	if count(b.f) != 0 {
		t.Error("ListNumbering from the class map: want no finding")
	}
	b = build(true)
	b.f.docEl.Set("A", listAttr("Decimal"))
	if count(b.f) != 0 {
		t.Error("ListNumbering inherited from the parent element: want no finding")
	}
	if b := build(false); count(b.f) != 0 {
		t.Error("no Lbl, no ListNumbering: want no finding")
	}
	// An LI reached through a grouping element still belongs to the list.
	f := ua2Fixture()
	l, lr := f.elem(f.docEl, f.docElRef, "L")
	div, divr := f.elem(l, lr, "Div")
	li, lir := f.elem(div, divr, "LI")
	f.elem(li, lir, "Lbl", object.Integer(0))
	if count(f) != 1 {
		t.Error("L > Div > LI > Lbl without ListNumbering: want 1 finding")
	}
	if got := findings(f.run("1"), "8.2.5.25", ""); len(got) != 0 {
		t.Errorf("PDF/UA-1: want no 8.2.5.25 finding, got %v", got)
	}
}

// TestUA2StructDestinations is ISO 14289-2 8.8 (veraPDF 8.8-t01, -t02): a
// destination in an outline item, the open action or a Link annotation is a
// structure destination, and so is a GoTo action's.
func TestUA2StructDestinations(t *testing.T) {
	pageDest := func(f *uaFixture) object.Array { return object.Array{f.pageRef, object.Name("Fit")} }
	elemDest := func(f *uaFixture) object.Array { return object.Array{f.docElRef, object.Name("Fit")} }
	outline := func(f *uaFixture, item *object.Dictionary) {
		ol := &object.Dictionary{}
		olr := f.add(ol)
		item.Set("Title", str("t"))
		item.Set("Parent", olr)
		ir := f.add(item)
		ol.Set("First", ir)
		ol.Set("Last", ir)
		f.cat.Set("Outlines", olr)
	}
	goTo := func(entries ...object.Entry) *object.Dictionary {
		return object.NewDictionary(append([]object.Entry{{Key: "S", Value: object.Name("GoTo")}}, entries...)...)
	}
	count := func(f *uaFixture) int { return len(findings(f.run("2"), "8.8", "")) }

	for _, tc := range []struct {
		name  string
		build func(f *uaFixture)
		want  int
	}{
		{"outline /Dest to a page", func(f *uaFixture) { // corpus 8.8-t01-fail-a
			outline(f, object.NewDictionary(object.Entry{Key: "Dest", Value: pageDest(f)}))
		}, 1},
		{"outline /Dest to a structure element", func(f *uaFixture) {
			outline(f, object.NewDictionary(object.Entry{Key: "Dest", Value: elemDest(f)}))
		}, 0},
		{"outline GoTo /D to a page", func(f *uaFixture) { // corpus 8.8-t02-fail-a
			outline(f, object.NewDictionary(object.Entry{Key: "A", Value: goTo(object.Entry{Key: "D", Value: pageDest(f)})}))
		}, 1},
		{"outline GoTo with /SD", func(f *uaFixture) {
			outline(f, object.NewDictionary(object.Entry{Key: "A", Value: goTo(
				object.Entry{Key: "D", Value: pageDest(f)}, object.Entry{Key: "SD", Value: elemDest(f)})}))
		}, 0},
		{"outline /Dest ignored beside /A", func(f *uaFixture) {
			outline(f, object.NewDictionary(
				object.Entry{Key: "Dest", Value: pageDest(f)},
				object.Entry{Key: "A", Value: goTo(object.Entry{Key: "SD", Value: elemDest(f)})}))
		}, 0},
		{"open action to a page", func(f *uaFixture) { f.cat.Set("OpenAction", pageDest(f)) }, 1},
		{"open action to a structure element", func(f *uaFixture) { f.cat.Set("OpenAction", elemDest(f)) }, 0},
		{"Link /Dest to a page", func(f *uaFixture) {
			a, _ := f.annot("Link", [4]int{10, 10, 100, 30}, "Link")
			a.Set("Dest", pageDest(f))
		}, 1},
		{"Link /Dest named, to a structure destination", func(f *uaFixture) {
			a, _ := f.annot("Link", [4]int{10, 10, 100, 30}, "Link")
			a.Set("Dest", str("there"))
			dest := object.NewDictionary(object.Entry{Key: "D", Value: pageDest(f)}, object.Entry{Key: "SD", Value: elemDest(f)})
			tree := object.NewDictionary(object.Entry{Key: "Names", Value: object.Array{str("there"), dest}})
			f.cat.Set("Names", object.NewDictionary(object.Entry{Key: "Dests", Value: tree}))
		}, 0},
		{"Link /Dest named, to a page", func(f *uaFixture) {
			a, _ := f.annot("Link", [4]int{10, 10, 100, 30}, "Link")
			a.Set("Dest", object.Name("there"))
			f.cat.Set("Dests", object.NewDictionary(object.Entry{Key: "there", Value: pageDest(f)}))
		}, 1},
		{"Link /Dest named, undefined", func(f *uaFixture) {
			a, _ := f.annot("Link", [4]int{10, 10, 100, 30}, "Link")
			a.Set("Dest", str("nowhere"))
		}, 1},
		{"Link GoTo /D named, to a structure destination", func(f *uaFixture) {
			a, _ := f.annot("Link", [4]int{10, 10, 100, 30}, "Link")
			a.Set("A", goTo(object.Entry{Key: "D", Value: object.Name("there")}))
			f.cat.Set("Dests", object.NewDictionary(object.Entry{Key: "there", Value: elemDest(f)}))
		}, 0},
		{"Link GoTo /D an explicit array", func(f *uaFixture) {
			// A GoTo action names a structure destination in /SD; veraPDF
			// does not read an explicit /D array as one.
			a, _ := f.annot("Link", [4]int{10, 10, 100, 30}, "Link")
			a.Set("A", goTo(object.Entry{Key: "D", Value: elemDest(f)}))
		}, 1},
		{"GoToR is not intra-document", func(f *uaFixture) {
			a, _ := f.annot("Link", [4]int{10, 10, 100, 30}, "Link")
			a.Set("A", object.NewDictionary(object.Entry{Key: "S", Value: object.Name("GoToR")}, object.Entry{Key: "F", Value: str("x.pdf")}, object.Entry{Key: "D", Value: object.Array{object.Integer(0), object.Name("Fit")}}))
		}, 0},
		{"GoTo in a /Next chain", func(f *uaFixture) {
			a, _ := f.annot("Link", [4]int{10, 10, 100, 30}, "Link")
			a.Set("A", object.NewDictionary(
				object.Entry{Key: "S", Value: object.Name("GoTo")}, object.Entry{Key: "SD", Value: elemDest(f)},
				object.Entry{Key: "Next", Value: object.Array{goTo(object.Entry{Key: "D", Value: pageDest(f)})}}))
		}, 1},
		{"GoTo in a page's additional actions", func(f *uaFixture) {
			f.page.Set("AA", object.NewDictionary(object.Entry{Key: "O", Value: goTo(object.Entry{Key: "D", Value: pageDest(f)})}))
		}, 1},
		{"open action GoTo to a page", func(f *uaFixture) {
			f.cat.Set("OpenAction", goTo(object.Entry{Key: "D", Value: pageDest(f)}))
		}, 1},
		{"a named destination shaped like an action is judged where it is used", func(f *uaFixture) {
			// PDF_UA-1 7.1-t01-pass-a: the Dests tree holds << /S /GoTo /D [page ...] >>
			// for the name a Link's GoTo uses. The Link's action is reported, once.
			a, _ := f.annot("Link", [4]int{10, 10, 100, 30}, "Link")
			a.Set("A", goTo(object.Entry{Key: "D", Value: str("Note")}))
			tree := object.NewDictionary(object.Entry{Key: "Names", Value: object.Array{str("Note"), f.add(goTo(object.Entry{Key: "D", Value: pageDest(f)}))}})
			f.cat.Set("Names", object.NewDictionary(object.Entry{Key: "Dests", Value: tree}))
		}, 1},
	} {
		f := ua2Fixture()
		tc.build(f)
		if got := count(f); got != tc.want {
			t.Errorf("%s: want %d 8.8 finding(s), got %d: %v", tc.name, tc.want, got, findings(f.run("2"), "8.8", ""))
		}
		if got := findings(f.run("1"), "8.8", ""); len(got) != 0 {
			t.Errorf("%s, PDF/UA-1: want no 8.8 finding, got %v", tc.name, got)
		}
	}
}

// TestUA2InvisibleAnnotArtifact is ISO 14289-2 8.9.2.2 (veraPDF 8.9.2.2-t01):
// an annotation with the Invisible flag is not in the logical structure,
// unless it is there as (inside) an Artifact element.
func TestUA2InvisibleAnnotArtifact(t *testing.T) {
	f := ua2Fixture()
	a, _ := f.annot("Highlight", [4]int{10, 10, 100, 30}, "Annot")
	if got := findings(f.run("2"), "8.9.2.2", ""); len(got) != 0 {
		t.Errorf("visible: want no finding, got %v", got)
	}
	a.Set("F", object.Integer(1)) // corpus 8.9.2.2-t01-fail-a
	if got := findings(f.run("2"), "8.9.2.2", "Invisible"); len(got) != 1 {
		t.Errorf("invisible, tagged: want 1 finding, got %v", got)
	}
	if got := findings(f.run("1"), "8.9.2.2", ""); len(got) != 0 {
		t.Errorf("PDF/UA-1: want no 8.9.2.2 finding, got %v", got)
	}
	// Inside an Artifact element.
	g := ua2Fixture()
	art, artr := g.elem(g.docEl, g.docElRef, "Artifact")
	art.Set("NS", g.docEl.Get("NS"))
	b, _ := g.annot("Highlight", [4]int{10, 10, 100, 30}, "Annot")
	b.Set("F", object.Integer(1))
	// Move the annotation's element under the Artifact.
	k := g.docEl.Get("K").(object.Array)
	el := k[len(k)-1]
	g.docEl.Set("K", k[:len(k)-1])
	art.Set("K", object.Array{el})
	g.objs[el.(object.IndirectRef).Number].Value.(*object.Dictionary).Set("P", artr)
	if got := findings(g.run("2"), "8.9.2.2", ""); len(got) != 0 {
		t.Errorf("invisible inside an Artifact element: want no finding, got %v", got)
	}
}

// TestUA2FileAttachmentAFRelationship is ISO 14289-2 8.9.2.4.10 (veraPDF
// 8.9.2.4.10-t01): a file attachment annotation's file specification carries
// /AFRelationship.
func TestUA2FileAttachmentAFRelationship(t *testing.T) {
	f := ua2Fixture()
	a, _ := f.annot("FileAttachment", [4]int{10, 10, 100, 30}, "Annot")
	a.Set("Contents", str("x"))
	if got := findings(f.run("2"), "8.9.2.4.10", ""); len(got) != 0 {
		t.Errorf("no /FS: want no finding, got %v", got)
	}
	fs := object.NewDictionary(object.Entry{Key: "Type", Value: object.Name("Filespec")}, object.Entry{Key: "F", Value: str("a.txt")}, object.Entry{Key: "UF", Value: str("a.txt")})
	a.Set("FS", f.add(fs))
	if got := findings(f.run("2"), "8.9.2.4.10", "AFRelationship"); len(got) != 1 { // corpus 8.9.2.4.10-t01-fail-a
		t.Errorf("/FS without /AFRelationship: want 1 finding, got %v", got)
	}
	if got := findings(f.run("1"), "8.9.2.4.10", ""); len(got) != 0 {
		t.Errorf("PDF/UA-1: want no 8.9.2.4.10 finding, got %v", got)
	}
	fs.Set("AFRelationship", object.Name("Supplement"))
	if got := findings(f.run("2"), "8.9.2.4.10", ""); len(got) != 0 {
		t.Errorf("/FS with /AFRelationship: want no finding, got %v", got)
	}
}

// widgetUnder adds a field widget to the page, enclosed through an OBJR by el.
func widgetUnder(f *uaFixture, el *object.Dictionary, name string) *object.Dictionary {
	a := &object.Dictionary{}
	aref := f.add(a)
	a.Set("Type", object.Name("Annot"))
	a.Set("Subtype", object.Name("Widget"))
	a.Set("FT", object.Name("Tx"))
	a.Set("T", str(name))
	a.Set("TU", str(name))
	a.Set("Rect", object.Array{object.Integer(10), object.Integer(10), object.Integer(100), object.Integer(30)})
	a.Set("F", object.Integer(4))
	a.Set("P", f.pageRef)
	a.Set("StructParent", object.Integer(1))
	annots, _ := f.page.Get("Annots").(object.Array)
	f.page.Set("Annots", append(annots, aref))
	k, _ := el.Get("K").(object.Array)
	el.Set("K", append(k, object.NewDictionary(object.Entry{Key: "Type", Value: object.Name("OBJR")}, object.Entry{Key: "Obj", Value: aref})))
	return a
}

// TestUA2FormOneWidget is ISO 14289-2 8.10.1 (veraPDF 8.10.1-t02): a Form
// structure element encloses at most one widget annotation.
func TestUA2FormOneWidget(t *testing.T) {
	f := ua2Fixture()
	form, _ := f.elem(f.docEl, f.docElRef, "Form")
	widgetUnder(f, form, "a").Set("Contents", str("a"))
	if got := findings(f.run("2"), "8.10.1", ""); len(got) != 0 {
		t.Errorf("one widget: want no finding, got %v", got)
	}
	widgetUnder(f, form, "b").Set("Contents", str("b")) // corpus 8.10.1-t02-fail-a
	if got := findings(f.run("2"), "8.10.1", "2 widget"); len(got) != 1 {
		t.Errorf("two widgets: want 1 finding, got %v", got)
	}
	if got := findings(f.run("1"), "8.10.1", ""); len(got) != 0 {
		t.Errorf("PDF/UA-1: want no 8.10.1 finding, got %v", got)
	}
}

// TestUA2WidgetDescription is ISO 14289-2 8.10.2.3 (veraPDF 8.10.2.3-t01): a
// field's widget annotation in the structure tree has /Contents, unless its
// Form element labels it with a non-empty Lbl.
func TestUA2WidgetDescription(t *testing.T) {
	count := func(f *uaFixture) int { return len(findings(f.run("2"), "8.10.2.3", "")) }

	f := ua2Fixture()
	form, formRef := f.elem(f.docEl, f.docElRef, "Form")
	w := widgetUnder(f, form, "a") // corpus 8.10.2.3-t01-fail-a
	if count(f) != 1 {
		t.Error("field widget without /Contents or label: want 1 finding")
	}
	if got := findings(f.run("1"), "8.10.2.3", ""); len(got) != 0 {
		t.Errorf("PDF/UA-1: want no 8.10.2.3 finding, got %v", got)
	}
	w.Set("Contents", str(""))
	if count(f) != 0 {
		t.Error("an empty /Contents is present (veraPDF: Contents != null): want no finding")
	}
	w.Delete("Contents")
	lbl, _ := f.elem(form, formRef, "Lbl")
	if count(f) != 1 {
		t.Error("an empty Lbl labels nothing: want 1 finding")
	}
	lbl.Set("K", object.Integer(0))
	if count(f) != 0 {
		t.Error("a Lbl with content: want no finding")
	}
	// A widget that is no field's — no /T, no /Parent — is not held to it.
	g := ua2Fixture()
	gform, _ := g.elem(g.docEl, g.docElRef, "Form")
	gw := widgetUnder(g, gform, "a")
	gw.Delete("T")
	if count(g) != 0 {
		t.Error("widget with neither /T nor /Parent: want no finding")
	}
	gw.Set("Parent", g.add(object.NewDictionary(object.Entry{Key: "FT", Value: object.Name("Tx")}, object.Entry{Key: "T", Value: str("p")})))
	if count(g) != 1 {
		t.Error("widget of a parent field: want 1 finding")
	}
}

// TestUA2EmbeddedFileDesc is ISO 14289-2 8.14.1 (veraPDF 8.14.1-t01): a file
// specification in the EmbeddedFiles name tree carries /Desc.
func TestUA2EmbeddedFileDesc(t *testing.T) {
	f := ua2Fixture()
	fs := object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("Filespec")},
		object.Entry{Key: "F", Value: str("a.txt")},
		object.Entry{Key: "UF", Value: str("a.txt")},
		object.Entry{Key: "EF", Value: object.NewDictionary(object.Entry{Key: "F", Value: f.add(&object.Stream{Data: []byte("x")})})},
	)
	fsRef := f.add(fs)
	f.cat.Set("Names", object.NewDictionary(object.Entry{Key: "EmbeddedFiles", Value: object.NewDictionary(
		object.Entry{Key: "Kids", Value: object.Array{f.add(object.NewDictionary(object.Entry{Key: "Names", Value: object.Array{str("a.txt"), fsRef}}))}})}))
	if got := findings(f.run("2"), "8.14.1", "Desc"); len(got) != 1 { // corpus 8.14.1-t01-fail-a
		t.Errorf("no /Desc: want 1 finding, got %v", got)
	}
	if got := findings(f.run("1"), "8.14.1", ""); len(got) != 0 {
		t.Errorf("PDF/UA-1: want no 8.14.1 finding, got %v", got)
	}
	fs.Set("Desc", str("the data"))
	if got := findings(f.run("2"), "8.14.1", ""); len(got) != 0 {
		t.Errorf("with /Desc: want no finding, got %v", got)
	}
	// A file specification outside the name tree is not held to it.
	g := ua2Fixture()
	a, _ := g.annot("FileAttachment", [4]int{10, 10, 100, 30}, "Annot")
	a.Set("FS", g.add(object.NewDictionary(object.Entry{Key: "F", Value: str("b")}, object.Entry{Key: "UF", Value: str("b")}, object.Entry{Key: "AFRelationship", Value: object.Name("Data")})))
	if got := findings(g.run("2"), "8.14.1", ""); len(got) != 0 {
		t.Errorf("file specification outside EmbeddedFiles: want no finding, got %v", got)
	}
}

// TestUA2ActionChainTerminates: an action's /Next is the file's to make, and an
// array that holds itself, or a chain that closes on itself, is followed once.
func TestUA2ActionChainTerminates(t *testing.T) {
	f := ua2Fixture()
	act := object.NewDictionary(object.Entry{Key: "S", Value: object.Name("GoTo")}, object.Entry{Key: "D", Value: object.Array{f.pageRef, object.Name("Fit")}})
	actRef := f.add(act)
	loop := f.add(nil)
	f.objs[loop.Number].Value = object.Array{loop, actRef}
	act.Set("Next", loop)
	a, _ := f.annot("Link", [4]int{10, 10, 100, 30}, "Link")
	a.Set("A", actRef)
	// A budget the walk spends only if it goes round the loop: then the run
	// is stopped and nothing is reported, rather than the test never ending.
	v := f.view()
	v.Run, v.Cancel = core.NewMeteredRun(&core.Recorder{}, 1_000_000, core.Canceler{})
	if got := findings(validateView(v, "2"), "8.8", "GoTo"); len(got) != 1 {
		t.Errorf("want the one GoTo action reported once, got %v", got)
	}
}

// TestUA2ListNumberingDeepNesting: a list inherits ListNumbering from its
// ancestors, and each of n nested labelled lists asks its whole ancestry. Read
// once per element, that is linear; read per list it is quadratic — 800
// million steps here, which the run's work meter stops (and the rule then
// reports nothing) long before it would finish.
func TestUA2ListNumberingDeepNesting(t *testing.T) {
	const n = 20000
	f := ua2Fixture()
	parent, parentRef := f.docEl, f.docElRef
	for i := 0; i < n; i++ {
		l, lr := f.elem(parent, parentRef, "L")
		li, lir := f.elem(l, lr, "LI")
		f.elem(li, lir, "Lbl", object.Integer(i))
		parent, parentRef = f.elem(li, lir, "LBody")
	}
	v := f.view()
	v.Run, v.Cancel = core.NewMeteredRun(&core.Recorder{}, 50_000_000, core.Canceler{})
	if got := findings(validateView(v, "2"), "8.2.5.25", "ListNumbering"); len(got) != n {
		t.Errorf("want %d findings, one per list, got %d", n, len(got))
	}
}
