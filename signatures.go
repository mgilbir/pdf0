package pdf0

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"
)

// This file verifies PDF digital signatures (ISO 32000-2 §12.8): it assembles
// the bytes named by the signature's /ByteRange, checks them against the
// CMS/PKCS#7 SignedData in /Contents (RFC 5652, plus the ESS/CAdES signed
// attributes PAdES relies on), and verifies the signature with the embedded
// certificate. It also holds the SignedData encoder the signing path uses, so
// producing and verifying share one model of the structure.
//
// Two things must stay front of mind. Everything decoded here is
// attacker-controlled DER from an untrusted file, so each step fails closed
// rather than trusting a field. And a cryptographically valid signature is not
// the same claim as an unmodified document: the digest says nothing about bytes
// outside the signed range, so coverage is established separately.

// SignatureResult reports the outcome of verifying one signature field.
//
// Valid and CoversWholeDocument are independent and must both be consulted:
// Valid says the bytes inside the signed /ByteRange are intact and were signed
// by the embedded certificate's key, but it says nothing about bytes OUTSIDE
// that range. A signed document can be modified after signing by an incremental
// update — the original signed range stays intact (Valid == true) while the
// rendered content changes (CoversWholeDocument == false). Use DocumentUnmodified
// for the combined "signed and nothing was changed" verdict.
type SignatureResult struct {
	// Field is the FULLY QUALIFIED name of the signature field whose /V
	// references this signature dictionary: the field's own /T partial name
	// prefixed by the /T of every ancestor field, joined with "." (ISO 32000-2
	// §12.7.4.2). The qualified name is what identifies a field uniquely in a
	// document — a partial name is only unique among its siblings — so it is
	// what a caller can display, log, or look the field up by. For the common
	// flat form (a top-level field, as pdf0's own signing produces) it is just
	// the partial name, e.g. "Signature1".
	//
	// It is empty when nothing names the signature: a bare signature dictionary
	// that no field's /V points at, or a field chain in which neither the field
	// nor any of its ancestors carries a /T.
	Field               string
	SignerCommonName    string         // Subject CN of the signing certificate
	CoversWholeDocument bool           // the /ByteRange covers the whole file except the /Contents window
	Valid               bool           // the signed bytes are intact and the signature verifies
	SigningTime         time.Time      // signing-time signed attribute, if present (self-asserted, untrusted)
	TrustedChain        bool           // the certificate chains to a supplied trust root
	ChainErr            error          // why the chain did not build (when roots were given)
	Revocation          RevocationInfo // revocation status from the document's DSS material
	Err                 error          // why signature verification failed, if it did
}

// DocumentUnmodified reports the safe combined verdict: the signature
// cryptographically verifies AND it covers the whole document, so nothing was
// changed after signing. Callers that read only Valid accept a document whose
// content was altered by a post-signing incremental update; prefer this.
func (r SignatureResult) DocumentUnmodified() bool {
	return r.Valid && r.CoversWholeDocument
}

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

// essCertIDv2 is RFC 5035 ESSCertIDv2 with the default (SHA-256) hash algorithm
// omitted: just the certificate hash. issuerSerial is optional and omitted.
type essCertIDv2 struct {
	CertHash []byte // OCTET STRING: hash of the certificate DER
}

// signingCertificateV2 is RFC 5035 SigningCertificateV2 (the policies field
// omitted).
type signingCertificateV2 struct {
	Certs []essCertIDv2
}

// VerifySignatures verifies every signature in the document against the original
// file bytes. For each it recomputes the digest over the signed /ByteRange,
// checks it against the signature's messageDigest attribute, and verifies the
// signature over the signed attributes with the embedded certificate. It does
// not build a trust chain (no root store): a Valid result means the bytes inside
// the signed /ByteRange are intact and were signed by the holder of the embedded
// certificate's private key. It does NOT by itself mean the document was not
// modified after signing — an incremental update can change the rendered content
// while leaving the signed range intact. Combine Valid with CoversWholeDocument
// (see SignatureResult.DocumentUnmodified).
func (d *Document) VerifySignatures(raw []byte) []SignatureResult {
	return d.VerifySignaturesWithRoots(raw, nil)
}

// VerifySignaturesWithRoots verifies every signature as VerifySignatures does and,
// when roots is non-nil, additionally builds the signer's certificate chain to one
// of those trust anchors (using the certificates embedded in the CMS as
// intermediates), validating at the current time. The chain outcome is reported
// in TrustedChain / ChainErr and does not affect Valid, which remains a statement
// about the cryptographic integrity of the signed content.
//
// Results are ordered by the object number of the signature dictionary, which is
// stable across runs (the objects are held in a map, whose iteration order is
// not) and meaningful: in a document signed by successive incremental updates the
// later signature is the later object.
func (d *Document) VerifySignaturesWithRoots(raw []byte, roots *x509.CertPool) []SignatureResult {
	sigs := documentSignatures(d, true)
	names := signatureFieldNames(d, sigs)
	var results []SignatureResult
	for _, s := range sigs {
		res := verifyOneSignature(d, s.dict, raw, roots)
		res.Field = names[s.num]
		results = append(results, res)
	}
	return results
}

// signatureEntry is a signature dictionary together with the number of the
// indirect object holding it.
type signatureEntry struct {
	num  int
	dict *Dictionary
}

// documentSignatures returns the document's signature dictionaries — those
// carrying both /ByteRange and /Contents, with a /Type of /Sig, /DocTimeStamp or
// absent — ordered by object number, so every caller reports its results in the
// same deterministic order rather than in Go map order.
func documentSignatures(d *Document, includeDocTimestamps bool) []signatureEntry {
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
		dict, ok := iobj.Value.(*Dictionary)
		if !ok || dict.Get("ByteRange") == nil || dict.Get("Contents") == nil {
			continue
		}
		switch t, _ := dict.Get("Type").(Name); t {
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

// maxFieldTreeDepth caps the field-hierarchy walks below, so a /Kids or /Parent
// chain in an untrusted document cannot drive unbounded recursion.
const maxFieldTreeDepth = 64

// signatureFieldNames maps the object number of each signature dictionary in
// sigs to the fully qualified name (see SignatureResult.Field) of the form field
// whose /V references it. Signatures no field points at are absent from the map.
//
// The interactive form's field tree is the authoritative source: walking it from
// the catalog's /AcroForm /Fields downwards yields each field's ancestors, hence
// its qualified name. Fields that no /AcroForm reaches (a widget attached only to
// a page, which producers do emit) are picked up by a second pass over the
// objects, reconstructing the ancestry from the field's own /Parent chain.
func signatureFieldNames(d *Document, sigs []signatureEntry) map[int]string {
	if len(sigs) == 0 {
		return nil
	}
	want := make(map[int]bool, len(sigs))
	for _, s := range sigs {
		want[s.num] = true
	}
	names := make(map[int]string, len(sigs))

	if cat := getCatalog(d); cat != nil {
		if form := d.ResolveDict(cat.Get("AcroForm")); form != nil {
			fields, _ := d.Resolve(form.Get("Fields")).(Array)
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
		fd, ok := iobj.Value.(*Dictionary)
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
func collectFieldNames(d *Document, node Object, prefix string, seen map[int]bool, want map[int]bool, names map[int]string, depth int) {
	if depth > maxFieldTreeDepth {
		return
	}
	if ref, ok := node.(IndirectRef); ok {
		if seen[ref.Number] {
			return // already visited: a cyclic or shared /Kids entry
		}
		seen[ref.Number] = true
	}
	fd := d.ResolveDict(node)
	if fd == nil {
		return
	}
	name := joinFieldName(prefix, fieldPartialName(d, fd))
	if v := fd.Get("V"); v != nil {
		if target := refObjNum(d, v); want[target] {
			if _, done := names[target]; !done {
				names[target] = name
			}
		}
	}
	kids, _ := d.Resolve(fd.Get("Kids")).(Array)
	for _, k := range kids {
		collectFieldNames(d, k, name, seen, want, names, depth+1)
	}
}

// qualifiedFieldName builds a field's fully qualified name from its own /T and
// those of its ancestors, following /Parent upwards.
func qualifiedFieldName(d *Document, field *Dictionary) string {
	var parts []string
	seen := map[*Dictionary]bool{}
	for node, depth := field, 0; node != nil && depth <= maxFieldTreeDepth; depth++ {
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
func fieldPartialName(d *Document, field *Dictionary) string {
	t, ok := d.Resolve(field.Get("T")).(String)
	if !ok {
		return ""
	}
	return decodePDFTextString(t.Value)
}

// joinFieldName appends a partial name to a qualified-name prefix. A field with
// no /T contributes nothing to the name of its descendants.
func joinFieldName(prefix, part string) string {
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
func refObjNum(d *Document, o Object) int {
	for hops := 0; hops < 64; hops++ {
		ref, ok := o.(IndirectRef)
		if !ok {
			break
		}
		iobj := d.Objects[ref.Number]
		if iobj == nil {
			return ref.Number
		}
		if next, isRef := iobj.Value.(IndirectRef); isRef {
			o = next
			continue
		}
		return ref.Number
	}
	if dict, ok := o.(*Dictionary); ok {
		return d.view().DictObjNum(dict)
	}
	return -1
}

func verifyOneSignature(d *Document, sig *Dictionary, raw []byte, roots *x509.CertPool) SignatureResult {
	var res SignatureResult
	contents, _ := d.Resolve(sig.Get("Contents")).(String)

	segments, covers, ok := byteRangeSegments(d, sig.Get("ByteRange"), int64(len(raw)))
	if !ok {
		res.Err = errors.New("malformed /ByteRange")
		return res
	}
	// "Covers the whole document" requires more than the segments reaching the
	// end of the file: there must be exactly two segments, the first starting at
	// offset 0, and the single gap between them must be exactly the signature's
	// /Contents window (<…hex…>). Otherwise a crafted multi-segment ByteRange, or
	// a gap that does not coincide with /Contents, could leave arbitrary file
	// bytes unsigned while still reaching the end (audit C12).
	res.CoversWholeDocument = covers && contentsGapIsSignature(raw, segments, contents.Value)
	signed := make([]byte, 0, len(raw))
	for _, s := range segments {
		if s[0] < 0 || s[1] < 0 || s[0]+s[1] > int64(len(raw)) {
			res.Err = errors.New("/ByteRange segment out of bounds")
			return res
		}
		signed = append(signed, raw[s[0]:s[0]+s[1]]...)
	}

	cert, certs, signingTime, err := verifyCMS(contents.Value, signed)
	if cert != nil {
		res.SignerCommonName = cert.Subject.CommonName
	}
	res.SigningTime = signingTime
	if err != nil {
		res.Err = err
		return res
	}
	res.Valid = true

	// Revocation status from the document's own long-term validation material
	// (DSS). The issuer certificate is sought in the CMS and the DSS /Certs.
	if issuer := issuerOf(cert, append(certs, d.DSSCerts()...)); issuer != nil {
		crls, ocsps := d.DSSRevocationMaterial()
		if len(crls) > 0 || len(ocsps) > 0 {
			res.Revocation = CheckCertRevocation(cert, issuer, crls, ocsps)
		}
	}

	// Optional trust-chain verification against a caller-supplied root store.
	if roots != nil {
		if err := chainTrusted(cert, certs, roots); err != nil {
			res.ChainErr = err
		} else {
			res.TrustedChain = true
		}
	}
	return res
}

// chainTrusted builds cert's chain to one of the trust anchors in roots, using
// the other embedded certs as intermediates, and returns nil if it verifies.
//
// It validates at the current wall-clock time (VerifyOptions.CurrentTime left
// zero). The signer's signing-time attribute is deliberately NOT used as the
// reference time: it is signed only by the (possibly adversarial) signer, so a
// holder of an expired or since-revoked certificate could backdate it into the
// certificate's old validity window and forge a trusted chain (audit C4). A
// trustworthy signing time comes only from a verified timestamp, which the PAdES
// B-T/B-LTA path establishes separately.
func chainTrusted(cert *x509.Certificate, certs []*x509.Certificate, roots *x509.CertPool) error {
	intermediates := x509.NewCertPool()
	for _, c := range certs {
		if c != cert {
			intermediates.AddCert(c)
		}
	}
	_, err := cert.Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediates,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	})
	return err
}

// byteRangeSegments returns the [start,length] pairs of a /ByteRange and whether
// they reach the end of the file.
func byteRangeSegments(d *Document, obj Object, fileLen int64) (segs [][2]int64, covers, ok bool) {
	arr, isArr := d.Resolve(obj).(Array)
	if !isArr || len(arr) == 0 || len(arr)%2 != 0 {
		return nil, false, false
	}
	vals := make([]int64, len(arr))
	for i, e := range arr {
		n, isInt := d.Resolve(e).(Integer)
		if !isInt {
			return nil, false, false
		}
		vals[i] = int64(n)
	}
	var end int64
	for i := 0; i < len(vals); i += 2 {
		segs = append(segs, [2]int64{vals[i], vals[i+1]})
		if s := vals[i] + vals[i+1]; s > end {
			end = s
		}
	}
	return segs, end >= fileLen, true
}

// contentsGapIsSignature reports whether the /ByteRange describes the canonical
// two-segment signing layout: the first segment starts at offset 0, the second
// ends at the end of the file, and the single gap between them is exactly the
// signature's /Contents hex string (<…>) — i.e. only the signature value itself
// is left unsigned. This is what "covers the whole document" must mean; without
// it a ByteRange could reach the end of the file while leaving other bytes
// unsigned (audit C12).
func contentsGapIsSignature(raw []byte, segs [][2]int64, contents []byte) bool {
	if len(segs) != 2 || segs[0][0] != 0 {
		return false
	}
	gapStart := segs[0][0] + segs[0][1]
	gapEnd := segs[1][0]
	if gapStart < 0 || gapEnd <= gapStart || gapEnd > int64(len(raw)) {
		return false
	}
	if segs[1][0]+segs[1][1] != int64(len(raw)) {
		return false // the second segment must reach the very end
	}
	window := raw[gapStart:gapEnd]
	if len(window) < 2 || window[0] != '<' || window[len(window)-1] != '>' {
		return false
	}
	// Hex-decode the bytes between the angle brackets (PDF permits whitespace in
	// a hex string) and require them to equal the parsed /Contents value.
	digits := make([]byte, 0, len(window)-2)
	for _, b := range window[1 : len(window)-1] {
		switch b {
		case ' ', '\t', '\r', '\n', '\f', 0:
			continue
		}
		digits = append(digits, b)
	}
	if len(digits)%2 != 0 {
		return false
	}
	decoded := make([]byte, len(digits)/2)
	if _, err := hex.Decode(decoded, digits); err != nil {
		return false
	}
	return bytes.Equal(decoded, contents)
}

// signerInfo mirrors the RFC 5652 SignerInfo fields verification needs.
type signerInfo struct {
	Version         int
	SID             asn1.RawValue
	DigestAlgorithm pkixAlgorithmIdentifier
	SignedAttrs     asn1.RawValue `asn1:"optional,tag:0"`
	SignatureAlgo   pkixAlgorithmIdentifier
	Signature       []byte
}

type pkixAlgorithmIdentifier struct {
	Algorithm  asn1.ObjectIdentifier
	Parameters asn1.RawValue `asn1:"optional"`
}

type attribute struct {
	Type   asn1.ObjectIdentifier
	Values asn1.RawValue `asn1:"set"`
}

// verifyCMS verifies a detached CMS SignedData blob over content. It returns the
// signer certificate, every certificate embedded in the CMS (for chain building)
// and the signing-time attribute, or an error if the signature does not verify.
func verifyCMS(der, content []byte) (cert *x509.Certificate, certs []*x509.Certificate, signingTime time.Time, err error) {
	var ci struct {
		ContentType asn1.ObjectIdentifier
		Content     asn1.RawValue `asn1:"explicit,optional,tag:0"`
	}
	if _, e := asn1.Unmarshal(der, &ci); e != nil || !ci.ContentType.Equal(oidSignedData) {
		return nil, nil, signingTime, errors.New("not a CMS SignedData")
	}
	var sd struct {
		Version          int
		DigestAlgorithms asn1.RawValue
		EncapContentInfo asn1.RawValue
		Certificates     asn1.RawValue   `asn1:"optional,tag:0"`
		CRLs             asn1.RawValue   `asn1:"optional,tag:1"`
		SignerInfos      []asn1.RawValue `asn1:"set"`
	}
	if _, e := asn1.Unmarshal(ci.Content.Bytes, &sd); e != nil {
		return nil, nil, signingTime, fmt.Errorf("parsing SignedData: %w", e)
	}
	// The eContentType declared in EncapContentInfo is what the content-type
	// signed attribute must equal (id-data for a detached document signature,
	// id-ct-TSTInfo for a time-stamp token, etc.).
	var eci struct {
		ContentType asn1.ObjectIdentifier
		Content     asn1.RawValue `asn1:"optional,explicit,tag:0"`
	}
	if _, e := asn1.Unmarshal(sd.EncapContentInfo.FullBytes, &eci); e != nil {
		return nil, nil, signingTime, fmt.Errorf("parsing EncapContentInfo: %w", e)
	}
	if len(sd.SignerInfos) != 1 {
		return nil, nil, signingTime, fmt.Errorf("expected exactly one SignerInfo, got %d", len(sd.SignerInfos))
	}
	var si signerInfo
	if _, e := asn1.Unmarshal(sd.SignerInfos[0].FullBytes, &si); e != nil {
		return nil, nil, signingTime, fmt.Errorf("parsing SignerInfo: %w", e)
	}
	certs, err = x509.ParseCertificates(sd.Certificates.Bytes)
	if err != nil || len(certs) == 0 {
		return nil, nil, signingTime, errors.New("no signing certificate")
	}
	cert = signerCertificate(certs, si.SID)
	if cert == nil {
		return nil, certs, signingTime, errors.New("signer certificate not found among the embedded certificates")
	}

	hashFn, ok := hashForOID(si.DigestAlgorithm.Algorithm)
	if !ok {
		return cert, certs, signingTime, errors.New("unsupported digest algorithm")
	}
	if hashFn == crypto.SHA1 || hashFn == crypto.MD5 {
		// SHA-1 and MD5 are collision-broken; reject them as the signature digest
		// (they remain acceptable for the OCSP CertID issuer hashes, which are not
		// a signature) (audit C36).
		return cert, certs, signingTime, errors.New("weak signature digest algorithm (SHA-1/MD5) is not accepted")
	}
	h := hashFn.New()
	h.Write(content)
	contentDigest := h.Sum(nil)

	if len(si.SignedAttrs.Bytes) == 0 {
		return cert, certs, signingTime, errors.New("signature without signed attributes is not supported")
	}
	attrs, e := parseAttributes(si.SignedAttrs.Bytes)
	if e != nil {
		return cert, certs, signingTime, e
	}
	signingTime = signingTimeFromAttrs(si.SignedAttrs.Bytes)
	md, ok := attrs[oidMessageDigest.String()]
	if !ok || !bytes.Equal(md, contentDigest) {
		return cert, certs, signingTime, errors.New("document digest does not match the signature (content was modified)")
	}
	// RFC 5652 §11.1: when signed attributes are present, a content-type
	// attribute equal to the SignedData's eContentType must be among them.
	if !signedContentTypeIs(si.SignedAttrs.Bytes, eci.ContentType) {
		return cert, certs, signingTime, errors.New("signed content-type attribute is missing or does not match the eContentType")
	}
	// CAdES/ESS: when a signing-certificate attribute is present it must bind THIS
	// signer certificate (its hash), not merely exist. pdf0 advertises checking
	// the CAdES certificate binding, so enforce it rather than only noting its
	// presence (audit C14).
	if err := checkESSCertBinding(si.SignedAttrs.Bytes, cert); err != nil {
		return cert, certs, signingTime, err
	}

	// The signature is computed over the DER of the signed attributes encoded as
	// an explicit SET OF; in the SignerInfo they carry the [0] IMPLICIT tag, so
	// re-tag the first byte to 0x31 (SET) before verifying.
	signedDER := append([]byte(nil), si.SignedAttrs.FullBytes...)
	signedDER[0] = 0x31
	sigAlgo, ok := resolveSignatureAlgorithm(si.SignatureAlgo.Algorithm, cert.PublicKeyAlgorithm.String(), hashFn)
	if !ok {
		return cert, certs, signingTime, errors.New("unsupported signature algorithm")
	}
	if err := cert.CheckSignature(sigAlgo, signedDER, si.Signature); err != nil {
		return cert, certs, signingTime, fmt.Errorf("signature does not verify: %w", err)
	}
	return cert, certs, signingTime, nil
}

// oidRSAPSS is the RSASSA-PSS signature algorithm identifier (RFC 4055).
var oidRSAPSS = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 10}

// essCertID is RFC 5035 ESSCertID (v1, SHA-1 hash); issuerSerial is optional and
// omitted here.
type essCertID struct {
	CertHash []byte
}

// signingCertificateV1 is RFC 5035 SigningCertificate (the policies field
// omitted).
type signingCertificateV1 struct {
	Certs []essCertID
}

// signedContentTypeIs reports whether the signed attributes carry a content-type
// attribute equal to want.
func signedContentTypeIs(setBytes []byte, want asn1.ObjectIdentifier) bool {
	rest := setBytes
	for len(rest) > 0 {
		var a attribute
		var err error
		rest, err = asn1.Unmarshal(rest, &a)
		if err != nil {
			return false
		}
		if a.Type.Equal(oidContentType) {
			var oid asn1.ObjectIdentifier
			if _, err := asn1.Unmarshal(a.Values.Bytes, &oid); err != nil {
				return false
			}
			return oid.Equal(want)
		}
	}
	return false
}

// checkESSCertBinding validates the ESS signing-certificate attribute, if
// present, against cert: the attribute's certificate hash must equal the hash of
// cert. Absence is permitted here (requiring it is a PAdES-baseline policy); a
// present-but-mismatched attribute is a hard failure.
func checkESSCertBinding(setBytes []byte, cert *x509.Certificate) error {
	rest := setBytes
	for len(rest) > 0 {
		var a attribute
		var err error
		rest, err = asn1.Unmarshal(rest, &a)
		if err != nil {
			return nil
		}
		switch {
		case a.Type.Equal(oidSigningCertificateV2):
			var sc signingCertificateV2
			if _, err := asn1.Unmarshal(a.Values.Bytes, &sc); err != nil || len(sc.Certs) == 0 {
				return errors.New("malformed signing-certificate-v2 attribute")
			}
			sum := sha256.Sum256(cert.Raw)
			if !bytes.Equal(sc.Certs[0].CertHash, sum[:]) {
				return errors.New("signing-certificate-v2 does not match the signer certificate")
			}
			return nil
		case a.Type.Equal(oidSigningCertificate):
			var sc signingCertificateV1
			if _, err := asn1.Unmarshal(a.Values.Bytes, &sc); err != nil || len(sc.Certs) == 0 {
				return errors.New("malformed signing-certificate attribute")
			}
			sum := sha1.Sum(cert.Raw)
			if !bytes.Equal(sc.Certs[0].CertHash, sum[:]) {
				return errors.New("signing-certificate does not match the signer certificate")
			}
			return nil
		}
	}
	return nil
}

// resolveSignatureAlgorithm maps the SignerInfo's signature-algorithm OID and the
// digest to an x509.SignatureAlgorithm. RSASSA-PSS is honoured (rather than being
// forced to PKCS#1 v1.5, which made a valid PSS signature falsely fail to verify,
// audit C36); otherwise it falls back to the public-key-algorithm mapping.
func resolveSignatureAlgorithm(sigOID asn1.ObjectIdentifier, pubAlgo string, hash crypto.Hash) (x509.SignatureAlgorithm, bool) {
	if sigOID.Equal(oidRSAPSS) {
		switch hash {
		case crypto.SHA256:
			return x509.SHA256WithRSAPSS, true
		case crypto.SHA384:
			return x509.SHA384WithRSAPSS, true
		case crypto.SHA512:
			return x509.SHA512WithRSAPSS, true
		}
		return 0, false
	}
	return signatureAlgorithm(pubAlgo, hash)
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
	return buildSignedDataFull(cert, key, content, nil, nil)
}

// buildSignedDataFull builds a detached CMS SignedData over content. When a TSA
// certificate and key are supplied it also embeds an RFC 3161 signature time-
// stamp over the signature value as an unsigned attribute, producing a PAdES-B-T
// signature.
func buildSignedDataFull(cert *x509.Certificate, key crypto.Signer, content []byte, tsaCert *x509.Certificate, tsaKey crypto.Signer) ([]byte, error) {
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
		token, err := buildTimestampToken(sig, tsaCert, tsaKey, time.Now())
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
