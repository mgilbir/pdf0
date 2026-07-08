package pdf0

import (
	"encoding/asn1"
	"strings"
	"testing"
)

// Minimal CMS SignedData shapes for constructing synthetic signature blobs.
type tAlgID struct{ OID asn1.ObjectIdentifier }
type tECI struct{ OID asn1.ObjectIdentifier }
type tSignerInfo struct{ Version int } // stand-in SEQUENCE; only its presence is counted
type tSignedData struct {
	Version          int
	DigestAlgorithms []tAlgID `asn1:"set"`
	EncapContentInfo tECI
	Certificates     asn1.RawValue `asn1:"optional,tag:0"`
	SignerInfos      []tSignerInfo `asn1:"set"`
}
type tContentInfo struct {
	ContentType asn1.ObjectIdentifier
	Content     tSignedData `asn1:"explicit,tag:0"`
}

// buildCMS marshals a DER CMS SignedData with the requested certificate presence
// and SignerInfo count.
func buildCMS(t *testing.T, hasCert bool, nSigners int) []byte {
	t.Helper()
	signers := make([]tSignerInfo, nSigners)
	for i := range signers {
		signers[i] = tSignerInfo{Version: 1}
	}
	sd := tSignedData{
		Version:          1,
		DigestAlgorithms: []tAlgID{{OID: asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1}}},
		EncapContentInfo: tECI{OID: asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 1}},
		SignerInfos:      signers,
	}
	if hasCert {
		inner, _ := asn1.Marshal(struct{ X int }{1}) // stand-in certificate
		sd.Certificates = asn1.RawValue{Class: 2, Tag: 0, IsCompound: true, Bytes: inner}
	}
	der, err := asn1.Marshal(tContentInfo{ContentType: oidSignedData, Content: sd})
	if err != nil {
		t.Fatalf("marshal CMS: %v", err)
	}
	return der
}

// 6.4.3 t2/t3: the PKCS#7 blob must embed the signing certificate and hold
// exactly one SignerInfo.
func TestValidatePDFA_SignaturePKCS7(t *testing.T) {
	raw := make([]byte, 1000)
	mk := func(contents []byte) *Document {
		doc := NewPDFADocument(PDFA2b)
		sig := &Dictionary{}
		sig.Set("Type", Name("Sig"))
		sig.Set("SubFilter", Name("adbe.pkcs7.detached"))
		sig.Set("Contents", String{Value: contents, IsHex: true})
		sig.Set("ByteRange", Array{Integer(0), Integer(400), Integer(600), Integer(400)}) // covers 1000
		doc.Objects[20] = &IndirectObject{Number: 20, Value: sig}
		return doc
	}
	flaggedPKCS7 := func(contents []byte) bool {
		for _, e := range ValidatePDFABytes(mk(contents), PDFA2b, raw) {
			if e.Rule == "6.4.3" && strings.Contains(e.Message, "PKCS#7") {
				return true
			}
		}
		return false
	}

	if flaggedPKCS7(buildCMS(t, true, 1)) {
		t.Error("a signing certificate with one SignerInfo must pass")
	}
	if !flaggedPKCS7(buildCMS(t, false, 1)) {
		t.Error("a missing signing certificate must be flagged")
	}
	if !flaggedPKCS7(buildCMS(t, true, 2)) {
		t.Error("two SignerInfos must be flagged")
	}
	if !flaggedPKCS7(buildCMS(t, true, 0)) {
		t.Error("zero SignerInfos must be flagged")
	}
	// A non-CMS /Contents (e.g. the raw value of an adbe.x509.rsa_sha1 signature)
	// must not trip the PKCS#7 rules.
	if flaggedPKCS7([]byte{0x01, 0x02, 0x03}) {
		t.Error("non-CMS /Contents must not be flagged by the PKCS#7 rules")
	}
}
