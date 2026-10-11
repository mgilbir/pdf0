package pdfua

import (
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// The tests in this file pin the rule boundaries the veraPDF PDF/UA corpus
// drew when its pass files were first held to the validator (docs audit
// 2026-10-11 D12). Each case is the shape of a corpus pass or fail file,
// reduced to the entries the rule reads, and each runs the whole validator
// for the part, so a rule moved, renamed or gated by part is still found.

// uaFixture is a one-page tagged document: a catalog, a page tree, a page with
// one content stream, and a structure tree whose root holds a Document
// element. Objects are numbered from 1 in the order they are added.
type uaFixture struct {
	objs                         map[int]*object.IndirectObject
	next                         int
	cat, page, res, root, docEl  *object.Dictionary
	content                      *object.Stream
	pageRef, docElRef, structRef object.IndirectRef
}

func newUAFixture(content string) *uaFixture {
	f := &uaFixture{objs: map[int]*object.IndirectObject{}}
	f.cat = &object.Dictionary{}
	catRef := f.add(f.cat)
	f.cat.Set("Type", object.Name("Catalog"))
	f.cat.Set("MarkInfo", object.NewDictionary(object.Entry{Key: "Marked", Value: object.Boolean(true)}))
	f.cat.Set("ViewerPreferences", object.NewDictionary(object.Entry{Key: "DisplayDocTitle", Value: object.Boolean(true)}))
	pages := &object.Dictionary{}
	pagesRef := f.add(pages)
	f.cat.Set("Pages", pagesRef)
	f.page = &object.Dictionary{}
	f.pageRef = f.add(f.page)
	pages.Set("Type", object.Name("Pages"))
	pages.Set("Kids", object.Array{f.pageRef})
	pages.Set("Count", object.Integer(1))
	f.res = &object.Dictionary{}
	f.page.Set("Type", object.Name("Page"))
	f.page.Set("Parent", pagesRef)
	f.page.Set("MediaBox", object.Array{object.Integer(0), object.Integer(0), object.Integer(612), object.Integer(792)})
	f.page.Set("Resources", f.res)
	f.page.Set("Tabs", object.Name("S"))
	f.content = &object.Stream{Data: []byte(content)}
	f.page.Set("Contents", f.add(f.content))
	f.root = &object.Dictionary{}
	f.structRef = f.add(f.root)
	f.cat.Set("StructTreeRoot", f.structRef)
	f.root.Set("Type", object.Name("StructTreeRoot"))
	f.docEl = &object.Dictionary{}
	f.docElRef = f.add(f.docEl)
	f.docEl.Set("S", object.Name("Document"))
	f.docEl.Set("P", f.structRef)
	f.root.Set("K", object.Array{f.docElRef})
	_ = catRef
	return f
}

func (f *uaFixture) add(v object.Object) object.IndirectRef {
	f.next++
	f.objs[f.next] = &object.IndirectObject{Number: f.next, Value: v}
	return object.IndirectRef{Number: f.next}
}

// elem adds a structure element of type s under parent and returns it.
func (f *uaFixture) elem(parent *object.Dictionary, parentRef object.IndirectRef, s string, kids ...object.Object) (*object.Dictionary, object.IndirectRef) {
	e := &object.Dictionary{}
	ref := f.add(e)
	e.Set("S", object.Name(s))
	e.Set("P", parentRef)
	e.Set("Pg", f.pageRef)
	if len(kids) > 0 {
		e.Set("K", object.Array(kids))
	}
	k, _ := parent.Get("K").(object.Array)
	parent.Set("K", append(k, ref))
	return e, ref
}

// annot adds an annotation to the page, enclosed (through an OBJR) by a new
// structure element of type se under the Document element, and returns both.
func (f *uaFixture) annot(subtype string, rect [4]int, se string) (a, el *object.Dictionary) {
	a = &object.Dictionary{}
	aref := f.add(a)
	a.Set("Type", object.Name("Annot"))
	a.Set("Subtype", object.Name(subtype))
	a.Set("Rect", object.Array{object.Integer(rect[0]), object.Integer(rect[1]), object.Integer(rect[2]), object.Integer(rect[3])})
	a.Set("F", object.Integer(4))
	a.Set("P", f.pageRef)
	a.Set("StructParent", object.Integer(1))
	annots, _ := f.page.Get("Annots").(object.Array)
	f.page.Set("Annots", append(annots, aref))
	objr := object.NewDictionary(object.Entry{Key: "Type", Value: object.Name("OBJR")}, object.Entry{Key: "Obj", Value: aref})
	el, _ = f.elem(f.docEl, f.docElRef, se, objr)
	return a, el
}

func (f *uaFixture) view() core.View {
	tr := &object.Dictionary{}
	tr.Set("Root", object.IndirectRef{Number: 1})
	return mkView(f.objs, tr)
}

func (f *uaFixture) run(part string) []Violation { return validateView(f.view(), part) }

// findings returns the messages of the findings under clause that contain sub.
func findings(vs []Violation, clause, sub string) []string {
	var out []string
	for _, v := range vs {
		if v.Clause == clause && strings.Contains(v.Message, sub) {
			out = append(out, v.Message)
		}
	}
	return out
}

func str(s string) object.String { return object.String{Value: []byte(s)} }

// TestUAAnnotOutsideCropBoxIsExempt: an annotation whose rectangle lies outside
// the page's crop box is never seen, and the PDF/UA-1 description rules exempt
// it (veraPDF 7.18.1-t02/t03, 7.18.5-t02; corpus 7.18.1-t02-pass-d, -h and
// 7.18.1-t03-pass-d). The same annotation inside the box is still held to them.
func TestUAAnnotOutsideCropBoxIsExempt(t *testing.T) {
	for _, tc := range []struct {
		subtype, se, want string
	}{
		{"Highlight", "Annot", "has no alternate description"},
		{"Widget", "Form", "neither a field description"},
		{"Link", "Link", "Link annotation has no alternate description"},
	} {
		f := newUAFixture("")
		f.cat.Set("Lang", str("en"))
		f.page.Set("CropBox", object.Array{object.Integer(0), object.Integer(0), object.Integer(612), object.Integer(792)})
		a, _ := f.annot(tc.subtype, [4]int{800, 800, 800, 800}, tc.se)
		descriptionFindings := func() []string {
			vs := f.run("1")
			return append(findings(vs, "7.18.1", tc.want), findings(vs, "7.18.5", tc.want)...)
		}
		if got := descriptionFindings(); len(got) != 0 {
			t.Errorf("%s outside the crop box: want no description finding, got %v", tc.subtype, got)
		}
		// The same document, the annotation now on the page.
		a.Set("Rect", object.Array{object.Integer(10), object.Integer(10), object.Integer(100), object.Integer(30)})
		if got := descriptionFindings(); len(got) == 0 {
			t.Errorf("%s inside the crop box without a description: want a finding, got none", tc.subtype)
		}
	}
}

// TestUAAnnotAltOnEnclosingElement: an annotation's /Alt is its enclosing
// structure element's (ISO 32000-1 Table 323; veraPDF PDAnnotation.getAlt),
// which is where the corpus pass files put it (7.18.1-t02-pass-a, -e and
// 7.18.1-t03-pass-b).
func TestUAAnnotAltOnEnclosingElement(t *testing.T) {
	for _, tc := range []struct{ subtype, se, want string }{
		{"Highlight", "Annot", "has no alternate description"},
		{"Widget", "Form", "neither a field description"},
	} {
		f := newUAFixture("")
		f.cat.Set("Lang", str("en"))
		_, el := f.annot(tc.subtype, [4]int{10, 10, 100, 30}, tc.se)
		if len(findings(f.run("1"), "7.18.1", tc.want)) == 0 {
			t.Fatalf("%s with no description anywhere: want a finding (control)", tc.subtype)
		}
		el.Set("Alt", str("a description"))
		if got := findings(f.run("1"), "7.18.1", tc.want); len(got) != 0 {
			t.Errorf("%s whose enclosing <%s> has /Alt: want no finding, got %v", tc.subtype, tc.se, got)
		}
	}
}

// TestUA2DoesNotRequireAnnotationContents: ISO 14289-2 8.9.2.4.2 says a Link
// annotation "should" include /Contents, and 8.9.2.3 lets markup annotations
// omit it; the veraPDF PDF/UA-2 profile has no such rule, and the corpus
// passes Links without one (8.2.5.20-t02-pass-a, -b). The PDF/UA-1 rules
// (7.18.1, 7.18.5) do not apply to part 2.
func TestUA2DoesNotRequireAnnotationContents(t *testing.T) {
	f := newUAFixture("")
	f.cat.Set("Lang", str("en"))
	f.annot("Link", [4]int{10, 10, 100, 30}, "Link")
	f.annot("Highlight", [4]int{10, 40, 100, 60}, "Annot")
	f.annot("Widget", [4]int{10, 70, 100, 90}, "Form")
	if got := findings(f.run("1"), "7.18.5", "no alternate description"); len(got) == 0 {
		t.Fatal("PDF/UA-1: a Link without /Contents must be reported (control)")
	}
	vs := f.run("2")
	got := append(findings(vs, "7.18.5", "alternate description"), findings(vs, "7.18.1", "alternate description")...)
	got = append(got, findings(vs, "7.18.1", "field description")...)
	if len(got) != 0 {
		t.Errorf("PDF/UA-2: want no annotation-description findings, got %v", got)
	}
}

// TestUAFigureEmptyActualText: a Figure passes with any /ActualText, the empty
// string included (veraPDF 7.3-t01 "ActualText != null"; corpus
// 7.3-t01-pass-c), but needs a non-empty /Alt when it has no /ActualText.
func TestUAFigureEmptyActualText(t *testing.T) {
	f := newUAFixture("")
	f.cat.Set("Lang", str("en"))
	fig, _ := f.elem(f.docEl, f.docElRef, "Figure")
	fig.Set("ActualText", str(""))
	if got := findings(f.run("1"), "7.3", "alternate text"); len(got) != 0 {
		t.Errorf("Figure with an empty /ActualText: want no finding, got %v", got)
	}
	fig.Delete("ActualText")
	fig.Set("Alt", str(""))
	if got := findings(f.run("1"), "7.3", "alternate text"); len(got) == 0 {
		t.Error("Figure with only an empty /Alt: want a finding")
	}
}

// TestUAFormulaAlt: a Formula needs an /Alt or an /ActualText (ISO 14289-1
// 7.7; veraPDF 7.7-t01; corpus 7.7-t01-fail-a, -b), exactly as a Figure does.
func TestUAFormulaAlt(t *testing.T) {
	f := newUAFixture("")
	f.cat.Set("Lang", str("en"))
	formula, _ := f.elem(f.docEl, f.docElRef, "Formula")
	if got := findings(f.run("1"), "7.7", "Formula"); len(got) == 0 {
		t.Error("Formula with neither /Alt nor /ActualText: want a 7.7 finding")
	}
	formula.Set("Alt", str("x squared"))
	if got := findings(f.run("1"), "7.7", "Formula"); len(got) != 0 {
		t.Errorf("Formula with /Alt: want no finding, got %v", got)
	}
}

// TestUACharMappingAdobeCollections: a composite font whose descendant uses one
// of the Adobe-GB1, -CNS1, -Japan1 or -Korea1 character collections maps to
// Unicode through Adobe's published CMaps, which ISO 14289-1 7.21.7 admits
// through ISO 19005-2 6.2.11.7.2 (corpus 7.21.7-t01-pass-a). Adobe-Identity
// has no such mapping.
func TestUACharMappingAdobeCollections(t *testing.T) {
	for _, tc := range []struct {
		ordering string
		flagged  bool
	}{{"Japan1", false}, {"Identity", true}} {
		f := newUAFixture("/P <</MCID 0>> BDC BT /F1 12 Tf <0001> Tj ET EMC")
		f.cat.Set("Lang", str("en"))
		csi := object.NewDictionary(
			object.Entry{Key: "Registry", Value: str("Adobe")},
			object.Entry{Key: "Ordering", Value: str(tc.ordering)},
			object.Entry{Key: "Supplement", Value: object.Integer(0)})
		desc := object.NewDictionary(
			object.Entry{Key: "Type", Value: object.Name("Font")},
			object.Entry{Key: "Subtype", Value: object.Name("CIDFontType0")},
			object.Entry{Key: "CIDSystemInfo", Value: csi})
		font := object.NewDictionary(
			object.Entry{Key: "Type", Value: object.Name("Font")},
			object.Entry{Key: "Subtype", Value: object.Name("Type0")},
			object.Entry{Key: "Encoding", Value: object.Name("Identity-H")},
			object.Entry{Key: "DescendantFonts", Value: object.Array{f.add(desc)}})
		f.res.Set("Font", object.NewDictionary(object.Entry{Key: "F1", Value: f.add(font)}))
		got := findings(f.run("1"), "7.2", "cannot be mapped to Unicode")
		if (len(got) != 0) != tc.flagged {
			t.Errorf("Adobe-%s Identity-H font without ToUnicode: flagged=%v, want %v (%v)", tc.ordering, len(got) != 0, tc.flagged, got)
		}
	}
}

// TestUALangTagSyntax is the veraPDF CosLang rule (7.2-t29, 8.4.4-t02): a /Lang
// in the catalog, a structure element or a marked-content property list must
// match RFC 3066's Language-Tag, 1*8ALPHA *("-" 1*8alphanum). A one-letter
// primary subtag is one (corpus 7.2-t29-pass-b, -d, -f); an empty value is
// not (7.2-t29-fail-n, -o, -p); and a property list's /Lang is held to it
// as much as the catalog's (7.2-t29-fail-g, -h, -i).
func TestUALangTagSyntax(t *testing.T) {
	const notTag = "not a valid language identifier"
	for _, part := range []string{"1", "2"} {
		for _, tc := range []struct {
			name, catLang, elemLang, content string
			bad                              bool
		}{
			{"one-letter primary subtag", "p", "p-pt", "", false},
			{"empty catalog /Lang", "", "", "", true},
			{"empty structure element /Lang", "en", "", "", true},
			{"property list /Lang of 1-pt", "en", "en", "/Span <</Lang (1-pt)>> BDC EMC", true},
			{"empty property list /Lang", "en", "en", "/Span <</Lang ()>> BDC EMC", true},
			{"valid property list /Lang", "en", "en", "/Span <</Lang (pt-PT)>> BDC EMC", false},
			{"named property list /Lang", "en", "en", "/Span /PL0 BDC EMC", true},
		} {
			f := newUAFixture(tc.content)
			f.cat.Set("Lang", str(tc.catLang))
			p, _ := f.elem(f.docEl, f.docElRef, "P")
			p.Set("Lang", str(tc.elemLang))
			f.res.Set("Properties", object.NewDictionary(object.Entry{Key: "PL0", Value: object.NewDictionary(object.Entry{Key: "Lang", Value: str("portugues-pt")})}))
			got := findings(f.run(part), "7.2", notTag)
			if (len(got) != 0) != tc.bad {
				t.Errorf("part %s, %s: flagged=%v, want %v (%v)", part, tc.name, len(got) != 0, tc.bad, got)
			}
		}
	}
}

// TestUA1LanguageDeterminable: ISO 14289-1 7.2 requires the natural language
// to be declared, not a catalog /Lang as such. Without one, veraPDF asks of
// each text-bearing object whether its language can be determined some other
// way (7.2-t02, t21..t25, t30..t34); the corpus's pass files declare it on
// structure elements and marked content (7.2-t21-pass-b ... 7.2-t34-pass-d)
// and its fail files leave one object without it (7.2-t02-fail-a ...
// 7.2-t34-fail-a).
func TestUA1LanguageDeterminable(t *testing.T) {
	const nl = "natural language"
	type build func(f *uaFixture)
	cases := []struct {
		name    string
		content string
		build   build
		bad     bool
	}{
		{"text in an element with /Lang", "/P <</MCID 0>> BDC BT (x) Tj ET EMC", func(f *uaFixture) {
			p, _ := f.elem(f.docEl, f.docElRef, "P", object.Integer(0))
			p.Set("Lang", str("en"))
		}, false},
		{"text in an element whose ancestor has /Lang", "/P <</MCID 0>> BDC BT (x) Tj ET EMC", func(f *uaFixture) {
			f.docEl.Set("Lang", str("en"))
			f.elem(f.docEl, f.docElRef, "P", object.Integer(0))
		}, false},
		{"text in a marked-content sequence with /Lang", "/Span <</Lang (en)>> BDC BT (x) Tj ET EMC", nil, false},
		{"text in an element with no /Lang anywhere", "/P <</MCID 0>> BDC BT (x) Tj ET EMC", func(f *uaFixture) {
			f.elem(f.docEl, f.docElRef, "P", object.Integer(0))
		}, true},
		{"text outside marked content", "BT (x) Tj ET", nil, true},
		{"element /Alt with /Lang on an ancestor", "", func(f *uaFixture) {
			f.docEl.Set("Lang", str("en"))
			fig, _ := f.elem(f.docEl, f.docElRef, "Figure")
			fig.Set("Alt", str("a picture"))
		}, false},
		{"element /ActualText with no /Lang", "", func(f *uaFixture) {
			s, _ := f.elem(f.docEl, f.docElRef, "Span")
			s.Set("ActualText", str("text"))
		}, true},
		{"element /E with no /Lang", "", func(f *uaFixture) {
			s, _ := f.elem(f.docEl, f.docElRef, "Span")
			s.Set("E", str("expansion"))
		}, true},
		{"Span marked content /ActualText with /Lang", "/Span <</ActualText (t) /Lang (en)>> BDC EMC", nil, false},
		{"Span marked content /ActualText without /Lang", "/Span <</ActualText (t)>> BDC EMC", nil, true},
		{"outline entries", "", func(f *uaFixture) {
			item := &object.Dictionary{}
			itemRef := f.add(item)
			item.Set("Title", str("Chapter"))
			ol := object.NewDictionary(object.Entry{Key: "First", Value: itemRef}, object.Entry{Key: "Last", Value: itemRef}, object.Entry{Key: "Count", Value: object.Integer(1)})
			f.cat.Set("Outlines", f.add(ol))
		}, true},
		{"an empty outline tree", "", func(f *uaFixture) {
			f.cat.Set("Outlines", f.add(object.NewDictionary(object.Entry{Key: "Count", Value: object.Integer(0)})))
		}, false},
		{"annotation /Contents whose element has /Lang", "", func(f *uaFixture) {
			a, el := f.annot("Link", [4]int{10, 10, 100, 30}, "Link")
			a.Set("Contents", str("a link"))
			el.Set("Lang", str("en"))
		}, false},
		{"annotation /Contents whose element has none", "", func(f *uaFixture) {
			a, _ := f.annot("Link", [4]int{10, 10, 100, 30}, "Link")
			a.Set("Contents", str("a link"))
		}, true},
		{"field /TU whose element has none", "", func(f *uaFixture) {
			a, _ := f.annot("Widget", [4]int{10, 10, 100, 30}, "Form")
			a.Set("FT", object.Name("Tx"))
			a.Set("T", str("name"))
			a.Set("TU", str("Your name"))
			f.cat.Set("AcroForm", object.NewDictionary(object.Entry{Key: "Fields", Value: object.Array{f.page.Get("Annots").(object.Array)[0]}}))
		}, true},
		{"metadata language alternative with only x-default", "", func(f *uaFixture) {
			f.cat.Set("Metadata", f.add(&object.Stream{Data: []byte(xmpTitle(`<rdf:li xml:lang="x-default">T</rdf:li>`))}))
		}, true},
		{"metadata language alternative with x-default and en", "", func(f *uaFixture) {
			f.cat.Set("Metadata", f.add(&object.Stream{Data: []byte(xmpTitle(`<rdf:li xml:lang="x-default">T</rdf:li><rdf:li xml:lang="en">T</rdf:li>`))}))
		}, false},
	}
	for _, tc := range cases {
		f := newUAFixture(tc.content)
		f.res.Set("Font", object.NewDictionary(object.Entry{Key: "F1", Value: f.add(object.NewDictionary())}))
		if tc.build != nil {
			tc.build(f)
		}
		vs := f.run("1")
		got := findings(vs, "7.2", nl)
		if (len(got) != 0) != tc.bad {
			t.Errorf("%s: flagged=%v, want %v (%v)", tc.name, len(got) != 0, tc.bad, got)
		}
		if old := findings(vs, "7.2", "default language"); len(old) != 0 {
			t.Errorf("%s: PDF/UA-1 does not require a catalog /Lang as such, got %v", tc.name, old)
		}
		// With a catalog /Lang every object's language is determined.
		f.cat.Set("Lang", str("en"))
		if got := findings(f.run("1"), "7.2", nl); len(got) != 0 {
			t.Errorf("%s with a catalog /Lang: want no finding, got %v", tc.name, got)
		}
	}

	// PDF/UA-2 does require the catalog /Lang (ISO 14289-2 8.4.4, veraPDF
	// 8.4.4-t01), whatever the structure says.
	f := newUAFixture("/P <</MCID 0>> BDC BT (x) Tj ET EMC")
	p, _ := f.elem(f.docEl, f.docElRef, "P", object.Integer(0))
	p.Set("Lang", str("en"))
	if got := findings(f.run("2"), "7.2", "default language"); len(got) == 0 {
		t.Error("PDF/UA-2 without a catalog /Lang: want a finding")
	}
}

func xmpTitle(items string) string {
	return `<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">` +
		`<rdf:Description rdf:about="" xmlns:pdfuaid="http://www.aiim.org/pdfua/ns/id/" xmlns:dc="http://purl.org/dc/elements/1.1/" pdfuaid:part="1">` +
		`<dc:title><rdf:Alt>` + items + `</rdf:Alt></dc:title></rdf:Description></rdf:RDF></x:xmpmeta>`
}

// tableFixture builds a Table of rows; each cell is "TH", "TD", or one of
// those followed by attributes: "TH#id" gives the cell an /ID, "TH!Row" a
// Scope ("TH!" a Scope of the empty name), "TD>a,b" a /Headers list.
func tableFixture(rows ...[]string) *uaFixture {
	f := newUAFixture("")
	f.cat.Set("Lang", str("en"))
	table, tref := f.elem(f.docEl, f.docElRef, "Table")
	for _, row := range rows {
		tr, trref := f.elem(table, tref, "TR")
		for _, spec := range row {
			typ := spec[:2]
			cell, _ := f.elem(tr, trref, typ)
			rest := spec[2:]
			attr := object.NewDictionary(object.Entry{Key: "O", Value: object.Name("Table")})
			switch {
			case strings.HasPrefix(rest, "#"):
				cell.Set("ID", str(rest[1:]))
			case strings.HasPrefix(rest, "!"):
				attr.Set("Scope", object.Name(rest[1:]))
			case strings.HasPrefix(rest, ">"):
				var hs object.Array
				for _, h := range strings.Split(rest[1:], ",") {
					hs = append(hs, str(h))
				}
				attr.Set("Headers", hs)
			}
			cell.Set("A", attr)
		}
	}
	return f
}

// TestUATableHeaderAssociation is veraPDF's table-header rule (7.5-t01/t02,
// 8.2.5.26-t05/t06): in a regular table where some TH has no Scope, every TD
// but a stub head must name its headers by /Headers, each the /ID of a TH in
// the table; in PDF/UA-2 it may instead find a header by position (a TH above
// in the column, or to the left in the row, whose Scope — or, without one,
// whose slot — points its way). The rule is about the data cells: a TH
// without Scope or /ID is no defect while every TD names its headers (corpus
// 7.5-t01-pass-e, 8.2.5.26-t05-pass-e).
func TestUATableHeaderAssociation(t *testing.T) {
	for _, tc := range []struct {
		name string
		rows [][]string
		bad1 bool // PDF/UA-1
		bad2 bool // PDF/UA-2
	}{
		{"every TH has a Scope", [][]string{{"TH!Column", "TH!Column"}, {"TD", "TD"}}, false, false},
		{"TDs name their headers; one TH has no Scope or ID",
			[][]string{{"TH", "TH#h1", "TH#h2"}, {"TH#r1", "TD>r1,h1", "TD>r1,h2"}}, false, false},
		{"a TD names no header", [][]string{{"TH#h1", "TH#h2"}, {"TD>h1", "TD"}}, true, false},
		{"a TD names an unknown header", [][]string{{"TH#h1", "TH#h2"}, {"TD>h1", "TD>h9"}}, true, false},
		{"no Scope, no Headers, headers by position", [][]string{{"TH", "TH"}, {"TD", "TD"}}, true, false},
		{"no header anywhere for a TD", [][]string{{"TD", "TD"}, {"TD", "TH"}}, true, true},
		// PDF/UA-1 does not look for headers by position at all.
		{"column headers scoped, row headers not", [][]string{{"TH", "TH!Column", "TH!Column"}, {"TH", "TD", "TD"}, {"TH", "TD", "TD"}}, true, false},
		// A stub head — a TD where a header row crosses a header column — needs
		// no header, wherever it is (PDFUA-Reference-08 has two in row 3).
		{"a stub head below the first row", [][]string{{"TH#r1", "TD>r1"}, {"TD", "TH#c1"}, {"TH#r2", "TD>r2,c1"}}, false, false},
		{"a TD in a header row but not a header column", [][]string{{"TH#r1", "TD>r1"}, {"TH#r2", "TD"}, {"TH#r3", "TD>r3"}}, true, false},
		// A Scope of the empty name is a Scope (no default applies) that names
		// no direction (corpus 8.2.5.26-t05-fail-a, 7.5-t01-fail-a).
		{"headers whose Scope is the empty name", [][]string{{"TH", "TH!", "TH!"}, {"TH!", "TD", "TD"}, {"TH!", "TD", "TD"}}, true, true},
	} {
		f := tableFixture(tc.rows...)
		for part, want := range map[string]bool{"1": tc.bad1, "2": tc.bad2} {
			got := findings(f.run(part), "7.5", "")
			if (len(got) != 0) != want {
				t.Errorf("part %s, %s: flagged=%v, want %v (%v)", part, tc.name, len(got) != 0, want, got)
			}
		}
	}
}

// TestUARealContentImage: an image painted outside any marked-content sequence
// is content that is neither tagged nor an artifact (ISO 14289-1 7.1; veraPDF
// 7.1-t03 over SESimpleContentItem; corpus 7.1-t03-fail-a), as is a painted
// path. A clipping path that paints nothing is not content.
func TestUARealContentImage(t *testing.T) {
	const msg = "neither tagged nor marked as an /Artifact"
	for _, tc := range []struct {
		name, content string
		bad           bool
	}{
		{"image outside marked content", "q 10 0 0 10 0 0 cm /Im0 Do Q", true},
		{"image in an artifact", "/Artifact BMC q 10 0 0 10 0 0 cm /Im0 Do Q EMC", false},
		{"image in tagged content", "/Figure <</MCID 0>> BDC q /Im0 Do Q EMC", false},
		{"filled path outside marked content", "0 0 10 10 re f", true},
		{"clipping path only", "q 0 0 10 10 re W n Q", false},
		{"inline image outside marked content", "BI /W 1 /H 1 /BPC 8 /CS /G ID \x00 EI", true},
		// Not reported, though veraPDF does: PDFUA-Reference-06 paints its
		// background this way (see checkUARealContent).
		{"shading outside marked content", "/Sh0 sh", false},
	} {
		f := newUAFixture(tc.content)
		f.cat.Set("Lang", str("en"))
		img := &object.Stream{Data: []byte{0}}
		img.Dict.Set("Type", object.Name("XObject"))
		img.Dict.Set("Subtype", object.Name("Image"))
		img.Dict.Set("Width", object.Integer(1))
		img.Dict.Set("Height", object.Integer(1))
		f.res.Set("XObject", object.NewDictionary(object.Entry{Key: "Im0", Value: f.add(img)}))
		got := findings(f.run("1"), "7.1", msg)
		if (len(got) != 0) != tc.bad {
			t.Errorf("%s: flagged=%v, want %v (%v)", tc.name, len(got) != 0, tc.bad, got)
		}
	}
}

// TestUA2LinkTargets: ISO 14289-2 8.2.5.20 puts Link annotations that target
// different locations in separate Link structure elements. veraPDF
// (8.2.5.20-t02) compares the structure destinations: a /Dest or GoTo /D whose
// first element is a structure element, or a GoTo /SD (corpus
// 8.2.5.20-t02-fail-a, -b and their pass siblings).
func TestUA2LinkTargets(t *testing.T) {
	for _, tc := range []struct {
		name    string
		viaSD   bool
		sameTwo bool
		bad     bool
	}{
		{"/Dest to two structure elements", false, false, true},
		{"/Dest to one structure element twice", false, true, false},
		{"GoTo /SD to two structure elements", true, false, true},
		{"GoTo /SD to one structure element twice", true, true, false},
	} {
		f := newUAFixture("")
		f.cat.Set("Lang", str("en"))
		_, h1 := f.elem(f.docEl, f.docElRef, "H1")
		_, h2 := f.elem(f.docEl, f.docElRef, "H2")
		target := []object.IndirectRef{h1, h2}
		if tc.sameTwo {
			target[1] = h1
		}
		link, linkRef := f.elem(f.docEl, f.docElRef, "Link")
		for i := 0; i < 2; i++ {
			a := &object.Dictionary{}
			aref := f.add(a)
			a.Set("Type", object.Name("Annot"))
			a.Set("Subtype", object.Name("Link"))
			a.Set("Rect", object.Array{object.Integer(10), object.Integer(10 + 30*i), object.Integer(100), object.Integer(30 + 30*i)})
			a.Set("StructParent", object.Integer(i))
			a.Set("Contents", str("go"))
			dest := object.Array{target[i], object.Name("Fit")}
			if tc.viaSD {
				a.Set("A", object.NewDictionary(
					object.Entry{Key: "S", Value: object.Name("GoTo")},
					object.Entry{Key: "D", Value: object.Array{f.pageRef, object.Name("Fit")}},
					object.Entry{Key: "SD", Value: dest}))
			} else {
				a.Set("Dest", dest)
			}
			annots, _ := f.page.Get("Annots").(object.Array)
			f.page.Set("Annots", append(annots, aref))
			k, _ := link.Get("K").(object.Array)
			link.Set("K", append(k, object.NewDictionary(object.Entry{Key: "Type", Value: object.Name("OBJR")}, object.Entry{Key: "Obj", Value: aref})))
		}
		_ = linkRef
		if got := findings(f.run("2"), "8.2.5.20", ""); (len(got) != 0) != tc.bad {
			t.Errorf("%s: flagged=%v, want %v (%v)", tc.name, len(got) != 0, tc.bad, got)
		}
		if got := findings(f.run("1"), "8.2.5.20", ""); len(got) != 0 {
			t.Errorf("%s: PDF/UA-1 has no such rule, got %v", tc.name, got)
		}
	}
}
