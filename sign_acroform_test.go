package pdf0

import (
	"bytes"
	"fmt"
	"github.com/mgilbir/pdf0/internal/signtest"
	"github.com/mgilbir/pdf0/sign"
	"sort"
	"testing"
)

// buildPDFWithFormField builds a one-page document that already has an
// interactive form: a text field, plus the form-level keys a producer sets
// (/DA, /DR, /NeedAppearances, /Q) and a /SigFlags value carrying a bit outside
// the two this package sets. Signing must extend this form, not replace it.
func buildPDFWithFormField() []byte {
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
	buf.WriteString("5 0 obj\n<< /Fields [6 0 R] /SigFlags 4 /DA (/Helv 0 Tf 0 g) /DR << /Font << >> >> /NeedAppearances true /Q 1 >>\nendobj\n")
	offs = append(offs, buf.Len())
	buf.WriteString("6 0 obj\n<< /Type /Annot /Subtype /Widget /FT /Tx /T (Applicant) /Rect [72 600 300 620] /P 3 0 R >>\nendobj\n")

	xrefOffset := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n", len(offs)+1)
	buf.WriteString("0000000000 65535 f \r\n")
	for _, o := range offs {
		fmt.Fprintf(&buf, "%010d 00000 n \r\n", o)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\n", len(offs)+1)
	fmt.Fprintf(&buf, "startxref\n%d\n%%%%EOF\n", xrefOffset)
	return buf.Bytes()
}

// formFields returns the /T of every field the document's /AcroForm /Fields
// lists, resolved through the references, so a test can say what the form
// actually contains.
func formFields(t *testing.T, d *Document) []string {
	t.Helper()
	cat := d.ResolveDict(d.Trailer.Get("Root"))
	if cat == nil {
		t.Fatal("signed document has no catalog")
	}
	form := d.ResolveDict(cat.Get("AcroForm"))
	if form == nil {
		t.Fatal("signed document has no /AcroForm")
	}
	fields, _ := d.Resolve(form.Get("Fields")).(Array)
	names := make([]string, 0, len(fields))
	for _, f := range fields {
		fd := d.ResolveDict(f)
		if fd == nil {
			t.Fatalf("/Fields entry %v does not resolve to a dictionary", f)
		}
		names = append(names, sign.FieldPartialName(d.view(), fd))
	}
	return names
}

// TestSecondSignatureFieldIsDistinct is the regression test for two defects that
// break multi-signature documents, both in withSignatureField.
//
// The field name was hardcoded to "Signature1", so signing twice produced two
// fields with the same fully qualified name — non-conformant (ISO 32000-2
// §12.7.4.2 requires uniqueness) and enough to make SignatureResult.Field
// useless for telling two signatures apart. And the catalog's /AcroForm was
// replaced by a fresh dictionary listing only the new field, so the first
// signature's field was orphaned: a viewer enumerating the form saw one
// signature where the file holds two.
func TestSecondSignatureFieldIsDistinct(t *testing.T) {
	cert, key := signtest.CertKey(t)

	base := buildPDFWithPageContents()
	doc, err := Read(bytes.NewReader(base), int64(len(base)))
	if err != nil {
		t.Fatal(err)
	}
	var first bytes.Buffer
	if err := doc.WriteSigned(&first, cert, key); err != nil {
		t.Fatalf("first WriteSigned: %v", err)
	}
	onceSigned := first.Bytes()

	reread, err := Read(bytes.NewReader(onceSigned), int64(len(onceSigned)))
	if err != nil {
		t.Fatalf("re-read once-signed: %v", err)
	}
	var second bytes.Buffer
	if err := reread.WriteSignedIncremental(&second, onceSigned, cert, key); err != nil {
		t.Fatalf("second (incremental) signing: %v", err)
	}
	out := second.Bytes()
	if !bytes.HasPrefix(out, onceSigned) {
		t.Fatal("the incremental second signature altered the first signature's bytes")
	}

	twice, err := Read(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatalf("re-read twice-signed: %v", err)
	}

	// Both signatures are reported, under distinct names, in signing order.
	res := twice.VerifySignatures(out)
	if len(res) != 2 {
		t.Fatalf("got %d signatures, want 2", len(res))
	}
	if res[0].Field != "Signature1" || res[1].Field != "Signature2" {
		t.Errorf("signature field names = [%q %q], want [Signature1 Signature2]", res[0].Field, res[1].Field)
	}

	// Both still verify, and exactly one — the newest — covers the whole file.
	covering := 0
	for i, r := range res {
		if !r.Valid || r.Err != nil {
			t.Errorf("signature %d (%q) invalid: valid=%v err=%v", i, r.Field, r.Valid, r.Err)
		}
		if r.CoversWholeDocument {
			covering++
		}
	}
	if covering != 1 {
		t.Errorf("got %d signatures covering the whole document, want exactly 1", covering)
	}

	// The form lists both fields: the first signature's field must not be
	// orphaned by the second signing.
	names := formFields(t, twice)
	sort.Strings(names)
	if len(names) != 2 || names[0] != "Signature1" || names[1] != "Signature2" {
		t.Errorf("/AcroForm /Fields = %v, want both signature fields", names)
	}
}

// TestSigningPreservesExistingForm covers the other half of the AcroForm defect:
// a document whose form already holds a non-signature field. Signing must leave
// that field in /Fields and keep the form-level keys, and /SigFlags is a bit
// field (ISO 32000-2 Table 225: bit 1 SignaturesExist, bit 2 AppendOnly) so the
// signature bits are OR-ed in rather than overwriting what is there.
func TestSigningPreservesExistingForm(t *testing.T) {
	cert, key := signtest.CertKey(t)
	base := buildPDFWithFormField()
	doc, err := Read(bytes.NewReader(base), int64(len(base)))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := doc.WriteSigned(&buf, cert, key); err != nil {
		t.Fatalf("WriteSigned: %v", err)
	}
	out := buf.Bytes()
	signed, err := Read(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatalf("re-read signed: %v", err)
	}

	names := formFields(t, signed)
	sort.Strings(names)
	if len(names) != 2 || names[0] != "Applicant" || names[1] != "Signature1" {
		t.Errorf("/AcroForm /Fields = %v, want the pre-existing Applicant field plus Signature1", names)
	}

	cat := signed.ResolveDict(signed.Trailer.Get("Root"))
	form := signed.ResolveDict(cat.Get("AcroForm"))
	if form == nil {
		t.Fatal("signed document has no /AcroForm")
	}
	// The signature bits are added to the existing value (4|3 == 7); an assign
	// would report 3 and silently clear a bit the producer set.
	if flags, _ := signed.Resolve(form.Get("SigFlags")).(Integer); flags != 7 {
		t.Errorf("/SigFlags = %v, want 7 (existing 4 OR the signature bits 3)", flags)
	}
	if da, _ := signed.Resolve(form.Get("DA")).(String); string(da.Value) != "/Helv 0 Tf 0 g" {
		t.Errorf("/DA = %q, want the form's default appearance to survive", da.Value)
	}
	if signed.ResolveDict(form.Get("DR")) == nil {
		t.Error("/DR dropped from the form")
	}
	if na, _ := signed.Resolve(form.Get("NeedAppearances")).(Boolean); !bool(na) {
		t.Error("/NeedAppearances dropped from the form")
	}
	if q, _ := signed.Resolve(form.Get("Q")).(Integer); q != 1 {
		t.Errorf("/Q = %v, want 1", q)
	}

	// The signature itself must still be sound.
	res := signed.VerifySignatures(out)
	if len(res) != 1 || !res[0].Valid || !res[0].CoversWholeDocument {
		t.Fatalf("signature did not verify: %+v", res)
	}
}

// TestSignIncrementalPreservesExistingForm pins the same preservation on the
// incremental path, where it matters most: WriteSignedIncremental exists to add
// a signature without disturbing what is already in the file. It also pins that
// the existing /AcroForm object is the one updated — if withSignatureField
// allocated a fresh object but left it out of the changed list, or updated an
// object the incremental xref never mentions, the appended update would not take
// effect and the re-read form would still show one field.
func TestSignIncrementalPreservesExistingForm(t *testing.T) {
	cert, key := signtest.CertKey(t)
	original := buildPDFWithFormField()
	doc, err := Read(bytes.NewReader(original), int64(len(original)))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := doc.WriteSignedIncremental(&buf, original, cert, key); err != nil {
		t.Fatalf("WriteSignedIncremental: %v", err)
	}
	out := buf.Bytes()
	if !bytes.HasPrefix(out, original) {
		t.Fatal("incremental signature altered the original bytes")
	}
	signed, err := Read(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}

	names := formFields(t, signed)
	sort.Strings(names)
	if len(names) != 2 || names[0] != "Applicant" || names[1] != "Signature1" {
		t.Errorf("/AcroForm /Fields = %v, want the pre-existing Applicant field plus Signature1", names)
	}
	res := signed.VerifySignatures(out)
	if len(res) != 1 || !res[0].Valid || !res[0].CoversWholeDocument {
		t.Fatalf("incremental signature did not verify: %+v", res)
	}
}

// TestFreeSignatureFieldNameSkipsOrphanedField covers the name-collision scan
// reaching past /Fields: a signature field the form does not list (pdf0 itself
// produced such files before the form was preserved, and page-only widgets are
// common) still occupies its name.
func TestFreeSignatureFieldNameSkipsOrphanedField(t *testing.T) {
	doc, _ := sigFieldTestDoc(10)
	orphan := &Dictionary{}
	orphan.Set("FT", Name("Sig"))
	orphan.Set("T", String{Value: []byte("Signature1")})
	orphan.Set("V", IndirectRef{Number: 10})
	doc.Objects[5] = &IndirectObject{Number: 5, Value: orphan}
	setCatalogWithFields(doc, Array{}) // an /AcroForm that lists no fields at all

	cat := doc.ResolveDict(doc.Trailer.Get("Root"))
	if got := freeFieldName(doc, cat, "Signature"); got != "Signature2" {
		t.Errorf("freeFieldName = %q, want Signature2: the orphaned field already holds Signature1", got)
	}
}
