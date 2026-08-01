package pdf0

import (
	"bytes"
	"crypto/x509"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// This file covers what signing does with structures that are *not* separate
// indirect objects. Three cases, three answers:
//
//   - a direct /AcroForm is legal PDF and is promoted to a new indirect object;
//   - a direct catalog (trailer /Root, ISO 32000-2 §7.5.5) and
//   - a direct first page (page-tree /Kids, §7.7.3.2) are malformed and refused.
//
// The bug these pin: dictObjNum reports -1 for a dictionary that is not the
// value of any indirect object, and the -1 was used as an object number, so the
// writers emitted "-1 0 obj" into the file and returned no error at all. Every
// assertion below is therefore on the produced bytes, not just on err == nil.

// negativeObjHeader matches an indirect object header with a non-positive object
// number, which ISO 32000-2 §7.3.10 forbids (object numbers are positive
// integers).
var negativeObjHeader = regexp.MustCompile(`(^|\n)-[0-9]+ [0-9]+ obj`)

func assertNoNegativeObject(t *testing.T, what string, out []byte) {
	t.Helper()
	if loc := negativeObjHeader.FindIndex(out); loc != nil {
		end := loc[1] + 48
		if end > len(out) {
			end = len(out)
		}
		t.Fatalf("%s: output contains an object with a negative number at byte %d: %q", what, loc[0], out[loc[0]:end])
	}
}

// buildPDFWithDirectForm is buildPDFWithFormField with the interactive form
// stored as a direct dictionary inside the catalog instead of as object 5. That
// is legal: ISO 32000-2 Table 29 does not require /AcroForm to be indirect.
func buildPDFWithDirectForm() []byte {
	var buf bytes.Buffer
	buf.WriteString("%PDF-2.0\n%\x80\x80\x80\x80\n")

	body := "BT /F1 12 Tf 72 700 Td (hello) Tj ET\n"
	form := "<< /Fields [5 0 R] /SigFlags 4 /DA (/Helv 0 Tf 0 g) /DR << /Font << >> >> /NeedAppearances true /Q 1 >>"

	offs := make([]int, 0, 5)
	offs = append(offs, buf.Len())
	buf.WriteString("1 0 obj\n<< /Type /Catalog /Pages 2 0 R /AcroForm " + form + " >>\nendobj\n")
	offs = append(offs, buf.Len())
	buf.WriteString("2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n")
	offs = append(offs, buf.Len())
	buf.WriteString("3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Annots [5 0 R] >>\nendobj\n")
	offs = append(offs, buf.Len())
	fmt.Fprintf(&buf, "4 0 obj\n<< /Length %d >>\nstream\n%sendstream\nendobj\n", len(body), body)
	offs = append(offs, buf.Len())
	buf.WriteString("5 0 obj\n<< /Type /Annot /Subtype /Widget /FT /Tx /T (Applicant) /Rect [72 600 300 620] /P 3 0 R >>\nendobj\n")

	return finishTestPDF(&buf, offs, "<< /Size %d /Root 1 0 R >>")
}

// buildPDFWithDirectCatalog stores the document catalog directly in the trailer
// /Root, which ISO 32000-2 §7.5.5 forbids (it shall be an indirect reference).
func buildPDFWithDirectCatalog() []byte {
	var buf bytes.Buffer
	buf.WriteString("%PDF-2.0\n%\x80\x80\x80\x80\n")

	offs := make([]int, 0, 2)
	offs = append(offs, buf.Len())
	buf.WriteString("1 0 obj\n<< /Type /Pages /Kids [2 0 R] /Count 1 >>\nendobj\n")
	offs = append(offs, buf.Len())
	buf.WriteString("2 0 obj\n<< /Type /Page /Parent 1 0 R /MediaBox [0 0 612 792] >>\nendobj\n")

	return finishTestPDF(&buf, offs, "<< /Size %d /Root << /Type /Catalog /Pages 1 0 R >> >>")
}

// buildPDFWithDirectPage stores the only page directly in the page tree's
// /Kids, which ISO 32000-2 §7.7.3.2 forbids (the entries shall be indirect
// references).
func buildPDFWithDirectPage() []byte {
	var buf bytes.Buffer
	buf.WriteString("%PDF-2.0\n%\x80\x80\x80\x80\n")

	offs := make([]int, 0, 2)
	offs = append(offs, buf.Len())
	buf.WriteString("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	offs = append(offs, buf.Len())
	buf.WriteString("2 0 obj\n<< /Type /Pages /Kids [<< /Type /Page /MediaBox [0 0 612 792] >>] /Count 1 >>\nendobj\n")

	return finishTestPDF(&buf, offs, "<< /Size %d /Root 1 0 R >>")
}

// finishTestPDF appends the xref table, trailer (trailerFmt takes /Size) and
// startxref for a body whose object offsets are offs, objects 1..len(offs).
func finishTestPDF(buf *bytes.Buffer, offs []int, trailerFmt string) []byte {
	xrefOffset := buf.Len()
	fmt.Fprintf(buf, "xref\n0 %d\n", len(offs)+1)
	buf.WriteString("0000000000 65535 f \r\n")
	for _, o := range offs {
		fmt.Fprintf(buf, "%010d 00000 n \r\n", o)
	}
	buf.WriteString("trailer\n")
	fmt.Fprintf(buf, trailerFmt+"\n", len(offs)+1)
	fmt.Fprintf(buf, "startxref\n%d\n%%%%EOF\n", xrefOffset)
	return buf.Bytes()
}

// TestArchivalTimestampPromotesDirectAcroForm is the regression test for the
// defect: withArchivalTimestamp took dictObjNum of a direct /AcroForm, got -1,
// and stored the updated form under object number -1. The result was an "-1 0
// obj" in the file — invalid under ISO 32000-2 §7.3.10 — that the catalog did
// not even reference, since the catalog still held the original direct form.
// No error was returned, and pdf0's own lenient reader parsed the result, so
// nothing caught it. The form must be promoted to a real object instead, with
// the catalog pointed at it.
func TestArchivalTimestampPromotesDirectAcroForm(t *testing.T) {
	cert, _ := testCertKey(t)
	tsaCert, tsaKey := testTSACertKey(t)

	base := buildPDFWithDirectForm()
	doc, err := Read(bytes.NewReader(base), int64(len(base)))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := doc.WriteArchivalTimestamp(&buf, base, []*x509.Certificate{cert}, tsaCert, tsaKey); err != nil {
		t.Fatalf("WriteArchivalTimestamp: %v", err)
	}
	out := buf.Bytes()
	assertNoNegativeObject(t, "WriteArchivalTimestamp", out)

	d2, err := Read(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	cat := d2.ResolveDict(d2.Trailer.Get("Root"))
	if cat == nil {
		t.Fatal("no catalog")
	}
	if _, ok := cat.Get("AcroForm").(IndirectRef); !ok {
		t.Errorf("catalog /AcroForm = %#v, want an indirect reference to the promoted form", cat.Get("AcroForm"))
	}
	form := d2.ResolveDict(cat.Get("AcroForm"))
	if form == nil {
		t.Fatal("catalog /AcroForm does not resolve")
	}
	if dictObjNum(d2, form) < 0 {
		t.Error("the form the catalog points at is still not an indirect object")
	}
	// The promoted form must be the original, extended: the pre-existing field
	// alongside the time-stamp field, the producer's other keys, and /SigFlags
	// bit 3 (which no signer sets) preserved.
	got := formFields(t, d2)
	sort.Strings(got)
	want := []string{"Applicant", "Timestamp1"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("form fields = %v, want %v", got, want)
	}
	if flags, _ := d2.Resolve(form.Get("SigFlags")).(Integer); flags != 7 {
		t.Errorf("/SigFlags = %v, want 7 (4 preserved | 3 set)", flags)
	}
	if form.Get("DA") == nil || form.Get("NeedAppearances") == nil {
		t.Error("the promoted form dropped keys of the original")
	}
	if d2.ResolveDict(cat.Get("DSS")) == nil {
		t.Error("no /DSS in the catalog")
	}
	// The whole point of the exercise: the archival time-stamp must verify over
	// the produced bytes.
	if !coveringDocTimestamp(d2, out) {
		t.Error("the archival time-stamp does not verify over the file it seals")
	}
}

// TestArchivalTimestampOnSignedDirectFormDocument runs the full B-LTA flow over
// a document whose form starts out direct: signing promotes it, the archival
// time-stamp then extends the promoted object. Both signature fields must end up
// in the one form, and the approval signature must stay valid.
func TestArchivalTimestampOnSignedDirectFormDocument(t *testing.T) {
	cert, key := testCertKey(t)
	tsaCert, tsaKey := testTSACertKey(t)

	base := buildPDFWithDirectForm()
	doc, err := Read(bytes.NewReader(base), int64(len(base)))
	if err != nil {
		t.Fatal(err)
	}
	var b1 bytes.Buffer
	if err := doc.WriteSignedTimestamped(&b1, cert, key, tsaCert, tsaKey); err != nil {
		t.Fatalf("WriteSignedTimestamped: %v", err)
	}
	o1 := b1.Bytes()
	assertNoNegativeObject(t, "WriteSignedTimestamped", o1)

	d1, err := Read(bytes.NewReader(o1), int64(len(o1)))
	if err != nil {
		t.Fatal(err)
	}
	var b2 bytes.Buffer
	if err := d1.WriteArchivalTimestamp(&b2, o1, []*x509.Certificate{cert}, tsaCert, tsaKey); err != nil {
		t.Fatalf("WriteArchivalTimestamp: %v", err)
	}
	out := b2.Bytes()
	assertNoNegativeObject(t, "WriteArchivalTimestamp", out)

	d2, err := Read(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	got := formFields(t, d2)
	sort.Strings(got)
	want := []string{"Applicant", "Signature1", "Timestamp1"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("form fields = %v, want %v", got, want)
	}
	if res := d2.ValidatePAdES(out); len(res) != 1 || res[0].Level != PAdESBLTA || !res[0].Valid {
		t.Errorf("expected one valid B-LTA signature, got %+v", res)
	}
}

// TestSignPromotesDirectAcroForm is the same case on the signing side, where the
// promotion already existed: it guards the two paths staying consistent.
func TestSignPromotesDirectAcroForm(t *testing.T) {
	cert, key := testCertKey(t)
	base := buildPDFWithDirectForm()

	for _, tc := range []struct {
		name  string
		write func(*Document, *bytes.Buffer) error
	}{
		{"WriteSigned", func(d *Document, b *bytes.Buffer) error { return d.WriteSigned(b, cert, key) }},
		{"WriteSignedIncremental", func(d *Document, b *bytes.Buffer) error {
			return d.WriteSignedIncremental(b, base, cert, key)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := Read(bytes.NewReader(base), int64(len(base)))
			if err != nil {
				t.Fatal(err)
			}
			var buf bytes.Buffer
			if err := tc.write(doc, &buf); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			out := buf.Bytes()
			assertNoNegativeObject(t, tc.name, out)

			signed, err := Read(bytes.NewReader(out), int64(len(out)))
			if err != nil {
				t.Fatalf("re-read: %v", err)
			}
			cat := signed.ResolveDict(signed.Trailer.Get("Root"))
			if _, ok := cat.Get("AcroForm").(IndirectRef); !ok {
				t.Errorf("catalog /AcroForm = %#v, want an indirect reference", cat.Get("AcroForm"))
			}
			got := formFields(t, signed)
			sort.Strings(got)
			if strings.Join(got, ",") != "Applicant,Signature1" {
				t.Errorf("form fields = %v, want [Applicant Signature1]", got)
			}
			results := signed.VerifySignatures(out)
			if len(results) != 1 || !results[0].Valid {
				t.Fatalf("signature did not verify: %+v", results)
			}
		})
	}
}

// TestSigningRefusesDirectCatalogOrPage pins the decision for the two malformed
// cases. Both the catalog and the first page are rewritten when a field is
// added, and an incremental update supersedes objects by number, so a structure
// with no number of its own cannot be updated. Promoting it would change the
// identity of something the rest of the file may reference; before the fix the
// -1 was used as an object number instead, silently producing an invalid file.
// A clear error is the answer, identically in both signing files.
func TestSigningRefusesDirectCatalogOrPage(t *testing.T) {
	cert, key := testCertKey(t)
	tsaCert, tsaKey := testTSACertKey(t)

	for _, doc := range []struct {
		name string
		base []byte
		want string
	}{
		{"direct catalog", buildPDFWithDirectCatalog(), "the document catalog is a direct object"},
		{"direct page", buildPDFWithDirectPage(), "the first page is a direct object"},
	} {
		for _, w := range []struct {
			name  string
			write func(*Document, []byte, *bytes.Buffer) error
		}{
			{"WriteSigned", func(d *Document, _ []byte, b *bytes.Buffer) error { return d.WriteSigned(b, cert, key) }},
			{"WriteSignedTimestamped", func(d *Document, _ []byte, b *bytes.Buffer) error {
				return d.WriteSignedTimestamped(b, cert, key, tsaCert, tsaKey)
			}},
			{"WriteSignedIncremental", func(d *Document, raw []byte, b *bytes.Buffer) error {
				return d.WriteSignedIncremental(b, raw, cert, key)
			}},
			{"WriteArchivalTimestamp", func(d *Document, raw []byte, b *bytes.Buffer) error {
				return d.WriteArchivalTimestamp(b, raw, []*x509.Certificate{cert}, tsaCert, tsaKey)
			}},
		} {
			t.Run(doc.name+"/"+w.name, func(t *testing.T) {
				d, err := Read(bytes.NewReader(doc.base), int64(len(doc.base)))
				if err != nil {
					t.Fatalf("read: %v", err)
				}
				var buf bytes.Buffer
				err = w.write(d, doc.base, &buf)
				if err == nil {
					assertNoNegativeObject(t, w.name, buf.Bytes())
					t.Fatalf("%s accepted a document with a %s", w.name, doc.name)
				}
				if !strings.Contains(err.Error(), doc.want) {
					t.Errorf("error = %q, want it to mention %q", err, doc.want)
				}
				if buf.Len() != 0 {
					t.Errorf("%d bytes written despite the error", buf.Len())
				}
			})
		}
	}
}

// TestWriteRefusesNonPositiveObjectNumber is the belt-and-braces guard at the
// writer: whatever puts it there, an object number that is not a positive
// integer (ISO 32000-2 §7.3.10) must never reach the file. Object 0 was already
// refused as the free-list head; a negative number is refused the same way, in
// both writers.
func TestWriteRefusesNonPositiveObjectNumber(t *testing.T) {
	newDoc := func() *Document {
		d := &Document{Version: "2.0", Objects: map[int]*IndirectObject{}, Trailer: Dictionary{}}
		cat := &Dictionary{}
		cat.Set("Type", Name("Catalog"))
		d.Objects[1] = &IndirectObject{Number: 1, Value: cat}
		d.Trailer.Set("Root", IndirectRef{Number: 1})
		return d
	}

	d := newDoc()
	stray := &Dictionary{}
	stray.Set("Type", Name("Bogus"))
	d.Objects[-1] = &IndirectObject{Number: -1, Value: stray}
	var buf bytes.Buffer
	if err := d.Write(&buf); err == nil {
		t.Errorf("Write accepted object number -1 and produced %d bytes", buf.Len())
	} else if !strings.Contains(err.Error(), "-1") {
		t.Errorf("Write error = %q, want it to name the offending number", err)
	}

	base := buildMinimalPDF()
	inc, err := Read(bytes.NewReader(base), int64(len(base)))
	if err != nil {
		t.Fatal(err)
	}
	inc.Objects[-1] = &IndirectObject{Number: -1, Value: stray}
	for _, changed := range [][]int{{-1}, {0}, {1, -1}} {
		var b bytes.Buffer
		if err := inc.WriteIncremental(&b, base, changed); err == nil {
			t.Errorf("WriteIncremental accepted changed=%v", changed)
		}
	}
	// A valid document is unaffected.
	var ok bytes.Buffer
	if err := newDoc().Write(&ok); err != nil {
		t.Errorf("Write of a valid document: %v", err)
	}
}
