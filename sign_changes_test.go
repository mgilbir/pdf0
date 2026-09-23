package pdf0

import (
	"bytes"
	"crypto"
	"crypto/x509"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/signtest"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/sign"
	"github.com/mgilbir/pdf0/syntax"
)

// The allowed-changes analysis (sign/changes.go over signedfile.go's revision
// diff): which changes after a signature are permitted, and the ways a crafted
// update tries to slip a change past it.

// certifiedMinimal signs the minimal document with a certification signature
// whose DocMDP transform has permission level p (ISO 32000-2 12.8.2.2): the
// signature dictionary carries a /Reference to the DocMDP transform and the
// catalog's /Perms /DocMDP names it. pdf0 has no API for certifying, so the
// test lays the dictionaries out itself and signs with the writer's own patch
// step.
func certifiedMinimal(t *testing.T, p int, cert *x509.Certificate, key crypto.Signer) []byte {
	t.Helper()
	d := readBytes(t, buildMinimalPDF())
	clone, _, err := withSignatureField(d)
	if err != nil {
		t.Fatal(err)
	}
	sigNum := -1
	for n, iobj := range clone.Objects {
		if sd, ok := iobj.Value.(*object.Dictionary); ok && sd.Get("ByteRange") != nil {
			sigNum = n
		}
	}
	sd := clone.Objects[sigNum].Value.(*object.Dictionary)
	sd.Set("Reference", object.Array{object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("SigRef")},
		object.Entry{Key: "TransformMethod", Value: object.Name("DocMDP")},
		object.Entry{Key: "TransformParams", Value: object.NewDictionary(
			object.Entry{Key: "Type", Value: object.Name("TransformParams")},
			object.Entry{Key: "P", Value: object.Integer(p)},
			object.Entry{Key: "V", Value: object.Name("1.2")},
		)},
	)})
	catNum := object.RefNum(clone.Trailer.Get("Root"))
	cat := clone.Objects[catNum].Value.(*object.Dictionary).Clone()
	cat.Set("Perms", object.NewDictionary(object.Entry{Key: "DocMDP", Value: object.IndirectRef{Number: sigNum}}))
	clone.Objects[catNum] = &object.IndirectObject{Number: catNum, Value: cat}
	var buf bytes.Buffer
	if err := clone.Write(&buf); err != nil {
		t.Fatal(err)
	}
	out, err := patchSignature(buf.Bytes(), cert, key, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// firstSignature returns the result of the lowest-numbered approval signature.
func firstSignature(t *testing.T, data []byte) sign.Result {
	t.Helper()
	approval, _ := approvalAndTimestamps(verifySigs(t, readBytes(t, data), sign.VerifyOptions{}))
	if len(approval) == 0 {
		t.Fatal("no signature")
	}
	return approval[0]
}

// TestDocMDPGovernsLaterSigning pins what each certification level permits
// after the certification signature (ISO 32000-2 Table 257): a DSS and a
// document time-stamp at every level, and a further signature at P 2 and 3
// but not P 1. Where no certification signature governs the document no
// modification-detection restriction applies (12.8.2.2), and a further
// approval signature — the multi-signer workflow — is permitted too. A page
// change is never permitted.
func TestDocMDPGovernsLaterSigning(t *testing.T) {
	cert, key := signtest.CertKey(t)
	tsaCert, tsaKey := signtest.TSACertKey(t)
	addSignature := func(data []byte) []byte {
		var out bytes.Buffer
		if err := readBytes(t, data).WriteSignedIncremental(&out, cert, key); err != nil {
			t.Fatalf("WriteSignedIncremental: %v", err)
		}
		return out.Bytes()
	}
	for _, tc := range []struct {
		p                     int // 0: an approval signature, no certification
		archival, signing, pg bool
	}{
		{p: 0, archival: true, signing: true},
		{p: 1, archival: true, signing: false},
		{p: 2, archival: true, signing: true},
		{p: 3, archival: true, signing: true},
	} {
		t.Run(fmt.Sprintf("P=%d", tc.p), func(t *testing.T) {
			var base []byte
			if tc.p == 0 {
				base = signMinimal(t)
			} else {
				base = certifiedMinimal(t, tc.p, cert, key)
			}
			if r := firstSignature(t, base); !r.DocumentUnmodified() {
				t.Fatalf("the signature must verify over its own file first: %+v", r)
			}
			if r := firstSignature(t, archive(t, base, ValidationData{Certs: []*x509.Certificate{cert}}, tsaCert, tsaKey)); r.ChangesAllowed != tc.archival {
				t.Errorf("after an archival update ChangesAllowed = %v, want %v: %v", r.ChangesAllowed, tc.archival, r.DisallowedChanges)
			}
			r := firstSignature(t, addSignature(base))
			if r.ChangesAllowed != tc.signing {
				t.Errorf("after a second signature ChangesAllowed = %v, want %v: %v", r.ChangesAllowed, tc.signing, r.DisallowedChanges)
			}
			if !tc.signing && !anyContains(r.DisallowedChanges, "DocMDP permission level (P 1)") {
				t.Errorf("the refusal should name the P 1 certification: %v", r.DisallowedChanges)
			}
			if r := firstSignature(t, tamperPage(t, base)); r.ChangesAllowed || !anyContains(r.DisallowedChanges, "MediaBox") {
				t.Errorf("a page change is never permitted: %+v", r.DisallowedChanges)
			}
		})
	}
}

// TestArchivalUpdatesAreRecognisedRepeatedly runs the B-LTA renewal cycle —
// two archival updates, the second extending the first's DSS — and checks
// every signature and time-stamp stays intact through it: an existing DSS
// changing is archival too.
func TestArchivalUpdatesAreRecognisedRepeatedly(t *testing.T) {
	ca, caKey := signtest.CA(t, "pdf0 renewal CA")
	tsaCert, tsaKey := signtest.TSAIssuedBy(t, ca, caKey)
	signed, leaf := signedByLeaf(t, ca, caKey, leafTemplate("pdf0 renewal signer"))
	crl := signtest.MakeCRL(t, ca, caKey, nil)
	ocsp := signtest.MakeOCSP(t, leaf, ca, caKey, "good")

	once := archive(t, signed, ValidationData{Certs: []*x509.Certificate{ca}, CRLs: [][]byte{crl}}, tsaCert, tsaKey)
	twice := archive(t, once, ValidationData{Certs: []*x509.Certificate{ca, tsaCert}, OCSPs: [][]byte{ocsp}}, tsaCert, tsaKey)
	d := readBytes(t, twice)
	if !coveringDocTimestamp(t, d) {
		t.Error("the newest time-stamp should cover the file")
	}

	// C60: the second update extended the DSS rather than replacing it.
	crls, ocsps := d.DSSRevocationMaterial()
	if len(crls) != 1 || !bytes.Equal(crls[0], crl) {
		t.Errorf("the first update's CRL is gone from the DSS: %d CRLs", len(crls))
	}
	if len(ocsps) != 1 || !bytes.Equal(ocsps[0], ocsp) {
		t.Errorf("the second update's OCSP response is missing: %d responses", len(ocsps))
	}
	certs := d.DSSCerts()
	if len(certs) != 2 || !certs[0].Equal(ca) || !certs[1].Equal(tsaCert) {
		t.Errorf("DSS certificates = %d, want the CA once (not duplicated) and the TSA", len(certs))
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	approval, _ := approvalAndTimestamps(verifySigs(t, d, sign.VerifyOptions{Roots: roots}))
	if len(approval) != 1 || !approval[0].Intact() || !approval[0].TrustedChain || approval[0].Revocation.Status != sign.RevocationGood {
		t.Errorf("the signature should be intact, trusted and good after two renewals: %+v", approval)
	}
}

// TestUpdateWithoutEOFIsAChange: an update appended with no %%EOF of its own
// is grouped into the revision before it (Source.Revisions), and a reader
// still follows it. It must be compared as a change after the signature, not
// taken for part of the signed revision.
func TestUpdateWithoutEOFIsAChange(t *testing.T) {
	signed := signMinimal(t)
	tampered := tamperPage(t, signed)
	i := bytes.LastIndex(tampered, []byte("%%EOF"))
	if i < 0 {
		t.Fatal("no end-of-file marker")
	}
	unterminated := tampered[:i] // the update's startxref stays; its %%EOF goes
	d := readBytes(t, unterminated)
	if mb := d.Objects[firstPageNum(t, d)].Value.(*object.Dictionary).Get("MediaBox"); !object.Equal(mb, object.Array{object.Integer(0), object.Integer(0), object.Integer(10), object.Integer(10)}) {
		t.Fatalf("Read should follow the unterminated update (MediaBox %v)", mb)
	}
	if n := len(d.Source().Revisions()); n != 1 {
		t.Fatalf("the update has no %%%%EOF, so the file should have one revision; got %d", n)
	}
	r := firstSignature(t, unterminated)
	if !r.Valid || r.ChangesAllowed || r.Intact() {
		t.Fatalf("the unterminated update's page change must be reported: valid=%v allowed=%v disallowed=%v", r.Valid, r.ChangesAllowed, r.DisallowedChanges)
	}
	if !anyContains(r.DisallowedChanges, "MediaBox") {
		t.Errorf("the page change should be named: %v", r.DisallowedChanges)
	}
}

// TestRedefinedObjectStreamIsAChange: in a file whose objects live in object
// streams, an update can change an object without redefining it, by giving the
// object stream that holds it a new definition. The object's own
// cross-reference entry is the same in both revisions; the analysis must still
// read it in both and see the difference.
func TestRedefinedObjectStreamIsAChange(t *testing.T) {
	cert, key := signtest.CertKey(t)
	src := readBytes(t, buildMinimalPDF())
	src.usedXRefStream = true // write object streams and a cross-reference stream
	var packed bytes.Buffer
	if err := src.Write(&packed); err != nil {
		t.Fatal(err)
	}
	var sb bytes.Buffer
	if err := readBytes(t, packed.Bytes()).WriteSignedIncremental(&sb, cert, key); err != nil {
		t.Fatal(err)
	}
	signed := sb.Bytes()
	d := readBytes(t, signed)
	// The page tree's root: signing rewrote the page itself (its /Annots) as
	// a plain object, but left the /Pages node in its object stream.
	pagesNum := object.RefNum(d.ResolveDict(d.Trailer.Get("Root")).Get("Pages"))
	e, ok := d.Source().Entry(pagesNum)
	if !ok || !e.Compressed {
		t.Fatalf("the /Pages node should be stored in an object stream (entry %+v)", e)
	}
	if r := firstSignature(t, signed); !r.DocumentUnmodified() {
		t.Fatalf("the signature should cover its file: %+v", r)
	}

	// The container, re-encoded with /Rotate 90 on the /Pages node, which
	// the page inherits: the page turns on its side.
	ce, _ := d.Source().Entry(e.StreamObjNum)
	lx := syntax.NewLexer(d.Source().data)
	lx.SetPosition(ce.Offset)
	cobj, err := syntax.NewParserFromLexer(lx).ParseIndirectObject()
	if err != nil {
		t.Fatal(err)
	}
	data, index, first, err := parseObjStmIndex(core.Canceler{}, cobj.Value.(*object.Stream), d.lim(), d.Resolve)
	if err != nil {
		t.Fatal(err)
	}
	var head, body bytes.Buffer
	for _, ie := range index {
		p := syntax.NewParser(data)
		p.SetOffset(int64(first + ie.Offset))
		obj, err := p.ParseObject()
		if err != nil {
			t.Fatal(err)
		}
		if ie.Number == pagesNum {
			pg := obj.(*object.Dictionary).Clone()
			pg.Set("Rotate", object.Integer(90))
			obj = pg
		}
		fmt.Fprintf(&head, "%d %d ", ie.Number, body.Len())
		if err := syntax.NewSerializer(&body).WriteObject(obj); err != nil {
			t.Fatal(err)
		}
		body.WriteString("\n")
	}
	content := append(head.Bytes(), body.Bytes()...)
	var upd bytes.Buffer
	upd.Write(signed)
	objOff := upd.Len()
	fmt.Fprintf(&upd, "%d 0 obj\n<< /Type /ObjStm /N %d /First %d /Length %d >>\nstream\n", e.StreamObjNum, len(index), head.Len(), len(content))
	upd.Write(content)
	upd.WriteString("\nendstream\nendobj\n")
	// A second, fresh object number is pointed at the old container's bytes,
	// so the old container is still read — it is a harmless new object — and
	// its offset is one the file still uses. An analysis that asked only
	// "are the bytes at this offset still in use" would take the /Pages node
	// as unchanged; it must see that the container's own entry changed.
	alias := d.Source().DeclaredSize()
	xrefOff := upd.Len()
	fmt.Fprintf(&upd, "xref\n%d 1\n%010d 00000 n \r\n%d 1\n%010d 00000 n \r\ntrailer\n<< /Size %d /Root %v /Prev %d >>\nstartxref\n%d\n%%%%EOF\n",
		e.StreamObjNum, objOff, alias, ce.Offset, alias+1, d.Trailer.Get("Root"), d.Source().Sections()[0].Offset(), xrefOff)
	attacked := upd.Bytes()

	ad := readBytes(t, attacked)
	if ae, _ := ad.Source().Entry(pagesNum); ae != e {
		t.Fatalf("the /Pages node's own entry should be unchanged (%+v vs %+v)", ae, e)
	}
	if rot := ad.Objects[pagesNum].Value.(*object.Dictionary).Get("Rotate"); rot != object.Integer(90) {
		t.Fatalf("a reader should see the changed /Pages node (Rotate %v)", rot)
	}
	r := firstSignature(t, attacked)
	if !r.Valid || r.ChangesAllowed || !anyContains(r.DisallowedChanges, "Rotate") {
		t.Fatalf("the page tree changed through its object stream must be reported: valid=%v allowed=%v disallowed=%v", r.Valid, r.ChangesAllowed, r.DisallowedChanges)
	}
}

// TestWriteSignedRefusesASignedDocument pins audit 2026-09-22 C154: a full
// rewrite of a signed document moves every byte its signatures cover, so
// WriteSigned refuses one unless told to discard them; WriteSignedIncremental
// is the path that keeps them.
func TestWriteSignedRefusesASignedDocument(t *testing.T) {
	cert, key := signtest.CertKey(t)
	signed := signMinimal(t)
	var out bytes.Buffer
	if err := readBytes(t, signed).WriteSigned(&out, cert, key); !errors.Is(err, ErrAlreadySigned) {
		t.Fatalf("WriteSigned on a signed document = %v, want ErrAlreadySigned", err)
	}
	if out.Len() != 0 {
		t.Error("a refused WriteSigned wrote output")
	}
	if err := readBytes(t, signed).WriteSigned(&out, cert, key, InvalidatingExistingSignatures()); err != nil {
		t.Fatalf("with InvalidatingExistingSignatures: %v", err)
	}
	valid := 0
	for _, r := range verifySigs(t, readBytes(t, out.Bytes()), sign.VerifyOptions{}) {
		if r.Valid {
			valid++
		}
	}
	if valid != 1 {
		t.Errorf("after an explicit rewrite exactly the new signature verifies; %d do", valid)
	}
	out.Reset()
	if err := readBytes(t, signed).WriteSignedIncremental(&out, cert, key); err != nil {
		t.Fatalf("WriteSignedIncremental: %v", err)
	}
	for _, r := range verifySigs(t, readBytes(t, out.Bytes()), sign.VerifyOptions{}) {
		if !r.Valid {
			t.Errorf("an incremental signature keeps the earlier one valid: %+v", r)
		}
	}
	if _, err := signOptions([]SignOption{WithSignatureTimestamp(nil, key)}); err == nil || !strings.Contains(err.Error(), "both") {
		t.Errorf("a time-stamp option with no certificate must be refused: %v", err)
	}
}

// TestSuccessiveApprovalSignaturesAreAllIntact: in the multi-signer workflow
// each signer adds an approval signature in a new revision, and no signature
// may invalidate the ones before it. Every signature and the archival
// time-stamp after them stay intact, and the first stays a conformant PAdES
// signature.
func TestSuccessiveApprovalSignaturesAreAllIntact(t *testing.T) {
	cert, key := signtest.CertKey(t)
	tsaCert, tsaKey := signtest.TSACertKey(t)
	data := signMinimal(t)
	for i := 0; i < 2; i++ {
		var out bytes.Buffer
		if err := readBytes(t, data).WriteSignedIncremental(&out, cert, key); err != nil {
			t.Fatal(err)
		}
		data = out.Bytes()
	}
	data = archive(t, data, ValidationData{}, tsaCert, tsaKey)
	d := readBytes(t, data)
	res := verifySigs(t, d, sign.VerifyOptions{})
	if len(res) != 4 {
		t.Fatalf("got %d results, want three signatures and a time-stamp", len(res))
	}
	for _, r := range res {
		if !r.Intact() {
			t.Errorf("%s is not intact: valid=%v disallowed=%v", r.Field, r.Valid, r.DisallowedChanges)
		}
	}
	for _, p := range padesOf(t, d, sign.VerifyOptions{}) {
		if !p.Conformant || !p.ChangesAllowed {
			t.Errorf("%s: conformant=%v issues=%v", p.Field, p.Conformant, p.Issues)
		}
	}
}

// fieldMDPSigned signs a document whose form holds, besides the signature, an
// empty signature field "Witness", with a FieldMDP transform (ISO 32000-2
// Table 258) of the given action and field list on the signature.
func fieldMDPSigned(t *testing.T, action string, fields []string) []byte {
	t.Helper()
	cert, key := signtest.CertKey(t)
	d := readBytes(t, buildMinimalPDF())
	clone, _, err := withSignatureField(d)
	if err != nil {
		t.Fatal(err)
	}
	var sigNum, formNum int
	for n, iobj := range clone.Objects {
		if sd, ok := iobj.Value.(*object.Dictionary); ok {
			if sd.Get("ByteRange") != nil {
				sigNum = n
			}
			if sd.Get("SigFlags") != nil {
				formNum = n
			}
		}
	}
	var list object.Array
	for _, f := range fields {
		list = append(list, object.String{Value: []byte(f)})
	}
	clone.Objects[sigNum].Value.(*object.Dictionary).Set("Reference", object.Array{object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("SigRef")},
		object.Entry{Key: "TransformMethod", Value: object.Name("FieldMDP")},
		object.Entry{Key: "TransformParams", Value: object.NewDictionary(
			object.Entry{Key: "Type", Value: object.Name("TransformParams")},
			object.Entry{Key: "Action", Value: object.Name(action)},
			object.Entry{Key: "Fields", Value: list},
			object.Entry{Key: "V", Value: object.Name("1.2")},
		)},
	)})
	witness := clone.Add(object.NewDictionary(
		object.Entry{Key: "FT", Value: object.Name("Sig")},
		object.Entry{Key: "T", Value: object.String{Value: []byte("Witness")}},
	))
	form := clone.Objects[formNum].Value.(*object.Dictionary)
	fl, _ := form.Get("Fields").(object.Array)
	form.Set("Fields", append(fl, witness))
	var buf bytes.Buffer
	if err := clone.Write(&buf); err != nil {
		t.Fatal(err)
	}
	out, err := patchSignature(buf.Bytes(), cert, key, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// fillWitness appends an update giving the empty "Witness" field a signature
// value — signing a field the signed revision left empty.
func fillWitness(t *testing.T, data []byte) []byte {
	t.Helper()
	d := readBytes(t, data)
	num := -1
	for n, iobj := range d.Objects {
		if fd, ok := iobj.Value.(*object.Dictionary); ok {
			if s, ok := fd.Get("T").(object.String); ok && string(s.Value) == "Witness" {
				num = n
			}
		}
	}
	if num < 0 {
		t.Fatal("no Witness field")
	}
	sig := d.Add(object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("Sig")},
		object.Entry{Key: "ByteRange", Value: object.Array{object.Integer(0), object.Integer(1), object.Integer(2), object.Integer(1)}},
		object.Entry{Key: "Contents", Value: object.String{Value: []byte{0}, IsHex: true}},
	))
	fd := d.Objects[num].Value.(*object.Dictionary).Clone()
	fd.Set("V", sig)
	d.Objects[num] = &object.IndirectObject{Number: num, Value: fd}
	var out bytes.Buffer
	if err := d.WriteIncremental(&out, []int{num, sig.Number}); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// TestFieldMDPLocksFields: a signature whose FieldMDP transform locks a field
// forbids later signing of that field, and only that field.
func TestFieldMDPLocksFields(t *testing.T) {
	for _, tc := range []struct {
		action  string
		fields  []string
		allowed bool
	}{
		{"All", nil, false},
		{"Include", []string{"Witness"}, false},
		{"Include", []string{"Other"}, true},
		{"Exclude", []string{"Witness"}, true},
		{"Exclude", []string{"Other"}, false},
	} {
		t.Run(fmt.Sprintf("%s%v", tc.action, tc.fields), func(t *testing.T) {
			base := fieldMDPSigned(t, tc.action, tc.fields)
			if r := firstSignature(t, base); !r.DocumentUnmodified() {
				t.Fatalf("the signature must verify over its own file: %+v", r)
			}
			r := firstSignature(t, fillWitness(t, base))
			if r.ChangesAllowed != tc.allowed {
				t.Errorf("ChangesAllowed = %v, want %v: %v", r.ChangesAllowed, tc.allowed, r.DisallowedChanges)
			}
			if !tc.allowed && !anyContains(r.DisallowedChanges, "locked by an earlier signature's FieldMDP") {
				t.Errorf("the refusal should name the lock: %v", r.DisallowedChanges)
			}
		})
	}
}
