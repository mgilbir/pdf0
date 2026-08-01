package pdf0

import (
	"bytes"
	"crypto/x509"
	"fmt"
	"sort"
	"strings"
	"testing"
)

// This file pins field naming for the two writers that add a signature field.
// ISO 32000-2 §12.7.4.2 requires fully qualified field names to be unique, and
// both fields are added at the top level of the interactive form, so the
// partial name written into /T is the qualified name.
//
// The bug these pin: withArchivalTimestamp set a literal "Timestamp1", so a
// second archival time-stamp produced a second field with the same name, and a
// time-stamp added to a document that already had a "Timestamp1" field
// collided with it. The signing path had already grown a free-name scan;
// the time-stamp path did not use it. Duplicate names are not only invalid —
// they defeat SignatureResult.Field and PAdESResult.Field, whose whole job is
// to say which field a result belongs to.

// buildPDFWithNamedField builds a one-page document carrying a single
// interactive-form field with the given partial name, so a test can occupy a
// name before signing or time-stamping.
func buildPDFWithNamedField(name string) []byte {
	var buf bytes.Buffer
	buf.WriteString("%PDF-2.0\n%\x80\x80\x80\x80\n")

	body := "BT /F1 12 Tf 72 700 Td (hello) Tj ET\n"

	offs := make([]int, 0, 6)
	offs = append(offs, buf.Len())
	buf.WriteString("1 0 obj\n<< /Type /Catalog /Pages 2 0 R /AcroForm 5 0 R >>\nendobj\n")
	offs = append(offs, buf.Len())
	buf.WriteString("2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n")
	offs = append(offs, buf.Len())
	buf.WriteString("3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Annots [6 0 R] >>\nendobj\n")
	offs = append(offs, buf.Len())
	fmt.Fprintf(&buf, "4 0 obj\n<< /Length %d >>\nstream\n%sendstream\nendobj\n", len(body), body)
	offs = append(offs, buf.Len())
	buf.WriteString("5 0 obj\n<< /Fields [6 0 R] /SigFlags 3 >>\nendobj\n")
	offs = append(offs, buf.Len())
	fmt.Fprintf(&buf, "6 0 obj\n<< /Type /Annot /Subtype /Widget /FT /Tx /T (%s) /Rect [72 600 300 620] /P 3 0 R >>\nendobj\n", name)

	return finishTestPDF(&buf, offs, "<< /Size %d /Root 1 0 R >>")
}

// allFieldNames returns the /T of every field-like dictionary in the document,
// whether or not the interactive form lists it, so a test can catch a duplicate
// name wherever it is. Sorted, for a stable comparison.
func allFieldNames(t *testing.T, d *Document) []string {
	t.Helper()
	var names []string
	for _, iobj := range d.Objects {
		fd, ok := iobj.Value.(*Dictionary)
		if !ok || (fd.Get("FT") == nil && fd.Get("V") == nil) {
			continue
		}
		if n := qualifiedFieldName(d, fd); n != "" {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names
}

// TestTwoArchivalTimestampsGetDistinctNames is the regression test for the
// hardcoded name: two archival time-stamps on one document must end up in two
// differently named fields. Before the fix both were "Timestamp1".
func TestTwoArchivalTimestampsGetDistinctNames(t *testing.T) {
	cert, _ := testCertKey(t)
	tsaCert, tsaKey := testTSACertKey(t)

	base := buildPDFWithPageContents()
	d0, err := Read(bytes.NewReader(base), int64(len(base)))
	if err != nil {
		t.Fatal(err)
	}
	var b1 bytes.Buffer
	if err := d0.WriteArchivalTimestamp(&b1, base, []*x509.Certificate{cert}, tsaCert, tsaKey); err != nil {
		t.Fatalf("first WriteArchivalTimestamp: %v", err)
	}
	o1 := b1.Bytes()
	d1, err := Read(bytes.NewReader(o1), int64(len(o1)))
	if err != nil {
		t.Fatalf("re-read after the first time-stamp: %v", err)
	}
	if got := formFields(t, d1); len(got) != 1 || got[0] != "Timestamp1" {
		t.Fatalf("after one time-stamp the form holds %v, want [Timestamp1]", got)
	}

	var b2 bytes.Buffer
	if err := d1.WriteArchivalTimestamp(&b2, o1, []*x509.Certificate{cert}, tsaCert, tsaKey); err != nil {
		t.Fatalf("second WriteArchivalTimestamp: %v", err)
	}
	o2 := b2.Bytes()
	if !bytes.HasPrefix(o2, o1) {
		t.Fatal("the second time-stamp altered the bytes the first one sealed")
	}
	d2, err := Read(bytes.NewReader(o2), int64(len(o2)))
	if err != nil {
		t.Fatalf("re-read after the second time-stamp: %v", err)
	}

	got := formFields(t, d2)
	sort.Strings(got)
	if strings.Join(got, ",") != "Timestamp1,Timestamp2" {
		t.Errorf("/AcroForm /Fields = %v, want [Timestamp1 Timestamp2]: two fields may not share a name (ISO 32000-2 §12.7.4.2)", got)
	}
	if all := allFieldNames(t, d2); strings.Join(all, ",") != "Timestamp1,Timestamp2" {
		t.Errorf("field names in the file = %v, want [Timestamp1 Timestamp2]", all)
	}
	// The names are only useful if the results carry them: two time-stamps, two
	// distinct field names, in object-number order.
	res := d2.VerifySignaturesWithRoots(o2, nil)
	if len(res) != 2 {
		t.Fatalf("got %d signature dictionaries, want 2 (%+v)", len(res), res)
	}
	if res[0].Field != "Timestamp1" || res[1].Field != "Timestamp2" {
		t.Errorf("result fields = [%q %q], want [Timestamp1 Timestamp2]", res[0].Field, res[1].Field)
	}
	if !coveringDocTimestamp(d2, o2) {
		t.Error("the outermost archival time-stamp does not verify over the file it seals")
	}
}

// TestArchivalTimestampSkipsTakenTimestampName covers the other half: the name
// need not have been produced by pdf0. A document that already carries a field
// called "Timestamp1" — for any reason — must not get a second one.
func TestArchivalTimestampSkipsTakenTimestampName(t *testing.T) {
	cert, _ := testCertKey(t)
	tsaCert, tsaKey := testTSACertKey(t)

	base := buildPDFWithNamedField("Timestamp1")
	doc, err := Read(bytes.NewReader(base), int64(len(base)))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := doc.WriteArchivalTimestamp(&buf, base, []*x509.Certificate{cert}, tsaCert, tsaKey); err != nil {
		t.Fatalf("WriteArchivalTimestamp: %v", err)
	}
	out := buf.Bytes()
	d2, err := Read(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	got := formFields(t, d2)
	sort.Strings(got)
	if strings.Join(got, ",") != "Timestamp1,Timestamp2" {
		t.Errorf("/AcroForm /Fields = %v, want [Timestamp1 Timestamp2]: the pre-existing field already holds Timestamp1", got)
	}
}

// TestSignatureThenTimestampNames pins the conventional names, which the fix
// must not disturb: the first signature of a fresh document is "Signature1" and
// the first time-stamp "Timestamp1", and the two counters are independent — a
// time-stamp added to a signed document does not become "Timestamp2" because a
// "Signature1" exists.
func TestSignatureThenTimestampNames(t *testing.T) {
	cert, key := testCertKey(t)
	tsaCert, tsaKey := testTSACertKey(t)

	base := buildPDFWithPageContents()
	d0, err := Read(bytes.NewReader(base), int64(len(base)))
	if err != nil {
		t.Fatal(err)
	}
	var b1 bytes.Buffer
	if err := d0.WriteSignedTimestamped(&b1, cert, key, tsaCert, tsaKey); err != nil {
		t.Fatalf("WriteSignedTimestamped: %v", err)
	}
	o1 := b1.Bytes()
	d1, err := Read(bytes.NewReader(o1), int64(len(o1)))
	if err != nil {
		t.Fatal(err)
	}
	if got := formFields(t, d1); len(got) != 1 || got[0] != "Signature1" {
		t.Fatalf("after signing the form holds %v, want [Signature1]", got)
	}

	var b2 bytes.Buffer
	if err := d1.WriteArchivalTimestamp(&b2, o1, []*x509.Certificate{cert}, tsaCert, tsaKey); err != nil {
		t.Fatalf("WriteArchivalTimestamp: %v", err)
	}
	o2 := b2.Bytes()
	d2, err := Read(bytes.NewReader(o2), int64(len(o2)))
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	got := formFields(t, d2)
	sort.Strings(got)
	if strings.Join(got, ",") != "Signature1,Timestamp1" {
		t.Errorf("/AcroForm /Fields = %v, want [Signature1 Timestamp1]", got)
	}
	if res := d2.ValidatePAdES(o2); len(res) != 1 || res[0].Field != "Signature1" || res[0].Level != PAdESBLTA {
		t.Errorf("ValidatePAdES = %+v, want one B-LTA result for Signature1", res)
	}
}
