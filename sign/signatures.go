package sign

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"fmt"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"math/big"
	"sort"
	"strings"
	"time"
)

// This file holds what signature verification and signing share: the
// signature dictionaries of a document and the names of the fields that hold
// them, the CMS/PKCS#7 object identifiers and attribute shapes (RFC 5652, plus
// the ESS/CAdES signed attributes PAdES relies on), and the SignedData encoder
// the signing path uses, so producing and verifying share one model of the
// structure. The verification itself is in verify.go (what a verdict means),
// cmsverify.go (the CMS), byterange.go (the signed bytes), trust.go (chains),
// revocation.go and changes.go (what changed after signing).

// CMS / PKCS#7 object identifiers (RFC 5652) and the CAdES/ESS attributes PAdES
// relies on (RFC 5035, ETSI EN 319 122).
var (
	oidData          = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 1}
	oidContentType   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 3}
	oidMessageDigest = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 4}
	oidSigningTime   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 5}
	// ESS signing-certificate (v1: SHA-1) and v2 (SHA-256+): the CAdES-BES
	// attribute binding the signer certificate into the signed attributes.
	oidSigningCertificate   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 16, 2, 12}
	oidSigningCertificateV2 = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 16, 2, 47}
	// signature-timestamp: the unsigned attribute carrying a B-T timestamp token.
	oidSignatureTimeStamp = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 16, 2, 14}
)

// essCertIDv2 is RFC 5035 ESSCertIDv2 as pdf0 writes it: the hash algorithm
// left at its DEFAULT (SHA-256, which DER requires to be omitted) and the
// optional issuerSerial omitted. Verification reads the full shape
// (essCertIDv2In), because other producers write both optional fields.
type essCertIDv2 struct {
	CertHash []byte // OCTET STRING: hash of the certificate DER
}

// signingCertificateV2 is RFC 5035 SigningCertificateV2 as pdf0 writes it (the
// policies field omitted).
type signingCertificateV2 struct {
	Certs []essCertIDv2
}

// signatureEntry is a signature dictionary together with the number of the
// indirect object holding it.
type signatureEntry struct {
	num  int
	dict *object.Dictionary
}

// documentSignatures returns the document's signature dictionaries — those
// carrying both /ByteRange and /Contents, with a /Type of /Sig, /DocTimeStamp or
// absent — ordered by object number, so every caller reports its results in the
// same deterministic order rather than in Go map order.
func documentSignatures(d core.View, includeDocTimestamps bool) []signatureEntry {
	nums := make([]int, 0, len(d.Objects))
	for num := range d.Objects {
		nums = append(nums, num)
	}
	sort.Ints(nums)
	var out []signatureEntry
	for _, num := range nums {
		iobj := d.Objects[num]
		if iobj == nil {
			continue
		}
		dict, ok := iobj.Value.(*object.Dictionary)
		if !ok || dict.Get("ByteRange") == nil || dict.Get("Contents") == nil {
			continue
		}
		switch t, _ := d.ResolveName(dict.Get("Type")); t {
		case "", "Sig":
		case "DocTimeStamp":
			if !includeDocTimestamps {
				continue
			}
		default:
			continue
		}
		out = append(out, signatureEntry{num: num, dict: dict})
	}
	return out
}

// MaxFieldTreeDepth caps the field-hierarchy walks below, so a /Kids or /Parent
// chain in an untrusted document cannot drive unbounded recursion.
const MaxFieldTreeDepth = 64

// signatureFieldNames maps the object number of each signature dictionary in
// sigs to the fully qualified name (see Result.Field) of the form field
// whose /V references it. Signatures no field points at are absent from the map.
//
// The interactive form's field tree is the authoritative source: walking it from
// the catalog's /AcroForm /Fields downwards yields each field's ancestors, hence
// its qualified name. Fields that no /AcroForm reaches (a widget attached only to
// a page, which producers do emit) are picked up by a second pass over the
// objects, reconstructing the ancestry from the field's own /Parent chain.
func signatureFieldNames(d core.View, sigs []signatureEntry) map[int]string {
	if len(sigs) == 0 {
		return nil
	}
	want := make(map[int]bool, len(sigs))
	for _, s := range sigs {
		want[s.num] = true
	}
	names := make(map[int]string, len(sigs))

	if cat := d.Catalog(); cat != nil {
		if form := d.ResolveDict(cat.Get("AcroForm")); form != nil {
			fields, _ := d.Resolve(form.Get("Fields")).(object.Array)
			seen := map[int]bool{}
			for _, f := range fields {
				collectFieldNames(d, f, "", seen, want, names, 0)
			}
		}
	}
	if len(names) == len(want) {
		return names
	}
	// Second pass, in object-number order so the outcome does not depend on map
	// iteration: any dictionary whose /V references a still-unnamed signature and
	// that carries a name of its own somewhere up its /Parent chain.
	nums := make([]int, 0, len(d.Objects))
	for num := range d.Objects {
		nums = append(nums, num)
	}
	sort.Ints(nums)
	for _, num := range nums {
		iobj := d.Objects[num]
		if iobj == nil {
			continue
		}
		fd, ok := iobj.Value.(*object.Dictionary)
		if !ok || want[num] {
			continue // not a dictionary, or the signature dictionary itself
		}
		v := fd.Get("V")
		if v == nil {
			continue
		}
		target := refObjNum(d, v)
		if !want[target] {
			continue
		}
		if _, done := names[target]; done {
			continue
		}
		names[target] = qualifiedFieldName(d, fd)
	}
	return names
}

// collectFieldNames walks one branch of the field tree, accumulating the
// qualified-name prefix, and records the name of every field whose /V references
// a wanted signature dictionary.
func collectFieldNames(d core.View, node object.Object, prefix string, seen map[int]bool, want map[int]bool, names map[int]string, depth int) {
	if depth > MaxFieldTreeDepth {
		return
	}
	if ref, ok := node.(object.IndirectRef); ok {
		if seen[ref.Number] {
			return // already visited: a cyclic or shared /Kids entry
		}
		seen[ref.Number] = true
	}
	fd := d.ResolveDict(node)
	if fd == nil {
		return
	}
	name := JoinFieldName(prefix, fieldPartialName(d, fd))
	if v := fd.Get("V"); v != nil {
		if target := refObjNum(d, v); want[target] {
			if _, done := names[target]; !done {
				names[target] = name
			}
		}
	}
	kids, _ := d.Resolve(fd.Get("Kids")).(object.Array)
	for _, k := range kids {
		collectFieldNames(d, k, name, seen, want, names, depth+1)
	}
}

// qualifiedFieldName builds a field's fully qualified name from its own /T and
// those of its ancestors, following /Parent upwards.
func qualifiedFieldName(d core.View, field *object.Dictionary) string {
	var parts []string
	seen := map[*object.Dictionary]bool{}
	for node, depth := field, 0; node != nil && depth <= MaxFieldTreeDepth; depth++ {
		if seen[node] {
			break // cyclic /Parent chain
		}
		seen[node] = true
		if part := fieldPartialName(d, node); part != "" {
			parts = append(parts, part)
		}
		node = d.ResolveDict(node.Get("Parent"))
	}
	// parts is leaf-first; the qualified name reads root-first.
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return strings.Join(parts, ".")
}

// fieldPartialName returns a field's /T decoded to UTF-8 (it is a PDF text
// string, so possibly UTF-16), or "" when it has none.
func fieldPartialName(d core.View, field *object.Dictionary) string {
	t, ok := d.Resolve(field.Get("T")).(object.String)
	if !ok {
		return ""
	}
	return core.DecodePDFTextString(t.Value)
}

// JoinFieldName appends a partial name to a qualified-name prefix. A field with
// no /T contributes nothing to the name of its descendants.
func JoinFieldName(prefix, part string) string {
	switch {
	case part == "":
		return prefix
	case prefix == "":
		return part
	}
	return prefix + "." + part
}

// refObjNum returns the number of the object an entry refers to: the referenced
// number for an indirect reference (following reference chains, as Resolve does),
// or the holding object's number for a direct dictionary. It returns -1 when the
// object has no indirect identity. Identity must be compared this way rather than
// by pointer, because /V is normally an indirect reference to the signature
// dictionary and not the dictionary itself.
func refObjNum(d core.View, o object.Object) int {
	for hops := 0; hops < 64; hops++ {
		ref, ok := o.(object.IndirectRef)
		if !ok {
			break
		}
		iobj := d.Objects[ref.Number]
		if iobj == nil {
			return ref.Number
		}
		if next, isRef := iobj.Value.(object.IndirectRef); isRef {
			o = next
			continue
		}
		return ref.Number
	}
	if dict, ok := o.(*object.Dictionary); ok {
		return d.DictObjNum(dict)
	}
	return -1
}

// signerInfo mirrors the RFC 5652 SignerInfo fields verification needs.
type signerInfo struct {
	Version         int
	SID             asn1.RawValue
	DigestAlgorithm pkixAlgorithmIdentifier
	SignedAttrs     asn1.RawValue `asn1:"optional,tag:0"`
	SignatureAlgo   pkixAlgorithmIdentifier
	Signature       []byte
	UnsignedAttrs   asn1.RawValue `asn1:"optional,tag:1"`
}

type pkixAlgorithmIdentifier struct {
	Algorithm  asn1.ObjectIdentifier
	Parameters asn1.RawValue `asn1:"optional"`
}

type attribute struct {
	Type   asn1.ObjectIdentifier
	Values asn1.RawValue `asn1:"set"`
}

// signingTimeFromAttrs extracts the signing-time signed attribute, or the zero
// time if it is absent or unparseable.
func signingTimeFromAttrs(setBytes []byte) time.Time {
	rest := setBytes
	for len(rest) > 0 {
		var a attribute
		var err error
		rest, err = asn1.Unmarshal(rest, &a)
		if err != nil {
			return time.Time{}
		}
		if a.Type.Equal(oidSigningTime) {
			var t time.Time
			if _, err := asn1.Unmarshal(a.Values.Bytes, &t); err == nil {
				return t
			}
		}
	}
	return time.Time{}
}

func parseAttributes(setBytes []byte) (map[string][]byte, error) {
	out := map[string][]byte{}
	rest := setBytes
	for len(rest) > 0 {
		var a attribute
		var err error
		rest, err = asn1.Unmarshal(rest, &a)
		if err != nil {
			return nil, fmt.Errorf("parsing signed attribute: %w", err)
		}
		// Store the raw value content (for messageDigest, the OCTET STRING bytes).
		var v asn1.RawValue
		if _, err := asn1.Unmarshal(a.Values.Bytes, &v); err == nil {
			out[a.Type.String()] = v.Bytes
		}
	}
	return out, nil
}

func hashForOID(oid asn1.ObjectIdentifier) (crypto.Hash, bool) {
	switch oid.String() {
	case "2.16.840.1.101.3.4.2.1":
		return crypto.SHA256, true
	case "2.16.840.1.101.3.4.2.2":
		return crypto.SHA384, true
	case "2.16.840.1.101.3.4.2.3":
		return crypto.SHA512, true
	case "1.3.14.3.2.26":
		return crypto.SHA1, true
	}
	return 0, false
}

func signatureAlgorithm(pubAlgo string, hash crypto.Hash) (x509.SignatureAlgorithm, bool) {
	switch pubAlgo {
	case "RSA":
		switch hash {
		case crypto.SHA256:
			return x509.SHA256WithRSA, true
		case crypto.SHA384:
			return x509.SHA384WithRSA, true
		case crypto.SHA512:
			return x509.SHA512WithRSA, true
		case crypto.SHA1:
			return x509.SHA1WithRSA, true
		}
	case "ECDSA":
		switch hash {
		case crypto.SHA256:
			return x509.ECDSAWithSHA256, true
		case crypto.SHA384:
			return x509.ECDSAWithSHA384, true
		case crypto.SHA512:
			return x509.ECDSAWithSHA512, true
		}
	}
	return 0, false
}

// --- CMS SignedData construction (used by signing) ---

// buildSignedData produces a detached CMS SignedData (adbe.pkcs7.detached form)
// over content, signed by key with cert embedded. SHA-256 with the key's
// algorithm.
func buildSignedData(cert *x509.Certificate, key crypto.Signer, content []byte) ([]byte, error) {
	return BuildSignedDataFull(cert, key, content, nil, nil)
}

// buildSignedDataFull builds a detached CMS SignedData over content. When a TSA
// certificate and key are supplied it also embeds an RFC 3161 signature time-
// stamp over the signature value as an unsigned attribute, producing a PAdES-B-T
// signature.
func BuildSignedDataFull(cert *x509.Certificate, key crypto.Signer, content []byte, tsaCert *x509.Certificate, tsaKey crypto.Signer) ([]byte, error) {
	hashFn := crypto.SHA256
	h := hashFn.New()
	h.Write(content)
	digest := h.Sum(nil)

	// Signed attributes: contentType (id-data) and messageDigest.
	ctVal, err := asn1.Marshal(oidData)
	if err != nil {
		return nil, err
	}
	mdVal, err := asn1.Marshal(digest) // OCTET STRING
	if err != nil {
		return nil, err
	}
	ctAttr, err := marshalAttribute(oidContentType, ctVal)
	if err != nil {
		return nil, err
	}
	mdAttr, err := marshalAttribute(oidMessageDigest, mdVal)
	if err != nil {
		return nil, err
	}

	// signing-certificate-v2 (CAdES-BES): bind the signer certificate into the
	// signed attributes so the signature is PAdES-B-B conformant.
	ch := hashFn.New()
	ch.Write(cert.Raw)
	scVal, err := asn1.Marshal(signingCertificateV2{Certs: []essCertIDv2{{CertHash: ch.Sum(nil)}}})
	if err != nil {
		return nil, err
	}
	scAttr, err := marshalAttribute(oidSigningCertificateV2, scVal)
	if err != nil {
		return nil, err
	}
	attrsSet := derSet([][]byte{ctAttr, mdAttr, scAttr}) // SET OF, DER-sorted

	// The signature is over the attributes encoded as SET (0x31).
	ah := hashFn.New()
	ah.Write(attrsSet)
	sig, err := key.Sign(rand.Reader, ah.Sum(nil), hashFn)
	if err != nil {
		return nil, err
	}

	// In the SignerInfo the attributes carry the [0] IMPLICIT tag (0xA0).
	signedAttrsImplicit := append([]byte(nil), attrsSet...)
	signedAttrsImplicit[0] = 0xA0

	sigAlgo, ok := sigAlgoOID(cert.PublicKeyAlgorithm.String())
	if !ok {
		return nil, errors.New("unsupported public key algorithm for signing")
	}
	// PAdES B-T: a signature time-stamp over the signature value, as an unsigned
	// attribute.
	var unsignedAttrs asn1.RawValue
	if tsaCert != nil && tsaKey != nil {
		token, err := BuildTimestampToken(sig, tsaCert, tsaKey, time.Now())
		if err != nil {
			return nil, err
		}
		tsAttr, err := marshalAttribute(oidSignatureTimeStamp, token)
		if err != nil {
			return nil, err
		}
		set := derSet([][]byte{tsAttr})
		set[0] = 0xA1 // [1] IMPLICIT for unsignedAttrs
		unsignedAttrs = asn1.RawValue{FullBytes: set}
	}

	si := signerInfoMarshal{
		Version: 1,
		SID: issuerAndSerial{
			Issuer: asn1.RawValue{FullBytes: cert.RawIssuer},
			Serial: cert.SerialNumber,
		},
		DigestAlgorithm: pkixAlgorithmIdentifier{Algorithm: asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1}},
		SignedAttrs:     asn1.RawValue{FullBytes: signedAttrsImplicit},
		SignatureAlgo:   pkixAlgorithmIdentifier{Algorithm: sigAlgo},
		Signature:       sig,
		UnsignedAttrs:   unsignedAttrs,
	}
	siDER, err := asn1.Marshal(si)
	if err != nil {
		return nil, err
	}

	sd := signedDataMarshal{
		Version:          1,
		DigestAlgorithms: asn1.RawValue{FullBytes: derSet([][]byte{mustMarshal(pkixAlgorithmIdentifier{Algorithm: asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1}})})},
		EncapContentInfo: encapContentInfo{ContentType: oidData},
		Certificates:     asn1.RawValue{Class: 2, Tag: 0, IsCompound: true, Bytes: cert.Raw},
		SignerInfos:      asn1.RawValue{FullBytes: derSet([][]byte{siDER})},
	}
	sdDER, err := asn1.Marshal(sd)
	if err != nil {
		return nil, err
	}
	return asn1.Marshal(contentInfoMarshal{ContentType: oidSignedData, Content: asn1.RawValue{Class: 2, Tag: 0, IsCompound: true, Bytes: sdDER}})
}

type signerInfoMarshal struct {
	Version         int
	SID             issuerAndSerial
	DigestAlgorithm pkixAlgorithmIdentifier
	SignedAttrs     asn1.RawValue
	SignatureAlgo   pkixAlgorithmIdentifier
	Signature       []byte
	UnsignedAttrs   asn1.RawValue `asn1:"optional,tag:1"`
}

type issuerAndSerial struct {
	Issuer asn1.RawValue
	Serial *big.Int
}

type encapContentInfo struct {
	ContentType asn1.ObjectIdentifier
}

type signedDataMarshal struct {
	Version          int
	DigestAlgorithms asn1.RawValue
	EncapContentInfo encapContentInfo
	Certificates     asn1.RawValue
	SignerInfos      asn1.RawValue
}

type contentInfoMarshal struct {
	ContentType asn1.ObjectIdentifier
	Content     asn1.RawValue
}

func marshalAttribute(oid asn1.ObjectIdentifier, value []byte) ([]byte, error) {
	return asn1.Marshal(struct {
		Type   asn1.ObjectIdentifier
		Values asn1.RawValue
	}{Type: oid, Values: asn1.RawValue{FullBytes: derSet([][]byte{value})}})
}

func sigAlgoOID(pubAlgo string) (asn1.ObjectIdentifier, bool) {
	switch pubAlgo {
	case "RSA":
		return asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 1}, true
	case "ECDSA":
		return asn1.ObjectIdentifier{1, 2, 840, 10045, 2, 1}, true
	}
	return nil, false
}

// derSet DER-encodes a SET OF from element encodings, sorted as DER requires.
func derSet(elems [][]byte) []byte {
	sorted := make([][]byte, len(elems))
	copy(sorted, elems)
	sort.Slice(sorted, func(i, j int) bool { return bytes.Compare(sorted[i], sorted[j]) < 0 })
	var body []byte
	for _, e := range sorted {
		body = append(body, e...)
	}
	out, _ := asn1.Marshal(asn1.RawValue{Class: 0, Tag: asn1.TagSet, IsCompound: true, Bytes: body})
	return out
}

func mustMarshal(v interface{}) []byte {
	b, _ := asn1.Marshal(v)
	return b
}

// signerCertificate returns the embedded certificate identified by a SignerInfo
// SID (issuerAndSerialNumber), or the sole certificate as a fallback.
func signerCertificate(certs []*x509.Certificate, sid asn1.RawValue) *x509.Certificate {
	var ias struct {
		Issuer asn1.RawValue
		Serial *big.Int
	}
	if _, err := asn1.Unmarshal(sid.FullBytes, &ias); err == nil && ias.Serial != nil {
		for _, c := range certs {
			if c.SerialNumber.Cmp(ias.Serial) == 0 && bytes.Equal(c.RawIssuer, ias.Issuer.FullBytes) {
				return c
			}
		}
	}
	if len(certs) == 1 {
		return certs[0]
	}
	return nil
}
