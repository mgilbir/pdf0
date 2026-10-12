package pdf0

import (
	"bytes"
	"crypto"
	"crypto/x509"
	"slices"
	"testing"

	"github.com/mgilbir/pdf0/internal/signtest"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/sign"
)

// The shadow attack on an earlier signature (docs audit 2026-10-11 D2): an
// incremental update adds a signature field whose widget paints over the
// signed page. Adding a signature is a permitted change — it is how a second
// party signs — so the earlier signature stays intact; but only a signature
// that verifies is a signature. One that does not is an unexplained change to
// what the page shows, and one that does is reported, with every other later
// signature a reader can see, in Result.LaterVisibleSignatures.

// overlayField describes the signature field a crafted update adds.
type overlayField struct {
	name string
	// signer signs the field's value over the update; nil leaves a value that
	// does not verify: a /ByteRange and /Contents that are present and wrong.
	signer    *x509.Certificate
	signerKey crypto.Signer
	// rect, flags and appearance shape the widget: appearance false leaves it
	// without /AP.
	rect       object.Array
	flags      int
	appearance bool
	// kid puts the widget in the field's /Kids instead of merging it into
	// the field dictionary.
	kid bool
	// streamValue stores the unverifiable value as a stream whose dictionary
	// has the signature keys: not a signature dictionary the verifier reads.
	streamValue bool
}

var fullPage = object.Array{object.Integer(0), object.Integer(0), object.Integer(612), object.Integer(792)}

// addOverlay appends to data an incremental update that adds the field o
// describes to the form and its widget to the first page, with an appearance
// that blanks the page and writes over it.
func addOverlay(t *testing.T, data []byte, o overlayField) []byte {
	t.Helper()
	clone, changed, err := withSignatureField(readBytes(t, data))
	if err != nil {
		t.Fatal(err)
	}
	sigNum, fieldNum, pageNum := changed[0], changed[1], changed[2]
	field := clone.Objects[fieldNum].Value.(*object.Dictionary)
	field.Set("T", object.String{Value: []byte(o.name)})
	widget := field
	if o.kid {
		widget = &object.Dictionary{}
		for _, k := range []object.Name{"Type", "Subtype", "Rect", "F", "P"} {
			widget.Set(k, field.Get(k))
			field.Delete(k)
		}
		widget.Set("Parent", object.IndirectRef{Number: fieldNum})
		kid := clone.Add(widget)
		field.Set("Kids", object.Array{kid})
		page := clone.Objects[pageNum].Value.(*object.Dictionary)
		annots := page.Get("Annots").(object.Array)
		annots[len(annots)-1] = kid
		changed = append(changed, kid.Number)
	}
	widget.Set("Rect", o.rect)
	widget.Set("F", object.Integer(o.flags))
	if o.appearance {
		ap := object.NewStream(object.NewDictionary(
			object.Entry{Key: "Type", Value: object.Name("XObject")},
			object.Entry{Key: "Subtype", Value: object.Name("Form")},
			object.Entry{Key: "BBox", Value: fullPage},
		), []byte("1 1 1 rg 0 0 612 792 re f 0 g BT /F1 24 Tf 100 700 Td (Pay 1,000,000) Tj ET"))
		apRef := clone.Add(ap)
		widget.Set("AP", object.NewDictionary(object.Entry{Key: "N", Value: apRef}))
		changed = append(changed, apRef.Number)
	}
	if o.signer == nil {
		sig := clone.Objects[sigNum].Value.(*object.Dictionary)
		sig.Set("ByteRange", object.Array{object.Integer(0), object.Integer(1), object.Integer(2), object.Integer(3)})
		sig.Set("Contents", object.String{Value: []byte{0}, IsHex: true})
		if o.streamValue {
			clone.Objects[sigNum] = &object.IndirectObject{Number: sigNum, Value: object.NewStream(sig, nil)}
		}
	}
	out, err := clone.incrementalBytes(changed)
	if err != nil {
		t.Fatal(err)
	}
	if o.signer != nil {
		if out, err = patchSignature(out, o.signer, o.signerKey, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// signedBy signs the minimal document with cert and key.
func signedBy(t *testing.T, cert *x509.Certificate, key crypto.Signer) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := readBytes(t, buildMinimalPDF()).WriteSigned(&buf, cert, key); err != nil {
		t.Fatalf("WriteSigned: %v", err)
	}
	return buf.Bytes()
}

// resultFor returns the result for the named field.
func resultFor(t *testing.T, res []sign.Result, field string) sign.Result {
	t.Helper()
	for _, r := range res {
		if r.Field == field {
			return r
		}
	}
	t.Fatalf("no result for field %q among %d", field, len(res))
	return sign.Result{}
}

// padesFor returns the PAdES result for the named field.
func padesFor(t *testing.T, res []sign.PAdESResult, field string) sign.PAdESResult {
	t.Helper()
	for _, r := range res {
		if r.Field == field {
			return r
		}
	}
	t.Fatalf("no PAdES result for field %q among %d", field, len(res))
	return sign.PAdESResult{}
}

// TestUnverifiableOverlaySignatureIsAChange is the attack as the audit ran
// it: a "signature" field whose value does not verify, with a full-page
// appearance. It used to leave the earlier signature intact and conformant.
func TestUnverifiableOverlaySignatureIsAChange(t *testing.T) {
	cert, key := signtest.CertKey(t)
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	opts := sign.VerifyOptions{Roots: roots}
	for _, tc := range []struct {
		name             string
		kid, streamValue bool
		why              string
	}{
		{"merged widget", false, false, "does not verify"},
		{"widget in /Kids", true, false, "does not verify"},
		{"value stored as a stream", false, true, "is not a signature dictionary"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := addOverlay(t, signedBy(t, cert, key), overlayField{name: "Evil", rect: fullPage, appearance: true, kid: tc.kid, streamValue: tc.streamValue})
			d := readBytes(t, data)
			res := verifySigs(t, d, opts)
			first := resultFor(t, res, "Signature1")
			if !first.Valid {
				t.Fatalf("the earlier signature's bytes are untouched and must still verify: %v", first.Err)
			}
			if first.Intact() {
				t.Fatalf("an added signature field whose value does not verify left the earlier signature intact")
			}
			if !anyContains(first.DisallowedChanges, tc.why) {
				t.Errorf("the refusal should say the added signature %s: %v", tc.why, first.DisallowedChanges)
			}
			for _, r := range res {
				if r.Field == "Evil" && r.Valid {
					t.Fatalf("the crafted value verifies: %+v", r)
				}
			}
			if p := padesFor(t, padesOf(t, d, opts), "Signature1"); p.Conformant || p.ChangesAllowed {
				t.Errorf("PAdES: conformant=%v allowed=%v, want both false: %v", p.Conformant, p.ChangesAllowed, p.Issues)
			}
		})
	}
}

// TestSignedOverlayIsSurfaced: the attacker signs the overlay with a key of
// their own. That is indistinguishable, in the bytes, from a second party
// signing — a permitted change — so the earlier signature stays intact; but
// the later signature's visible appearance is reported on it, and the later
// signature itself is not trusted.
func TestSignedOverlayIsSurfaced(t *testing.T) {
	cert, key := signtest.CertKey(t)
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	opts := sign.VerifyOptions{Roots: roots}
	mallory, malloryKey := signtest.CA(t, "Mallory")
	for _, kid := range []bool{false, true} {
		name := "merged widget"
		if kid {
			name = "widget in /Kids"
		}
		t.Run(name, func(t *testing.T) {
			data := addOverlay(t, signedBy(t, cert, key), overlayField{name: "Evil", signer: mallory, signerKey: malloryKey, rect: fullPage, appearance: true, kid: kid})
			d := readBytes(t, data)
			res := verifySigs(t, d, opts)
			first := resultFor(t, res, "Signature1")
			if !first.Intact() {
				t.Fatalf("a verifying later signature is a permitted change: %v", first.DisallowedChanges)
			}
			if !slices.Equal(first.LaterVisibleSignatures, []string{"Evil"}) {
				t.Errorf("LaterVisibleSignatures = %q, want [Evil]", first.LaterVisibleSignatures)
			}
			evil := resultFor(t, res, "Evil")
			if !evil.DocumentUnmodified() || evil.TrustedChain {
				t.Errorf("the overlay signature: unmodified=%v trusted=%v, want true and false", evil.DocumentUnmodified(), evil.TrustedChain)
			}
			if len(evil.LaterVisibleSignatures) != 0 {
				t.Errorf("nothing was added after the overlay: %q", evil.LaterVisibleSignatures)
			}
			p := padesFor(t, padesOf(t, d, opts), "Signature1")
			if !p.Conformant || !slices.Equal(p.LaterVisibleSignatures, []string{"Evil"}) {
				t.Errorf("PAdES: conformant=%v later=%q issues=%v", p.Conformant, p.LaterVisibleSignatures, p.Issues)
			}
		})
	}
}

// TestOverlayChangedAfterItsSigningIsAChange: the attacker signs a harmless
// appearance and then, in a further update, replaces it. The later signature
// does not vouch for the appearance the reader sees, so it is not a permitted
// addition after the earlier signature either.
func TestOverlayChangedAfterItsSigningIsAChange(t *testing.T) {
	mallory, malloryKey := signtest.CA(t, "Mallory")
	data := addOverlay(t, signMinimal(t), overlayField{name: "Evil", signer: mallory, signerKey: malloryKey, rect: fullPage, appearance: true})
	d := readBytes(t, data)
	apNum := -1
	for n, iobj := range d.Objects {
		if st, ok := iobj.Value.(*object.Stream); ok && bytes.Contains(st.Data, []byte("Pay 1,000,000")) {
			apNum = n
		}
	}
	if apNum < 0 {
		t.Fatal("no appearance stream")
	}
	st := d.Objects[apNum].Value.(*object.Stream)
	d.Objects[apNum] = &object.IndirectObject{Number: apNum, Value: object.NewStream(st.Dict.Clone(), []byte("1 1 1 rg 0 0 612 792 re f 0 g BT /F1 24 Tf 100 700 Td (Pay 9,000,000) Tj ET"))}
	var out bytes.Buffer
	if err := d.WriteIncremental(&out, []int{apNum}); err != nil {
		t.Fatal(err)
	}
	res := verifySigs(t, readBytes(t, out.Bytes()), sign.VerifyOptions{})
	if evil := resultFor(t, res, "Evil"); evil.Intact() {
		t.Fatalf("the overlay's own appearance changed after it was signed: %+v", evil)
	}
	first := resultFor(t, res, "Signature1")
	if first.Intact() {
		t.Fatalf("an added signature that is not intact left the earlier signature intact")
	}
	if !anyContains(first.DisallowedChanges, "not intact") {
		t.Errorf("the refusal should say the added signature is not intact: %v", first.DisallowedChanges)
	}
}

// TestLaterVisibleSignatures: which later signatures are reported as visible.
// A second signature by the same trusted signer, with an appearance, is the
// legitimate multi-party case: every signature stays intact, and the later
// one is listed on the earlier. A widget the reader cannot see is not.
func TestLaterVisibleSignatures(t *testing.T) {
	cert, key := signtest.CertKey(t)
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	opts := sign.VerifyOptions{Roots: roots}
	zero := object.Array{object.Integer(0), object.Integer(0), object.Integer(0), object.Integer(0)}
	box := object.Array{object.Integer(400), object.Integer(50), object.Integer(560), object.Integer(110)}
	for _, tc := range []struct {
		name    string
		o       overlayField
		visible bool
	}{
		{"visible", overlayField{rect: box, flags: 4, appearance: true}, true},
		{"visible, widget in /Kids", overlayField{rect: box, flags: 4, appearance: true, kid: true}, true},
		{"zero-area rectangle", overlayField{rect: zero, flags: 4, appearance: true}, false},
		{"Hidden", overlayField{rect: box, flags: 4 | 2, appearance: true}, false},
		{"NoView", overlayField{rect: box, flags: 4 | 32, appearance: true}, false},
		{"no appearance", overlayField{rect: box, flags: 4}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.o.name, tc.o.signer, tc.o.signerKey = "Countersignature", cert, key
			d := readBytes(t, addOverlay(t, signedBy(t, cert, key), tc.o))
			res := verifySigs(t, d, opts)
			for _, r := range res {
				if !r.Intact() || !r.TrustedChain {
					t.Errorf("%s: intact=%v trusted=%v disallowed=%v", r.Field, r.Intact(), r.TrustedChain, r.DisallowedChanges)
				}
			}
			var want []string
			if tc.visible {
				want = []string{"Countersignature"}
			}
			if got := resultFor(t, res, "Signature1").LaterVisibleSignatures; !slices.Equal(got, want) {
				t.Errorf("LaterVisibleSignatures = %q, want %q", got, want)
			}
		})
	}
	// WriteSignedIncremental writes an invisible widget: the second signature
	// is intact and permitted, and not listed.
	var out bytes.Buffer
	if err := readBytes(t, signedBy(t, cert, key)).WriteSignedIncremental(&out, cert, key); err != nil {
		t.Fatal(err)
	}
	res := verifySigs(t, readBytes(t, out.Bytes()), opts)
	if first := resultFor(t, res, "Signature1"); !first.Intact() || len(first.LaterVisibleSignatures) != 0 {
		t.Errorf("after WriteSignedIncremental: intact=%v later=%q", first.Intact(), first.LaterVisibleSignatures)
	}
	if second := resultFor(t, res, "Signature2"); !second.DocumentUnmodified() {
		t.Errorf("the second signature should cover the file: %+v", second)
	}
}

// TestUnverifiableValueInAnEmptyFieldIsAChange covers the other way a
// signature is added: a value given to a signature field the signed revision
// left empty. A value that does not verify is a change like any other; a
// value that does is permitted, and when the field's widget can be seen it is
// listed.
func TestUnverifiableValueInAnEmptyFieldIsAChange(t *testing.T) {
	box := object.Array{object.Integer(400), object.Integer(50), object.Integer(560), object.Integer(110)}
	visibleWidget := func(d *Document) []object.Entry {
		ap := d.Add(object.NewStream(object.NewDictionary(
			object.Entry{Key: "Type", Value: object.Name("XObject")},
			object.Entry{Key: "Subtype", Value: object.Name("Form")},
			object.Entry{Key: "BBox", Value: object.Array{object.Integer(0), object.Integer(0), object.Integer(160), object.Integer(60)}},
		), []byte("0 g 0 0 160 60 re S")))
		return []object.Entry{
			{Key: "Type", Value: object.Name("Annot")},
			{Key: "Subtype", Value: object.Name("Widget")},
			{Key: "Rect", Value: box},
			{Key: "F", Value: object.Integer(4)},
			{Key: "AP", Value: object.NewDictionary(object.Entry{Key: "N", Value: ap})},
		}
	}
	for _, tc := range []struct {
		name    string
		widget  func(*Document) []object.Entry
		visible bool
	}{
		{"no widget", nil, false},
		{"visible widget", visibleWidget, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := fieldMDPSigned(t, "Include", []string{"Other"}, tc.widget) // Witness is not locked
			r := firstSignature(t, fillWitness(t, base, true))
			if !r.Intact() {
				t.Fatalf("signing the empty field is permitted: %v", r.DisallowedChanges)
			}
			var want []string
			if tc.visible {
				want = []string{"Witness"}
			}
			if !slices.Equal(r.LaterVisibleSignatures, want) {
				t.Errorf("LaterVisibleSignatures = %q, want %q", r.LaterVisibleSignatures, want)
			}
			r = firstSignature(t, fillWitness(t, base, false))
			if r.Intact() {
				t.Fatal("a value that does not verify in the empty field left the earlier signature intact")
			}
			if !anyContains(r.DisallowedChanges, "does not verify") {
				t.Errorf("the refusal should say the added signature does not verify: %v", r.DisallowedChanges)
			}
		})
	}
}
