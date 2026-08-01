package pdf0

import (
	"bytes"
	"crypto/x509"
	"fmt"
	"testing"
)

// This file pins the page a signature or time-stamp widget is attached to, and
// the /P that names it.
//
// The bug these pin: the page dictionary and the reference written into /P were
// produced by two independent helpers. firstPage skipped /Kids entries that are
// not /Type /Page; pageRef blindly returned the root's first /Kids entry. A page
// tree may legally hold intermediate /Pages nodes and leaf /Page objects side by
// side (ISO 32000-2 §7.7.3.2), so whenever the first entry was an intermediate
// node the widget was appended to one page's /Annots while its /P pointed at a
// /Type /Pages node — not a page at all, where Table 166 requires "an indirect
// reference to the page object with which this annotation is associated".
//
// The same shape used to be unsignable in the other direction: a tree whose
// pages all sit below intermediate nodes offered the old firstPage no /Type
// /Page among the root's kids, and signing failed with "document has no page to
// attach the signature to".

// buildPDFWithMixedKids builds a document whose root page tree mixes node kinds
// at one level: /Kids [5 0 R 3 0 R] where 5 is an intermediate /Pages node
// holding the first page and 3 is a leaf /Page. Reading order is therefore
// object 6 (under the intermediate node) then object 3.
func buildPDFWithMixedKids() []byte {
	var buf bytes.Buffer
	buf.WriteString("%PDF-2.0\n%\x80\x80\x80\x80\n")

	first := "BT /F1 12 Tf 72 700 Td (first) Tj ET\n"
	second := "BT /F1 12 Tf 72 700 Td (second) Tj ET\n"

	offs := make([]int, 0, 7)
	offs = append(offs, buf.Len())
	buf.WriteString("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	offs = append(offs, buf.Len())
	buf.WriteString("2 0 obj\n<< /Type /Pages /Kids [5 0 R 3 0 R] /Count 2 >>\nendobj\n")
	offs = append(offs, buf.Len())
	buf.WriteString("3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R >>\nendobj\n")
	offs = append(offs, buf.Len())
	fmt.Fprintf(&buf, "4 0 obj\n<< /Length %d >>\nstream\n%sendstream\nendobj\n", len(second), second)
	offs = append(offs, buf.Len())
	buf.WriteString("5 0 obj\n<< /Type /Pages /Parent 2 0 R /Kids [6 0 R] /Count 1 >>\nendobj\n")
	offs = append(offs, buf.Len())
	buf.WriteString("6 0 obj\n<< /Type /Page /Parent 5 0 R /MediaBox [0 0 612 792] /Contents 7 0 R >>\nendobj\n")
	offs = append(offs, buf.Len())
	fmt.Fprintf(&buf, "7 0 obj\n<< /Length %d >>\nstream\n%sendstream\nendobj\n", len(first), first)

	return finishTestPDF(&buf, offs, "<< /Size %d /Root 1 0 R >>")
}

// buildPDFWithNestedPagesOnly builds a document whose only page sits below an
// intermediate node, with no /Type /Page among the root's /Kids at all.
func buildPDFWithNestedPagesOnly() []byte {
	var buf bytes.Buffer
	buf.WriteString("%PDF-2.0\n%\x80\x80\x80\x80\n")

	body := "BT /F1 12 Tf 72 700 Td (nested) Tj ET\n"

	offs := make([]int, 0, 5)
	offs = append(offs, buf.Len())
	buf.WriteString("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	offs = append(offs, buf.Len())
	buf.WriteString("2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n")
	offs = append(offs, buf.Len())
	buf.WriteString("3 0 obj\n<< /Type /Pages /Parent 2 0 R /Kids [4 0 R] /Count 1 >>\nendobj\n")
	offs = append(offs, buf.Len())
	buf.WriteString("4 0 obj\n<< /Type /Page /Parent 3 0 R /MediaBox [0 0 612 792] /Contents 5 0 R >>\nendobj\n")
	offs = append(offs, buf.Len())
	fmt.Fprintf(&buf, "5 0 obj\n<< /Length %d >>\nstream\n%sendstream\nendobj\n", len(body), body)

	return finishTestPDF(&buf, offs, "<< /Size %d /Root 1 0 R >>")
}

// signatureWidget returns the object number and dictionary of the one signature
// widget (/FT /Sig) in the document.
func signatureWidget(t *testing.T, d *Document) (int, *Dictionary) {
	t.Helper()
	num, found := -1, (*Dictionary)(nil)
	for n, iobj := range d.Objects {
		fd, ok := iobj.Value.(*Dictionary)
		if !ok {
			continue
		}
		if sub, _ := fd.Get("Subtype").(Name); sub != "Widget" {
			continue
		}
		if ft, _ := fd.Get("FT").(Name); ft != "Sig" {
			continue
		}
		if found != nil {
			t.Fatalf("more than one signature widget: objects %d and %d", num, n)
		}
		num, found = n, fd
	}
	if found == nil {
		t.Fatal("no signature widget in the produced document")
	}
	return num, found
}

// pageCarryingAnnot returns the object number of the page whose /Annots lists
// the given annotation, failing unless exactly one page does.
func pageCarryingAnnot(t *testing.T, d *Document, annotNum int) int {
	t.Helper()
	num := -1
	for n, iobj := range d.Objects {
		pg, ok := iobj.Value.(*Dictionary)
		if !ok {
			continue
		}
		if ty, _ := pg.Get("Type").(Name); ty != "Page" {
			continue
		}
		annots, _ := d.Resolve(pg.Get("Annots")).(Array)
		for _, a := range annots {
			if ref, ok := a.(IndirectRef); ok && ref.Number == annotNum {
				if num >= 0 {
					t.Fatalf("annotation %d appears in the /Annots of pages %d and %d", annotNum, num, n)
				}
				num = n
			}
		}
	}
	if num < 0 {
		t.Fatalf("no page carries annotation %d in its /Annots", annotNum)
	}
	return num
}

// TestWidgetPageAndPRefAgree is the regression test: whatever the shape of the
// page tree, the widget's /P must be an indirect reference to a /Type /Page
// object, and that object must be the page whose /Annots carries the widget.
// Everything is asserted on the re-read output, not on internal state.
func TestWidgetPageAndPRefAgree(t *testing.T) {
	cert, key := testCertKey(t)
	tsaCert, tsaKey := testTSACertKey(t)

	writers := []struct {
		name  string
		write func(*Document, []byte, *bytes.Buffer) error
	}{
		{"WriteSigned", func(d *Document, _ []byte, b *bytes.Buffer) error { return d.WriteSigned(b, cert, key) }},
		{"WriteSignedIncremental", func(d *Document, raw []byte, b *bytes.Buffer) error {
			return d.WriteSignedIncremental(b, raw, cert, key)
		}},
		{"WriteArchivalTimestamp", func(d *Document, raw []byte, b *bytes.Buffer) error {
			return d.WriteArchivalTimestamp(b, raw, []*x509.Certificate{cert}, tsaCert, tsaKey)
		}},
	}
	bases := []struct {
		name string
		base []byte
	}{
		{"mixed kids", buildPDFWithMixedKids()},
		{"pages only below intermediate nodes", buildPDFWithNestedPagesOnly()},
		{"flat tree", buildPDFWithPageContents()},
	}

	for _, bs := range bases {
		for _, w := range writers {
			t.Run(bs.name+"/"+w.name, func(t *testing.T) {
				doc, err := Read(bytes.NewReader(bs.base), int64(len(bs.base)))
				if err != nil {
					t.Fatal(err)
				}
				var buf bytes.Buffer
				if err := w.write(doc, bs.base, &buf); err != nil {
					t.Fatalf("%s: %v", w.name, err)
				}
				out := buf.Bytes()
				signed, err := Read(bytes.NewReader(out), int64(len(out)))
				if err != nil {
					t.Fatalf("re-read: %v", err)
				}

				widgetNum, widget := signatureWidget(t, signed)
				pRef, ok := widget.Get("P").(IndirectRef)
				if !ok {
					t.Fatalf("widget /P = %#v, want an indirect reference (ISO 32000-2 Table 166)", widget.Get("P"))
				}
				target := signed.ResolveDict(pRef)
				if target == nil {
					t.Fatalf("widget /P (%d 0 R) does not resolve to a dictionary", pRef.Number)
				}
				if ty, _ := target.Get("Type").(Name); ty != "Page" {
					t.Errorf("widget /P (%d 0 R) resolves to /Type %v, want /Page", pRef.Number, ty)
				}
				if carrier := pageCarryingAnnot(t, signed, widgetNum); carrier != pRef.Number {
					t.Errorf("widget is in the /Annots of page %d but its /P names %d: the annotation and its page must agree", carrier, pRef.Number)
				}
				// The page signed is the document's first in reading order, the
				// same one PageList reports.
				pages := signed.PageList()
				if len(pages) == 0 {
					t.Fatal("the produced document has no pages")
				}
				if first := dictObjNum(signed, pages[0]); first != pRef.Number {
					t.Errorf("the widget is on page object %d, want the first page in reading order (object %d)", pRef.Number, first)
				}
			})
		}
	}
}

// TestSignDocumentWithNestedPageTree pins the behaviour change that comes with
// descending into intermediate nodes: a document whose pages all sit below one
// is signable at all. Before, firstPage found no /Type /Page among the root's
// kids and every writer refused the document.
func TestSignDocumentWithNestedPageTree(t *testing.T) {
	cert, key := testCertKey(t)
	base := buildPDFWithNestedPagesOnly()
	doc, err := Read(bytes.NewReader(base), int64(len(base)))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := doc.WriteSigned(&buf, cert, key); err != nil {
		t.Fatalf("WriteSigned on a document whose only page is nested: %v", err)
	}
	out := buf.Bytes()
	signed, err := Read(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	res := signed.VerifySignatures(out)
	if len(res) != 1 || !res[0].Valid {
		t.Fatalf("signature did not verify: %+v", res)
	}
}

// TestFirstPageStopsOnACyclicPageTree guards the descent against an untrusted
// document: /Kids pointing back at an ancestor must not loop forever, and a tree
// that holds no page at all must still report none.
func TestFirstPageStopsOnACyclicPageTree(t *testing.T) {
	d := &Document{Version: "2.0", Objects: map[int]*IndirectObject{}, Trailer: Dictionary{}}
	cat := &Dictionary{}
	cat.Set("Type", Name("Catalog"))
	cat.Set("Pages", IndirectRef{Number: 2})
	root := &Dictionary{}
	root.Set("Type", Name("Pages"))
	root.Set("Kids", Array{IndirectRef{Number: 3}})
	inner := &Dictionary{}
	inner.Set("Type", Name("Pages"))
	inner.Set("Kids", Array{IndirectRef{Number: 2}}) // back up to the root
	d.Objects[1] = &IndirectObject{Number: 1, Value: cat}
	d.Objects[2] = &IndirectObject{Number: 2, Value: root}
	d.Objects[3] = &IndirectObject{Number: 3, Value: inner}
	d.Trailer.Set("Root", IndirectRef{Number: 1})

	if pg := firstPage(d, cat); pg != nil {
		t.Errorf("firstPage on a cyclic, page-less tree = %v, want nil", pg)
	}
}
