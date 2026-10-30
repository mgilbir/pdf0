package pdf0

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"unicode/utf16"

	"github.com/mgilbir/pdf0/object"
)

// Single-defect PDF/UA files, made here as the PDF/A ones are
// (pdfa_fixtures_verapdf_test.go): a tagged, declared document veraPDF passes
// at each part — a heading and a paragraph in an embedded font, under a
// Document element — given one thing wrong, or one thing that only looks
// wrong. Each records the clauses veraPDF fails it on at PDF/UA-1 and PDF/UA-2,
// and ValidatePDFUA and ValidatePDFUA2 must report exactly those — at part 2
// ISO/TS 32005's containment rules among them, named as veraPDF names them
// ("Table 5. Parent-Child"). They pin what the PDF/UA corpus does not test.

// uaFixture is one file: what is wrong with it, and veraPDF's clauses at
// PDF/UA-1 and PDF/UA-2 — none for a file it passes.
type uaFixture struct {
	name  string
	build func(u *uaDoc)
	want  [2][]string
}

// uaDoc is the base document being given its defect, read back from its
// bytes, with the objects a fixture reaches for.
type uaDoc struct {
	*Document
	part       int
	cat, page  *object.Dictionary
	pageRef    object.IndirectRef
	docElem    *object.Dictionary // the structure tree's Document element
	docElemRef object.IndirectRef
}

// uaBase is the conforming document for a part.
func uaBase(t *testing.T, part int) *uaDoc {
	t.Helper()
	d := NewDocument()
	if part == 1 {
		d.Version = "1.7"
	}
	accessiblePage(t, d)
	describeAndDeclare(t, d, part)
	rd := writtenAndRead(t, d)
	u := &uaDoc{Document: rd, part: part}
	u.cat = rd.ResolveDict(rd.Trailer.Get("Root"))
	pages := rd.ResolveDict(u.cat.Get("Pages"))
	u.pageRef = rd.Resolve(pages.Get("Kids")).(object.Array)[0].(object.IndirectRef)
	u.page = rd.ResolveDict(u.pageRef)
	root := rd.ResolveDict(u.cat.Get("StructTreeRoot"))
	u.docElemRef = root.Get("K").(object.IndirectRef)
	u.docElem = rd.ResolveDict(u.docElemRef)
	return u
}

// kid appends k to the Document element's kids.
func (u *uaDoc) kid(k object.Object) {
	kids, ok := u.Resolve(u.docElem.Get("K")).(object.Array)
	if !ok {
		kids = object.Array{u.docElem.Get("K")}
	}
	u.docElem.Set("K", append(slices.Clone(kids), k))
}

// elem adds a structure element of type tag under parent (the Document
// element when nil) and returns it and its reference.
func (u *uaDoc) elem(parent *object.Dictionary, parentRef object.IndirectRef, tag string, kids ...object.Object) (*object.Dictionary, object.IndirectRef) {
	if parent == nil {
		parent, parentRef = u.docElem, u.docElemRef
	}
	e := object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("StructElem")},
		object.Entry{Key: "S", Value: object.Name(tag)},
		object.Entry{Key: "P", Value: parentRef},
		object.Entry{Key: "Pg", Value: u.pageRef},
	)
	if len(kids) > 0 {
		e.Set("K", object.Array(kids))
	}
	ref := u.Add(e)
	if parent == u.docElem {
		u.kid(ref)
	} else {
		k, ok := u.Resolve(parent.Get("K")).(object.Array)
		if !ok && parent.Get("K") != nil {
			k = object.Array{parent.Get("K")}
		}
		parent.Set("K", append(slices.Clone(k), ref))
	}
	return e, ref
}

// ns20 puts e in the PDF 2.0 namespace, the Document element's, where the
// part has one: for the types only PDF 2.0 defines.
func (u *uaDoc) ns20(e *object.Dictionary) {
	if ns := u.docElem.Get("NS"); ns != nil {
		e.Set("NS", ns)
	}
}

// fenote adds an FENote, in the PDF 2.0 namespace, and a Span that may
// refer to it; refer and back say which /Ref entries are written.
func (u *uaDoc) fenote(refer, back bool, attrs *object.Dictionary) {
	note, noteRef := u.elem(nil, object.IndirectRef{}, "FENote")
	u.ns20(note)
	if attrs != nil {
		note.Set("A", attrs)
	}
	p, pRef := u.elem(nil, object.IndirectRef{}, "P")
	span, spanRef := u.elem(p, pRef, "Span")
	if refer {
		span.Set("Ref", object.Array{noteRef})
	}
	if back {
		note.Set("Ref", object.Array{spanRef})
	}
}

// annot puts a on the page, 100pt square and printable, with /Contents,
// enclosed in a structure element of type tag (none when tag is empty) by
// an object reference, with the parent-tree entry that leads back to it. The
// page's tab order is the structure's.
func (u *uaDoc) annot(a *object.Dictionary, tag string) (annotRef object.IndirectRef, el *object.Dictionary) {
	a.Set("Type", object.Name("Annot"))
	a.Set("Rect", object.Array{fxInt(100), fxInt(500), fxInt(200), fxInt(600)})
	if a.Get("F") == nil {
		a.Set("F", fxInt(4))
	}
	if a.Get("Contents") == nil {
		a.Set("Contents", object.String{Value: []byte("an annotation")})
	}
	a.Set("P", u.pageRef)
	annotRef = u.Add(a)
	annots, _ := u.Resolve(u.page.Get("Annots")).(object.Array)
	u.page.Set("Annots", append(slices.Clone(annots), annotRef))
	u.page.Set("Tabs", object.Name("S"))
	if tag == "" {
		return annotRef, nil
	}
	root := u.ResolveDict(u.cat.Get("StructTreeRoot"))
	key, _ := u.Resolve(root.Get("ParentTreeNextKey")).(object.Integer)
	root.Set("ParentTreeNextKey", key+1)
	a.Set("StructParent", key)
	el, elRef := u.elem(nil, object.IndirectRef{}, tag, object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("OBJR")},
		object.Entry{Key: "Obj", Value: annotRef},
		object.Entry{Key: "Pg", Value: u.pageRef},
	))
	pt := u.ResolveDict(root.Get("ParentTree"))
	nums, _ := u.Resolve(pt.Get("Nums")).(object.Array)
	pt.Set("Nums", append(slices.Clone(nums), key, elRef))
	return annotRef, el
}

// uaText is s as a UTF-16BE text string.
func uaText(s string) object.String {
	b := []byte{0xFE, 0xFF}
	for _, c := range utf16.Encode([]rune(s)) {
		b = append(b, byte(c>>8), byte(c))
	}
	return object.String{Value: b}
}

func uaStr(s string) object.String { return object.String{Value: []byte(s)} }

// uaDict is a dictionary of the given entries, alternating key and value; a
// nil value leaves its key out.
func uaDict(kv ...any) *object.Dictionary {
	d := &object.Dictionary{}
	for i := 0; i+1 < len(kv); i += 2 {
		if v, ok := kv[i+1].(object.Object); ok && v != nil {
			d.Set(object.Name(kv[i].(string)), v)
		}
	}
	return d
}

var uaFixtures = []uaFixture{
	{name: "base", build: func(u *uaDoc) {}},
	{name: "struct-elem-without-parent", build: func(u *uaDoc) {
		k := u.Resolve(u.docElem.Get("K")).(object.Array)
		u.ResolveDict(k[0]).Delete("P")
	}, want: [2][]string{{"7.1"}, {"8.2.2"}}},
	{name: "markinfo-not-marked", build: func(u *uaDoc) {
		u.cat.Set("MarkInfo", uaDict("Marked", object.Boolean(false)))
	}, want: [2][]string{{"6.2"}, {"6.2"}}},
	{name: "link-annotation", build: func(u *uaDoc) {
		u.annot(uaDict("Subtype", object.Name("Link"), "A", uaDict("S", object.Name("URI"), "URI", uaStr("https://example.com/"))), "Link")
	}, want: [2][]string{nil, nil}},
	{name: "link-annotation-in-p", build: func(u *uaDoc) {
		u.annot(uaDict("Subtype", object.Name("Link"), "A", uaDict("S", object.Name("URI"), "URI", uaStr("https://example.com/"))), "P")
	}, want: [2][]string{{"7.18.5"}, {"8.2.5.20"}}},
	{name: "square-annotation", build: func(u *uaDoc) {
		u.annot(uaDict("Subtype", object.Name("Square")), "Annot")
	}, want: [2][]string{nil, nil}},
	{name: "square-annotation-in-p", build: func(u *uaDoc) {
		u.annot(uaDict("Subtype", object.Name("Square")), "P")
	}, want: [2][]string{{"7.18.1"}, {"8.9.2.3"}}},
	{name: "square-annotation-rc-differs", build: func(u *uaDoc) {
		u.annot(uaDict("Subtype", object.Name("Square"), "RC", uaStr("<body><p>something else</p></body>")), "Annot")
	}, want: [2][]string{nil, {"8.9.2.3"}}},
	{name: "stamp-without-contents", build: func(u *uaDoc) {
		_, _ = u.annot(uaDict("Subtype", object.Name("Stamp"), "Name", object.Name("Approved"), "Contents", object.Null{}), "Annot")
	}, want: [2][]string{{"7.18.1"}, nil}},
	{name: "stamp-without-name-or-contents", build: func(u *uaDoc) {
		u.annot(uaDict("Subtype", object.Name("Stamp"), "Contents", object.Null{}), "Annot")
	}, want: [2][]string{{"7.18.1"}, {"8.9.2.4.7"}}},
	{name: "ink-without-contents", build: func(u *uaDoc) {
		u.annot(uaDict("Subtype", object.Name("Ink"), "InkList", object.Array{object.Array{fxInt(100), fxInt(500), fxInt(200), fxInt(600)}}, "Contents", object.Null{}), "Annot")
	}, want: [2][]string{{"7.18.1"}, {"8.9.2.4.8"}}},
	{name: "popup-in-structure", build: func(u *uaDoc) {
		u.annot(uaDict("Subtype", object.Name("Popup")), "Annot")
	}, want: [2][]string{nil, {"8.9.2.4.9"}}},
	{name: "sound-annotation", build: func(u *uaDoc) {
		u.annot(uaDict("Subtype", object.Name("Sound"), "Sound", u.Add(object.NewStream(uaDict("Type", object.Name("Sound"), "R", fxInt(8000)), []byte{0, 0}))), "Annot")
	}, want: [2][]string{nil, {"8.9.2.4.11"}}},
	{name: "movie-annotation", build: func(u *uaDoc) {
		u.annot(uaDict("Subtype", object.Name("Movie"), "Movie", uaDict("F", uaStr("a.mov"))), "Annot")
	}, want: [2][]string{nil, {"8.9.2.4.11"}}},
	{name: "screen-without-contents", build: func(u *uaDoc) {
		u.annot(uaDict("Subtype", object.Name("Screen"), "Contents", object.Null{}), "Annot")
	}, want: [2][]string{{"7.18.1"}, {"8.9.2.4.12"}}},
	{name: "zero-size-widget-not-artifact", build: func(u *uaDoc) {
		a, _ := u.annot(uaDict("Subtype", object.Name("Widget"), "FT", object.Name("Tx"), "T", uaStr("f"), "TU", uaStr("a field")), "Form")
		w := u.ResolveDict(a)
		w.Set("Rect", object.Array{fxInt(100), fxInt(500), fxInt(100), fxInt(500)})
		u.cat.Set("AcroForm", uaDict("Fields", object.Array{a}))
	}, want: [2][]string{nil, {"8.9.2.4.13"}}},
	{name: "printer-mark-annotation", build: func(u *uaDoc) {
		u.annot(uaDict("Subtype", object.Name("PrinterMark")), "Annot")
	}, want: [2][]string{{"7.18.8"}, {"8.9.2.4.14"}}},
	{name: "trap-net-annotation", build: func(u *uaDoc) {
		u.annot(uaDict("Subtype", object.Name("TrapNet")), "Annot")
	}, want: [2][]string{{"7.18.2"}, {"8.9.2.4.15"}}},
	{name: "watermark-annotation-in-p", build: func(u *uaDoc) {
		u.annot(uaDict("Subtype", object.Name("Watermark")), "P")
	}, want: [2][]string{{"7.18.1"}, {"8.9.2.4.16"}}},
	{name: "3d-annotation-without-contents", build: func(u *uaDoc) {
		u.annot(uaDict("Subtype", object.Name("3D"), "Contents", object.Null{}), "Annot")
	}, want: [2][]string{{"7.18.1"}, {"8.9.2.4.19"}}},
	{name: "richmedia-annotation-without-contents", build: func(u *uaDoc) {
		u.annot(uaDict("Subtype", object.Name("RichMedia"), "Contents", object.Null{}), "Annot")
	}, want: [2][]string{{"7.18.1"}, {"8.9.2.4.19"}}},
	{name: "annotation-page-without-tabs", build: func(u *uaDoc) {
		u.annot(uaDict("Subtype", object.Name("Square")), "Annot")
		u.page.Delete("Tabs")
	}, want: [2][]string{{"7.18.3"}, {"8.9.3.3"}}},
	{name: "annotation-contents-differs-from-alt", build: func(u *uaDoc) {
		_, el := u.annot(uaDict("Subtype", object.Name("Square")), "Annot")
		el.Set("Alt", uaStr("something else"))
	}, want: [2][]string{nil, {"8.9.4.2"}}},
	{name: "annotation-noview-tagged", build: func(u *uaDoc) {
		u.annot(uaDict("Subtype", object.Name("Square"), "F", fxInt(4|32)), "Annot")
	}, want: [2][]string{nil, {"8.9.2.2"}}},
	{name: "widget-in-form", build: func(u *uaDoc) {
		a, _ := u.annot(uaDict("Subtype", object.Name("Widget"), "FT", object.Name("Tx"), "T", uaStr("f"), "TU", uaStr("a field")), "Form")
		u.cat.Set("AcroForm", uaDict("Fields", object.Array{a}))
	}, want: [2][]string{nil, nil}},
	{name: "widget-not-in-form", build: func(u *uaDoc) {
		a, _ := u.annot(uaDict("Subtype", object.Name("Widget"), "FT", object.Name("Tx"), "T", uaStr("f"), "TU", uaStr("a field")), "P")
		u.cat.Set("AcroForm", uaDict("Fields", object.Array{a}))
	}, want: [2][]string{{"7.18.4"}, {"8.10.1"}}},
	{name: "form-element-with-two-kids", build: func(u *uaDoc) {
		a, el := u.annot(uaDict("Subtype", object.Name("Widget"), "FT", object.Name("Tx"), "T", uaStr("f"), "TU", uaStr("a field")), "Form")
		u.cat.Set("AcroForm", uaDict("Fields", object.Array{a}))
		k := u.Resolve(el.Get("K")).(object.Array)
		el.Set("K", append(slices.Clone(k), uaDict("Type", object.Name("OBJR"), "Obj", a, "Pg", u.pageRef)))
	}, want: [2][]string{{"7.18.4"}, {"8.10.1"}}},
	{name: "acroform-xfa", build: func(u *uaDoc) {
		u.cat.Set("AcroForm", uaDict("Fields", object.Array{}, "XFA", u.Add(object.NewStream(&object.Dictionary{}, []byte("<xdp:xdp xmlns:xdp=\"http://ns.adobe.com/xdp/\"/>")))))
	}, want: [2][]string{nil, {"8.10.1"}}},
	{name: "text-field-rv-without-v", build: func(u *uaDoc) {
		a, _ := u.annot(uaDict("Subtype", object.Name("Widget"), "FT", object.Name("Tx"), "T", uaStr("f"), "TU", uaStr("a field"),
			"RV", uaStr("<body><p>rich</p></body>")), "Form")
		u.cat.Set("AcroForm", uaDict("Fields", object.Array{a}))
	}, want: [2][]string{nil, {"8.10.3.3"}}},
	{name: "widget-aa-without-contents", build: func(u *uaDoc) {
		a, _ := u.annot(uaDict("Subtype", object.Name("Widget"), "FT", object.Name("Tx"), "T", uaStr("f"), "TU", uaStr("a field"),
			"Contents", object.Null{}, "AA", uaDict("Fo", uaDict("S", object.Name("Named"), "N", object.Name("NextPage")))), "Form")
		u.cat.Set("AcroForm", uaDict("Fields", object.Array{a}))
	}, want: [2][]string{nil, {"8.10.2.3"}}},
	{name: "note-structure-type", build: func(u *uaDoc) {
		u.elem(nil, object.IndirectRef{}, "Note")
	}, want: [2][]string{{"7.9"}, {"8.2.5.14"}}},
	{name: "fenote-bad-notetype", build: func(u *uaDoc) {
		e, _ := u.elem(nil, object.IndirectRef{}, "FENote")
		e.Set("A", uaDict("O", object.Name("NSO"), "NS", u.Resolve(u.docElem.Get("NS")), "NoteType", object.Name("Sidenote")))
	}, want: [2][]string{{"7.1"}, {"8.2.4"}}},
	{name: "toci-without-ref", build: func(u *uaDoc) {
		toc, tocRef := u.elem(nil, object.IndirectRef{}, "TOC")
		u.elem(toc, tocRef, "TOCI")
	}, want: [2][]string{nil, {"8.2.5.8"}}},
	{name: "ruby-without-rt", build: func(u *uaDoc) {
		r, rRef := u.elem(nil, object.IndirectRef{}, "Ruby")
		u.elem(r, rRef, "RB")
	}, want: [2][]string{nil, {"8.2.5.23", "Table 5. Document-Ruby"}}},
	{name: "warichu-without-wp", build: func(u *uaDoc) {
		w, wRef := u.elem(nil, object.IndirectRef{}, "Warichu")
		u.elem(w, wRef, "WT")
	}, want: [2][]string{nil, {"8.2.5.24", "Table 5. Document-Warichu"}}},
	{name: "caption-in-the-middle", build: func(u *uaDoc) {
		f, fRef := u.elem(nil, object.IndirectRef{}, "Sect")
		u.elem(f, fRef, "Div")
		u.elem(f, fRef, "Caption")
		u.elem(f, fRef, "Div")
	}, want: [2][]string{nil, nil}},
	{name: "list-item-without-lbody", build: func(u *uaDoc) {
		l, lRef := u.elem(nil, object.IndirectRef{}, "L")
		li, liRef := u.elem(l, lRef, "LI")
		u.elem(li, liRef, "P")
	}, want: [2][]string{{"7.2"}, {"Table 5. LI-P"}}},
	{name: "table-columns-differ", build: func(u *uaDoc) {
		tb, tbRef := u.elem(nil, object.IndirectRef{}, "Table")
		r1, r1Ref := u.elem(tb, tbRef, "TR")
		u.elem(r1, r1Ref, "TD")
		u.elem(r1, r1Ref, "TD")
		r2, r2Ref := u.elem(tb, tbRef, "TR")
		u.elem(r2, r2Ref, "TD")
	}, want: [2][]string{{"7.2"}, {"8.2.5.26"}}},
	{name: "table-cells-intersect", build: func(u *uaDoc) {
		tb, tbRef := u.elem(nil, object.IndirectRef{}, "Table")
		r1, r1Ref := u.elem(tb, tbRef, "TR")
		c, _ := u.elem(r1, r1Ref, "TD")
		c.Set("A", uaDict("O", object.Name("Table"), "RowSpan", fxInt(2)))
		u.elem(r1, r1Ref, "TD")
		r2, r2Ref := u.elem(tb, tbRef, "TR")
		u.elem(r2, r2Ref, "TD")
		u.elem(r2, r2Ref, "TD")
	}, want: [2][]string{{"7.2"}, {"8.2.5.26"}}},
	{name: "ruby-rb-rt", build: func(u *uaDoc) {
		r, rRef := u.elem(nil, object.IndirectRef{}, "Ruby")
		u.elem(r, rRef, "RB")
		u.elem(r, rRef, "RT")
	}, want: [2][]string{nil, {"Table 5. Document-Ruby"}}},
	{name: "warichu-wp-wt-wp", build: func(u *uaDoc) {
		w, wRef := u.elem(nil, object.IndirectRef{}, "Warichu")
		u.elem(w, wRef, "WP")
		u.elem(w, wRef, "WT")
		u.elem(w, wRef, "WP")
	}, want: [2][]string{nil, {"Table 5. Document-Warichu"}}},
	{name: "toci-with-ref", build: func(u *uaDoc) {
		toc, tocRef := u.elem(nil, object.IndirectRef{}, "TOC")
		toci, _ := u.elem(toc, tocRef, "TOCI")
		toci.Set("Ref", object.Array{u.docElemRef})
	}, want: [2][]string{nil, nil}},
	{name: "square-annotation-rc-same", build: func(u *uaDoc) {
		u.annot(uaDict("Subtype", object.Name("Square"), "RC", uaStr("an annotation")), "Annot")
	}, want: [2][]string{nil, {"8.9.2.3"}}},
	{name: "annotation-contents-same-as-alt", build: func(u *uaDoc) {
		_, el := u.annot(uaDict("Subtype", object.Name("Square")), "Annot")
		el.Set("Alt", uaStr("an annotation"))
	}, want: [2][]string{nil, nil}},
	{name: "text-field-rv-equals-v", build: func(u *uaDoc) {
		a, _ := u.annot(uaDict("Subtype", object.Name("Widget"), "FT", object.Name("Tx"), "T", uaStr("f"), "TU", uaStr("a field"),
			"V", uaStr("rich"), "RV", uaStr("rich")), "Form")
		u.cat.Set("AcroForm", uaDict("Fields", object.Array{a}))
	}, want: [2][]string{nil, {"8.10.3.3"}}},
	{name: "annotation-noview-togglenoview", build: func(u *uaDoc) {
		u.annot(uaDict("Subtype", object.Name("Square"), "F", fxInt(4|32|256)), "Annot")
	}, want: [2][]string{nil, nil}},
	{name: "stamp-with-name", build: func(u *uaDoc) {
		u.annot(uaDict("Subtype", object.Name("Stamp"), "Name", object.Name("Approved")), "Annot")
	}, want: [2][]string{nil, nil}},
	{name: "popup-not-in-structure", build: func(u *uaDoc) {
		_, _ = u.annot(uaDict("Subtype", object.Name("Square")), "Annot")
		u.annot(uaDict("Subtype", object.Name("Popup")), "")
	}, want: [2][]string{{"7.18.1"}, nil}},
	{name: "square-annotation-rc-xhtml-same", build: func(u *uaDoc) {
		u.annot(uaDict("Subtype", object.Name("Square"), "RC", uaStr(`<?xml version="1.0"?><body xmlns="http://www.w3.org/1999/xhtml" xmlns:xfa="http://www.xfa.org/schema/xfa-data/1.0/" xfa:APIVersion="Acrobat:7.0.0" xfa:spec="2.0.2"><p>an annotation</p></body>`)), "Annot")
	}, want: [2][]string{nil, nil}},
	{name: "square-annotation-rc-markup-same", build: func(u *uaDoc) {
		u.annot(uaDict("Subtype", object.Name("Square"), "RC", uaStr(`<body><p>an annotation</p></body>`)), "Annot")
	}, want: [2][]string{nil, nil}},
	{name: "text-field-rv-xhtml-equals-v", build: func(u *uaDoc) {
		a, _ := u.annot(uaDict("Subtype", object.Name("Widget"), "FT", object.Name("Tx"), "T", uaStr("f"), "TU", uaStr("a field"),
			"V", uaStr("rich"), "RV", uaStr(`<?xml version="1.0"?><body xmlns="http://www.w3.org/1999/xhtml"><p>rich</p></body>`)), "Form")
		u.cat.Set("AcroForm", uaDict("Fields", object.Array{a}))
	}, want: [2][]string{nil, nil}},
	{name: "popup-with-parent-not-in-structure", build: func(u *uaDoc) {
		sq, _ := u.annot(uaDict("Subtype", object.Name("Square")), "Annot")
		pr, _ := u.annot(uaDict("Subtype", object.Name("Popup"), "Parent", sq), "")
		u.ResolveDict(sq).Set("Popup", pr)
	}, want: [2][]string{{"7.18.1"}, nil}},
	{name: "popup-outside-the-crop-box", build: func(u *uaDoc) {
		sq, _ := u.annot(uaDict("Subtype", object.Name("Square")), "Annot")
		pr, _ := u.annot(uaDict("Subtype", object.Name("Popup"), "Parent", sq), "")
		u.ResolveDict(pr).Set("Rect", object.Array{fxInt(595), fxInt(500), fxInt(800), fxInt(600)})
		u.ResolveDict(sq).Set("Popup", pr)
	}, want: [2][]string{nil, nil}},
	{name: "link-holding-p-without-content", build: func(u *uaDoc) {
		l, lRef := u.elem(nil, object.IndirectRef{}, "Link")
		u.elem(l, lRef, "P")
	}, want: [2][]string{nil, nil}},
	{name: "link-holding-p-and-content", build: func(u *uaDoc) {
		k := u.Resolve(u.docElem.Get("K")).(object.Array)
		para := u.ResolveDict(k[1])
		para.Set("S", object.Name("Link"))
		u.elem(para, k[1].(object.IndirectRef), "P")
	}, want: [2][]string{nil, {"Table 5. Link-P"}}},
	{name: "table-with-two-captions", build: func(u *uaDoc) {
		tb, tbRef := u.elem(nil, object.IndirectRef{}, "Table")
		u.elem(tb, tbRef, "Caption")
		r, rRef := u.elem(tb, tbRef, "TR")
		u.elem(r, rRef, "TD")
		u.elem(tb, tbRef, "Caption")
	}, want: [2][]string{{"7.2"}, {"Table 5. Table-Caption"}}},
	{name: "table-with-content", build: func(u *uaDoc) {
		k := u.Resolve(u.docElem.Get("K")).(object.Array)
		u.ResolveDict(k[1]).Set("S", object.Name("Table"))
	}, want: [2][]string{nil, {"Table 5. Table-content"}}},
	{name: "p-in-tr", build: func(u *uaDoc) {
		tb, tbRef := u.elem(nil, object.IndirectRef{}, "Table")
		r, rRef := u.elem(tb, tbRef, "TR")
		u.elem(r, rRef, "P")
	}, want: [2][]string{{"7.2"}, {"Table 5. TR-P"}}},
	{name: "code-holding-sub", build: func(u *uaDoc) {
		// Sub is a PDF 2.0 type: in the PDF 2.0 namespace, as the
		// Document element is, where the part has one.
		c, cRef := u.elem(nil, object.IndirectRef{}, "Code")
		sub, _ := u.elem(c, cRef, "Sub")
		if ns := u.docElem.Get("NS"); ns != nil {
			sub.Set("NS", ns)
		}
	}, want: [2][]string{{"7.1"}, nil}},
	{name: "heading-holding-two-arts", build: func(u *uaDoc) {
		h, hRef := u.elem(nil, object.IndirectRef{}, "H1")
		u.elem(h, hRef, "Art")
		u.elem(h, hRef, "Art")
	}, want: [2][]string{nil, {"Table 5. Hn-Art"}}},
	{name: "div-in-table-holding-p", build: func(u *uaDoc) {
		tb, tbRef := u.elem(nil, object.IndirectRef{}, "Table")
		dv, dvRef := u.elem(tb, tbRef, "Div")
		u.elem(dv, dvRef, "P")
	}, want: [2][]string{{"7.2"}, {"Table 5. Table-Div", "Table 5. Table-P"}}},
	{name: "li-with-content", build: func(u *uaDoc) {
		// The paragraph and its marked content become a list item.
		k := slices.Clone(u.Resolve(u.docElem.Get("K")).(object.Array))
		liRef := k[1].(object.IndirectRef)
		lRef := u.Add(uaDict("Type", object.Name("StructElem"), "S", object.Name("L"), "P", u.docElemRef, "Pg", u.pageRef, "K", object.Array{liRef}))
		li := u.ResolveDict(liRef)
		li.Set("S", object.Name("LI"))
		li.Set("P", lRef)
		k[1] = lRef
		u.docElem.Set("K", k)
	}, want: [2][]string{nil, {"8.2.5.25"}}},
	{name: "fenote-notetype-sidenote", build: func(u *uaDoc) {
		u.fenote(true, true, uaDict("O", object.Name("FENote"), "NoteType", object.Name("Sidenote")))
	}, want: [2][]string{{"7.1"}, {"8.2.5.14"}}},
	{name: "fenote-notetype-footnote", build: func(u *uaDoc) {
		u.fenote(true, true, uaDict("O", object.Name("FENote"), "NoteType", object.Name("Footnote")))
	}, want: [2][]string{{"7.1"}, nil}},
	{name: "fenote-refs-paired", build: func(u *uaDoc) { u.fenote(true, true, nil) }, want: [2][]string{{"7.1"}, nil}},
	{name: "fenote-referrer-not-named", build: func(u *uaDoc) { u.fenote(true, false, nil) }, want: [2][]string{{"7.1"}, {"8.2.5.14"}}},
	{name: "fenote-names-a-non-referrer", build: func(u *uaDoc) { u.fenote(false, true, nil) }, want: [2][]string{{"7.1"}, {"8.2.5.14"}}},
	{name: "alt-with-pua", build: func(u *uaDoc) {
		f, _ := u.elem(nil, object.IndirectRef{}, "Span")
		f.Set("Alt", uaText(""))
	}, want: [2][]string{nil, {"8.4.3", "Table 5. Document-Span"}}},
	{name: "actualtext-with-pua", build: func(u *uaDoc) {
		f, _ := u.elem(nil, object.IndirectRef{}, "Span")
		f.Set("ActualText", uaText(""))
	}, want: [2][]string{nil, {"8.4.3", "Table 5. Document-Span"}}},
	{name: "optional-content-config-as", build: func(u *uaDoc) {
		g := u.Add(uaDict("Type", object.Name("OCG"), "Name", uaStr("layer")))
		u.cat.Set("OCProperties", uaDict("OCGs", object.Array{g}, "D", uaDict("Name", uaStr("default"), "Order", object.Array{g},
			"AS", object.Array{uaDict("Event", object.Name("View"), "OCGs", object.Array{g}, "Category", object.Array{object.Name("View")})})))
	}, want: [2][]string{{"7.10"}, {"8.7"}}},
	{name: "catalog-without-metadata", build: func(u *uaDoc) {
		u.cat.Delete("Metadata")
	}, want: [2][]string{{"7.1"}, {"8.11.1"}}},
}

// TestSingleDefectFilesAgreeWithVeraPDFOnPDFUA is the PDF/UA counterpart of
// TestSingleDefectFilesAgreeWithVeraPDF.
func TestSingleDefectFilesAgreeWithVeraPDFOnPDFUA(t *testing.T) {
	vera := os.Getenv("PDF0_VERAPDF")
	for i, flavour := range []string{"ua1", "ua2"} {
		part := i + 1
		dir := filepath.Join(t.TempDir(), flavour)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, f := range uaFixtures {
			u := uaBase(t, part)
			f.build(u)
			var buf bytes.Buffer
			if err := u.Write(&buf); err != nil {
				t.Fatalf("%s at %s: %v", f.name, flavour, err)
			}
			data := buf.Bytes()
			want := slices.Clone(f.want[i])
			slices.Sort(want)
			if got := pdf0UAClauses(t, data, part); !slices.Equal(got, want) {
				t.Errorf("%s at %s: pdf0 fails it on %v; veraPDF, as the fixture records, on %v", f.name, flavour, got, want)
			}
			if err := os.WriteFile(filepath.Join(dir, f.name+".pdf"), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if vera == "" {
			continue
		}
		verdicts := veraPDFClauses(t, vera, flavour, dir)
		for _, f := range uaFixtures {
			want := slices.Clone(f.want[i])
			slices.Sort(want)
			got, ok := verdicts[f.name+".pdf"]
			if !ok {
				t.Errorf("%s at %s: veraPDF gave no verdict", f.name, flavour)
				continue
			}
			if !slices.Equal(got, want) {
				t.Errorf("%s at %s: veraPDF fails it on %v, and the fixture records %v", f.name, flavour, got, want)
			}
		}
	}
	if vera == "" {
		t.Log("PDF0_VERAPDF is not set: the fixtures were held to the clauses they record, not to veraPDF run now")
	}
}

// pdf0UAClauses is the distinct clauses pdf0 fails a file on at a PDF/UA
// part, checker findings aside.
func pdf0UAClauses(t *testing.T, data []byte, part int) []string {
	t.Helper()
	doc, err := Read(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	vs := ValidatePDFUA(doc)
	if part == 2 {
		vs = ValidatePDFUA2(doc)
	}
	var out []string
	for _, v := range vs {
		if !IsCheckerFinding(v) && !slices.Contains(out, v.Clause) {
			out = append(out, v.Clause)
		}
	}
	slices.Sort(out)
	return out
}
