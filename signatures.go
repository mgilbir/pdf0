package pdf0

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"time"
)

// SignatureResult reports the outcome of verifying one signature field.
type SignatureResult struct {
	Field               string    // signature field name, if any
	SignerCommonName    string    // Subject CN of the signing certificate
	CoversWholeDocument bool      // the /ByteRange spans the whole file
	Valid               bool      // digest matches and the signature verifies
	SigningTime         time.Time // signing-time signed attribute, if present
	TrustedChain        bool      // the certificate chains to a supplied trust root
	ChainErr            error     // why the chain did not build (when roots were given)
	Err                 error     // why signature verification failed, if it did
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
// not build a trust chain (no root store): a Valid result means the document
// content is intact and was signed by the holder of the embedded certificate's
// private key.
func (d *Document) VerifySignatures(raw []byte) []SignatureResult {
	return d.VerifySignaturesWithRoots(raw, nil)
}

// VerifySignaturesWithRoots verifies every signature as VerifySignatures does and,
// when roots is non-nil, additionally builds the signer's certificate chain to one
// of those trust anchors (using the certificates embedded in the CMS as
// intermediates, at the signing time when present). The chain outcome is reported
// in TrustedChain / ChainErr and does not affect Valid, which remains a statement
// about the cryptographic integrity of the signed content.
func (d *Document) VerifySignaturesWithRoots(raw []byte, roots *x509.CertPool) []SignatureResult {
	var results []SignatureResult
	for _, iobj := range d.Objects {
		dict, ok := iobj.Value.(*Dictionary)
		if !ok || dict.Get("ByteRange") == nil || dict.Get("Contents") == nil {
			continue
		}
		if t, _ := dict.Get("Type").(Name); t != "" && t != "Sig" && t != "DocTimeStamp" {
			continue
		}
		results = append(results, d.verifyOneSignature(dict, raw, roots))
	}
	return results
}

func (d *Document) verifyOneSignature(sig *Dictionary, raw []byte, roots *x509.CertPool) SignatureResult {
	var res SignatureResult
	contents, _ := d.Resolve(sig.Get("Contents")).(String)

	segments, covers, ok := byteRangeSegments(d, sig.Get("ByteRange"), int64(len(raw)))
	res.CoversWholeDocument = covers
	if !ok {
		res.Err = errors.New("malformed /ByteRange")
		return res
	}
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

	// Optional trust-chain verification against a caller-supplied root store.
	if roots != nil {
		intermediates := x509.NewCertPool()
		for _, c := range certs {
			if c != cert {
				intermediates.AddCert(c)
			}
		}
		opts := x509.VerifyOptions{
			Roots:         roots,
			Intermediates: intermediates,
			KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
		}
		if !signingTime.IsZero() {
			opts.CurrentTime = signingTime
		}
		if _, err := cert.Verify(opts); err != nil {
			res.ChainErr = err
		} else {
			res.TrustedChain = true
		}
	}
	return res
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

	// The signature is computed over the DER of the signed attributes encoded as
	// an explicit SET OF; in the SignerInfo they carry the [0] IMPLICIT tag, so
	// re-tag the first byte to 0x31 (SET) before verifying.
	signedDER := append([]byte(nil), si.SignedAttrs.FullBytes...)
	signedDER[0] = 0x31
	sigAlgo, ok := signatureAlgorithm(cert.PublicKeyAlgorithm.String(), hashFn)
	if !ok {
		return cert, certs, signingTime, errors.New("unsupported signature algorithm")
	}
	if err := cert.CheckSignature(sigAlgo, signedDER, si.Signature); err != nil {
		return cert, certs, signingTime, fmt.Errorf("signature does not verify: %w", err)
	}
	return cert, certs, signingTime, nil
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
