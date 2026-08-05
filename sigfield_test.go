package pdf0

import (
	"bytes"
	"crypto/x509"
	"encoding/hex"
	"github.com/mgilbir/pdf0/internal/signtest"
	"github.com/mgilbir/pdf0/object"
	"testing"
)

// TestSignedDocumentReportsFieldName pins that verification reports the name of
// the signature field: WriteSigned builds a field named "Signature1" whose /V
// references the signature dictionary, so both the CMS verification and the
// PAdES assessment must name it.
func TestSignedDocumentReportsFieldName(t *testing.T) {
	cert, key := signtest.CertKey(t)
	base := buildMinimalPDF()
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

	res := signed.VerifySignatures(out)
	if len(res) != 1 {
		t.Fatalf("got %d signatures, want 1", len(res))
	}
	if !res[0].Valid {
		t.Fatalf("signature did not verify: %v", res[0].Err)
	}
	if res[0].Field != "Signature1" {
		t.Errorf("SignatureResult.Field = %q, want %q", res[0].Field, "Signature1")
	}

	pades := signed.ValidatePAdES(out)
	if len(pades) != 1 {
		t.Fatalf("got %d PAdES results, want 1", len(pades))
	}
	if pades[0].Field != "Signature1" {
		t.Errorf("PAdESResult.Field = %q, want %q", pades[0].Field, "Signature1")
	}
}

// sigFieldTestDoc builds an in-memory document holding one signature dictionary
// (object sigNum) plus whatever field/catalog objects the caller supplies, and
// the raw bytes whose /ByteRange the signature covers. The signature itself is
// not cryptographically valid — these tests are about naming and ordering, which
// are reported regardless of the verification verdict.
func sigFieldTestDoc(sigNums ...int) (*Document, []byte) {
	raw := []byte("%PDF-2.0 signature placeholder <00> %%EOF")
	doc := &Document{Objects: map[int]*object.IndirectObject{}}
	for _, num := range sigNums {
		sig := &object.Dictionary{}
		sig.Set("Type", object.Name("Sig"))
		sig.Set("SubFilter", object.Name("ETSI.CAdES.detached"))
		sig.Set("Contents", object.String{Value: []byte{0x00}, IsHex: true})
		sig.Set("ByteRange", object.Array{object.Integer(0), object.Integer(31), object.Integer(35), object.Integer(len(raw) - 35)})
		doc.Objects[num] = &object.IndirectObject{Number: num, Value: sig}
	}
	return doc, raw
}

// setCatalogWithFields gives doc a catalog (object 1) whose /AcroForm (object 2)
// lists the given field references.
func setCatalogWithFields(doc *Document, fields object.Array) {
	form := &object.Dictionary{}
	form.Set("Fields", fields)
	form.Set("SigFlags", object.Integer(3))
	cat := &object.Dictionary{}
	cat.Set("Type", object.Name("Catalog"))
	cat.Set("AcroForm", object.IndirectRef{Number: 2})
	doc.Objects[1] = &object.IndirectObject{Number: 1, Value: cat}
	doc.Objects[2] = &object.IndirectObject{Number: 2, Value: form}
	doc.Trailer.Set("Root", object.IndirectRef{Number: 1})
}

// TestSignatureFieldFullyQualifiedName pins the naming rule for a field that is
// a child in a hierarchy: the reported name is the parent chain's /T values
// joined with "." (ISO 32000-2 §12.7.4.2), not the bare partial name.
func TestSignatureFieldFullyQualifiedName(t *testing.T) {
	doc, raw := sigFieldTestDoc(10)

	child := &object.Dictionary{}
	child.Set("Type", object.Name("Annot"))
	child.Set("Subtype", object.Name("Widget"))
	child.Set("FT", object.Name("Sig"))
	child.Set("T", object.String{Value: []byte("Countersign")})
	child.Set("V", object.IndirectRef{Number: 10})
	child.Set("Parent", object.IndirectRef{Number: 4})
	doc.Objects[5] = &object.IndirectObject{Number: 5, Value: child}

	parent := &object.Dictionary{}
	parent.Set("T", object.String{Value: []byte("Approvals")})
	parent.Set("Kids", object.Array{object.IndirectRef{Number: 5}})
	doc.Objects[4] = &object.IndirectObject{Number: 4, Value: parent}

	setCatalogWithFields(doc, object.Array{object.IndirectRef{Number: 4}})

	res := doc.VerifySignatures(raw)
	if len(res) != 1 {
		t.Fatalf("got %d signatures, want 1", len(res))
	}
	if want := "Approvals.Countersign"; res[0].Field != want {
		t.Errorf("SignatureResult.Field = %q, want %q", res[0].Field, want)
	}
	pades := doc.ValidatePAdES(raw)
	if len(pades) != 1 {
		t.Fatalf("got %d PAdES results, want 1", len(pades))
	}
	if want := "Approvals.Countersign"; pades[0].Field != want {
		t.Errorf("PAdESResult.Field = %q, want %q", pades[0].Field, want)
	}
}

// TestSignatureFieldFromPageOnlyWidget covers a signature field that the
// /AcroForm does not list: the name is still recovered, from the widget's own
// /Parent chain.
func TestSignatureFieldFromPageOnlyWidget(t *testing.T) {
	doc, raw := sigFieldTestDoc(10)

	field := &object.Dictionary{}
	field.Set("Type", object.Name("Annot"))
	field.Set("Subtype", object.Name("Widget"))
	field.Set("FT", object.Name("Sig"))
	field.Set("T", object.String{Value: []byte("Orphan")})
	field.Set("V", object.IndirectRef{Number: 10})
	doc.Objects[5] = &object.IndirectObject{Number: 5, Value: field}
	setCatalogWithFields(doc, object.Array{}) // an AcroForm that lists no fields at all

	res := doc.VerifySignatures(raw)
	if len(res) != 1 {
		t.Fatalf("got %d signatures, want 1", len(res))
	}
	if res[0].Field != "Orphan" {
		t.Errorf("Field = %q, want %q", res[0].Field, "Orphan")
	}
}

// TestBareSignatureHasNoFieldName documents the legitimately empty case: a
// signature dictionary that no field's /V references cannot be named.
func TestBareSignatureHasNoFieldName(t *testing.T) {
	doc, raw := sigFieldTestDoc(10)
	res := doc.VerifySignatures(raw)
	if len(res) != 1 {
		t.Fatalf("got %d signatures, want 1", len(res))
	}
	if res[0].Field != "" {
		t.Errorf("Field = %q, want empty for a signature no field references", res[0].Field)
	}
}

// TestSignatureResultOrderIsByObjectNumber pins the result order: signatures are
// reported in ascending object-number order, not in the map order of
// Document.Objects. The field names are deliberately the reverse of the object
// order so an accidental pass is not possible.
func TestSignatureResultOrderIsByObjectNumber(t *testing.T) {
	doc, raw := sigFieldTestDoc(11, 20)

	first := &object.Dictionary{}
	first.Set("FT", object.Name("Sig"))
	first.Set("T", object.String{Value: []byte("Zulu")})
	first.Set("V", object.IndirectRef{Number: 11})
	doc.Objects[12] = &object.IndirectObject{Number: 12, Value: first}

	second := &object.Dictionary{}
	second.Set("FT", object.Name("Sig"))
	second.Set("T", object.String{Value: []byte("Alpha")})
	second.Set("V", object.IndirectRef{Number: 20})
	doc.Objects[21] = &object.IndirectObject{Number: 21, Value: second}

	setCatalogWithFields(doc, object.Array{object.IndirectRef{Number: 21}, object.IndirectRef{Number: 12}})

	// Give the two signatures distinguishable verdicts as well, so the order is
	// pinned by something other than the field name: object 11 gets a malformed
	// /ByteRange, object 20 keeps a well-formed one.
	sig11 := doc.Objects[11].Value.(*object.Dictionary)
	sig11.Set("ByteRange", object.Array{object.Name("bogus")})

	wantNames := []string{"Zulu", "Alpha"} // objects 11 then 20
	wantErrs := []string{"malformed /ByteRange", "not a CMS SignedData"}
	for run := 0; run < 20; run++ {
		res := doc.VerifySignatures(raw)
		if len(res) != 2 {
			t.Fatalf("got %d signatures, want 2", len(res))
		}
		for i := range res {
			if res[i].Field != wantNames[i] {
				t.Fatalf("run %d: result %d Field = %q, want %q (results must be in object-number order)", run, i, res[i].Field, wantNames[i])
			}
			if res[i].Err == nil || res[i].Err.Error() != wantErrs[i] {
				t.Fatalf("run %d: result %d Err = %v, want %q (results must be in object-number order)", run, i, res[i].Err, wantErrs[i])
			}
		}
		pades := doc.ValidatePAdES(raw)
		if len(pades) != 2 {
			t.Fatalf("got %d PAdES results, want 2", len(pades))
		}
		for i := range pades {
			if pades[i].Field != wantNames[i] {
				t.Fatalf("run %d: PAdES result %d Field = %q, want %q", run, i, pades[i].Field, wantNames[i])
			}
		}
	}
}

// TestTwoRealSignaturesOrderAndNames builds a document holding two signature
// dictionaries produced by pdf0 itself — an approval signature (field
// "Signature1") and an archival document time-stamp added as an incremental
// update (field "Timestamp1") — and pins that VerifySignatures names both and
// reports them in object-number order, which here is the order they were
// written.
func TestTwoRealSignaturesOrderAndNames(t *testing.T) {
	cert, key := signtest.CertKey(t)
	tsaCert, tsaKey := signtest.TSACertKey(t)
	base := buildMinimalPDF()
	doc, err := Read(bytes.NewReader(base), int64(len(base)))
	if err != nil {
		t.Fatal(err)
	}
	var b1 bytes.Buffer
	if err := doc.WriteSignedTimestamped(&b1, cert, key, tsaCert, tsaKey); err != nil {
		t.Fatalf("WriteSignedTimestamped: %v", err)
	}
	o1 := b1.Bytes()
	d1, err := Read(bytes.NewReader(o1), int64(len(o1)))
	if err != nil {
		t.Fatal(err)
	}
	var b2 bytes.Buffer
	if err := d1.WriteArchivalTimestamp(&b2, o1, []*x509.Certificate{cert}, tsaCert, tsaKey); err != nil {
		t.Fatalf("WriteArchivalTimestamp: %v", err)
	}
	out := b2.Bytes()
	d2, err := Read(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}

	res := d2.VerifySignaturesWithRoots(out, nil)
	if len(res) != 2 {
		t.Fatalf("got %d signature dictionaries, want 2 (%+v)", len(res), res)
	}
	got := []string{res[0].Field, res[1].Field}
	if got[0] != "Signature1" || got[1] != "Timestamp1" {
		t.Fatalf("fields = %v, want [Signature1 Timestamp1] in object-number order", got)
	}
	if !res[0].Valid {
		t.Errorf("the approval signature should verify: %v", res[0].Err)
	}

	// ValidatePAdES assesses only the approval signature, and names it too.
	pades := d2.ValidatePAdES(out)
	if len(pades) != 1 {
		t.Fatalf("got %d PAdES results, want 1", len(pades))
	}
	if pades[0].Field != "Signature1" {
		t.Errorf("PAdESResult.Field = %q, want %q", pades[0].Field, "Signature1")
	}
}

// TestSignatureFieldNameIndirectValue guards the identity comparison: /V may be
// an indirect reference (it normally is), and may even be a reference to a
// reference, so the field must be matched by object number rather than by
// pointer equality on the unresolved value.
func TestSignatureFieldNameIndirectValue(t *testing.T) {
	doc, raw := sigFieldTestDoc(10)
	// Object 7 is an indirect reference to the signature dictionary; the field's
	// /V points at object 7, so resolving the chain is required.
	doc.Objects[7] = &object.IndirectObject{Number: 7, Value: object.IndirectRef{Number: 10}}

	field := &object.Dictionary{}
	field.Set("FT", object.Name("Sig"))
	field.Set("T", object.String{Value: []byte("Indirect")})
	field.Set("V", object.IndirectRef{Number: 7})
	doc.Objects[5] = &object.IndirectObject{Number: 5, Value: field}
	setCatalogWithFields(doc, object.Array{object.IndirectRef{Number: 5}})

	res := doc.VerifySignatures(raw)
	if len(res) != 1 {
		t.Fatalf("got %d signatures, want 1", len(res))
	}
	if res[0].Field != "Indirect" {
		t.Errorf("Field = %q, want %q", res[0].Field, "Indirect")
	}
}

// TestSignatureFieldCyclicHierarchy makes sure a malformed document whose field
// hierarchy loops cannot hang the naming walk (the validator reads untrusted
// files).
func TestSignatureFieldCyclicHierarchy(t *testing.T) {
	doc, raw := sigFieldTestDoc(10)

	a := &object.Dictionary{}
	a.Set("T", object.String{Value: []byte("A")})
	a.Set("Kids", object.Array{object.IndirectRef{Number: 6}})
	a.Set("Parent", object.IndirectRef{Number: 6})
	doc.Objects[5] = &object.IndirectObject{Number: 5, Value: a}

	b := &object.Dictionary{}
	b.Set("T", object.String{Value: []byte("B")})
	b.Set("Kids", object.Array{object.IndirectRef{Number: 5}})
	b.Set("Parent", object.IndirectRef{Number: 5})
	b.Set("V", object.IndirectRef{Number: 10})
	doc.Objects[6] = &object.IndirectObject{Number: 6, Value: b}

	setCatalogWithFields(doc, object.Array{object.IndirectRef{Number: 5}})

	res := doc.VerifySignatures(raw)
	if len(res) != 1 {
		t.Fatalf("got %d signatures, want 1", len(res))
	}
	if want := "A.B"; res[0].Field != want {
		t.Errorf("Field = %q, want %q", res[0].Field, want)
	}
}

// TestSignatureFieldNameUTF16 checks that a /T stored as a UTF-16BE PDF text
// string is decoded, not returned as raw bytes.
func TestSignatureFieldNameUTF16(t *testing.T) {
	doc, raw := sigFieldTestDoc(10)
	utf16Name, err := hex.DecodeString("FEFF0053006900670144") // BOM + "Sig" + U+0144
	if err != nil {
		t.Fatal(err)
	}
	field := &object.Dictionary{}
	field.Set("FT", object.Name("Sig"))
	field.Set("T", object.String{Value: utf16Name})
	field.Set("V", object.IndirectRef{Number: 10})
	doc.Objects[5] = &object.IndirectObject{Number: 5, Value: field}
	setCatalogWithFields(doc, object.Array{object.IndirectRef{Number: 5}})

	res := doc.VerifySignatures(raw)
	if len(res) != 1 {
		t.Fatalf("got %d signatures, want 1", len(res))
	}
	if want := "Sigń"; res[0].Field != want {
		t.Errorf("Field = %q, want %q", res[0].Field, want)
	}
}
