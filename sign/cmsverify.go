package sign

import (
	"bytes"
	"crypto"
	"crypto/rsa"
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"fmt"
	"time"
)

// This file verifies a CMS SignedData (RFC 5652) — a document signature's
// /Contents or an RFC 3161 time-stamp token — against the digest of the
// content it signs. The content is supplied as a digest function rather than
// as bytes: a document signature signs a range of the file, which is hashed
// where it lies (byterange.go), never copied out.
//
// Everything decoded here is attacker-controlled DER from an untrusted file,
// so each step fails closed rather than trusting a field.

// contentDigest returns the digest, under h, of the content a signature signs.
type contentDigest func(h crypto.Hash) ([]byte, error)

// digestOf is the contentDigest of bytes held in memory.
func digestOf(content []byte) contentDigest {
	return func(h crypto.Hash) ([]byte, error) {
		if !h.Available() {
			return nil, fmt.Errorf("hash %v is not available", h)
		}
		hh := h.New()
		hh.Write(content)
		return hh.Sum(nil), nil
	}
}

// signedData is a parsed CMS SignedData with exactly one SignerInfo.
type signedData struct {
	eContentType asn1.ObjectIdentifier
	eContent     []byte // the encapsulated content; nil for a detached signature
	hasEContent  bool
	certs        []*x509.Certificate
	cert         *x509.Certificate // the signer, identified by the SignerInfo's sid
	si           signerInfo
}

// parseSignedData parses der as a ContentInfo holding a SignedData. Bytes after
// the DER are ignored: a signature's /Contents is a window reserved before the
// signature existed, zero-filled past its end.
func parseSignedData(der []byte) (*signedData, error) {
	var ci struct {
		ContentType asn1.ObjectIdentifier
		Content     asn1.RawValue `asn1:"explicit,optional,tag:0"`
	}
	if _, err := asn1.Unmarshal(der, &ci); err != nil || !ci.ContentType.Equal(oidSignedData) {
		return nil, errors.New("not a CMS SignedData")
	}
	var sd struct {
		Version          int
		DigestAlgorithms asn1.RawValue
		EncapContentInfo asn1.RawValue
		Certificates     asn1.RawValue   `asn1:"optional,tag:0"`
		CRLs             asn1.RawValue   `asn1:"optional,tag:1"`
		SignerInfos      []asn1.RawValue `asn1:"set"`
	}
	if _, err := asn1.Unmarshal(ci.Content.Bytes, &sd); err != nil {
		return nil, fmt.Errorf("parsing SignedData: %w", err)
	}
	var eci struct {
		ContentType asn1.ObjectIdentifier
		Content     asn1.RawValue `asn1:"optional,explicit,tag:0"`
	}
	if _, err := asn1.Unmarshal(sd.EncapContentInfo.FullBytes, &eci); err != nil {
		return nil, fmt.Errorf("parsing EncapContentInfo: %w", err)
	}
	out := &signedData{eContentType: eci.ContentType}
	if len(eci.Content.FullBytes) > 0 {
		// eContent is an OCTET STRING under the [0] EXPLICIT tag. (For a
		// RawValue field encoding/asn1 keeps the tagged element whole, so the
		// OCTET STRING is its content.)
		inner := eci.Content
		if inner.Class == asn1.ClassContextSpecific && inner.Tag == 0 {
			if _, err := asn1.Unmarshal(inner.Bytes, &inner); err != nil {
				return nil, fmt.Errorf("parsing eContent: %w", err)
			}
		}
		if inner.Class != asn1.ClassUniversal || inner.Tag != asn1.TagOctetString || inner.IsCompound {
			return nil, errors.New("eContent is not a primitive OCTET STRING")
		}
		out.eContent, out.hasEContent = inner.Bytes, true
	}
	if len(sd.SignerInfos) != 1 {
		return nil, fmt.Errorf("expected exactly one SignerInfo, got %d", len(sd.SignerInfos))
	}
	if _, err := asn1.Unmarshal(sd.SignerInfos[0].FullBytes, &out.si); err != nil {
		return nil, fmt.Errorf("parsing SignerInfo: %w", err)
	}
	certs, err := x509.ParseCertificates(sd.Certificates.Bytes)
	if err != nil || len(certs) == 0 {
		return nil, errors.New("no signing certificate")
	}
	out.certs = certs
	out.cert = signerCertificate(certs, out.si.SID)
	if out.cert == nil {
		return out, errors.New("signer certificate not found among the embedded certificates")
	}
	return out, nil
}

// digestHash returns the SignerInfo's digest algorithm, refusing the
// collision-broken ones (audit 2026-07-26 C36).
func (sd *signedData) digestHash() (crypto.Hash, error) {
	h, ok := hashForOID(sd.si.DigestAlgorithm.Algorithm)
	if !ok {
		return 0, errors.New("unsupported digest algorithm")
	}
	if h == crypto.SHA1 || h == crypto.MD5 {
		return 0, errors.New("weak signature digest algorithm (SHA-1/MD5) is not accepted")
	}
	return h, nil
}

// verify checks the SignerInfo against the content digest returns: the
// message-digest attribute equals the content's digest, the content-type
// attribute equals the eContentType (RFC 5652 11.1), a CAdES/ESS
// signing-certificate attribute, when present, binds this signer certificate,
// and the signature over the signed attributes verifies under the signer's key
// with the algorithm and parameters the SignerInfo declares. It returns the
// signing-time attribute, which the signer asserts and nothing verifies.
func (sd *signedData) verify(digest contentDigest) (signingTime time.Time, err error) {
	hashFn, err := sd.digestHash()
	if err != nil {
		return signingTime, err
	}
	if len(sd.si.SignedAttrs.Bytes) == 0 {
		return signingTime, errors.New("signature without signed attributes is not supported")
	}
	attrs, err := parseAttributes(sd.si.SignedAttrs.Bytes)
	if err != nil {
		return signingTime, err
	}
	signingTime = signingTimeFromAttrs(sd.si.SignedAttrs.Bytes)
	want, err := digest(hashFn)
	if err != nil {
		return signingTime, err
	}
	md, ok := attrs[oidMessageDigest.String()]
	if !ok || !bytes.Equal(md, want) {
		return signingTime, errors.New("document digest does not match the signature (content was modified)")
	}
	if !signedContentTypeIs(sd.si.SignedAttrs.Bytes, sd.eContentType) {
		return signingTime, errors.New("signed content-type attribute is missing or does not match the eContentType")
	}
	if err := checkESSCertBinding(sd.si.SignedAttrs.Bytes, sd.cert); err != nil {
		return signingTime, err
	}
	// The signature is computed over the DER of the signed attributes encoded as
	// an explicit SET OF; in the SignerInfo they carry the [0] IMPLICIT tag, so
	// re-tag the first byte to 0x31 (SET) before verifying.
	signedDER := append([]byte(nil), sd.si.SignedAttrs.FullBytes...)
	signedDER[0] = 0x31
	if err := checkSignerSignature(sd.cert, sd.si.SignatureAlgo, hashFn, signedDER, sd.si.Signature); err != nil {
		return signingTime, fmt.Errorf("signature does not verify: %w", err)
	}
	return signingTime, nil
}

// VerifyCMS verifies a CMS SignedData blob over content: for a detached
// signature, content is what was signed; for one that encapsulates its
// content, content must equal it. It returns the signer certificate, every
// certificate the blob carried (for chain building) and the claimed signing
// time. A nil error means only that the signature is cryptographically sound;
// it establishes no trust (see VerifyOptions).
func VerifyCMS(der, content []byte) (cert *x509.Certificate, certs []*x509.Certificate, signingTime time.Time, err error) {
	sd, err := parseSignedData(der)
	if sd == nil {
		return nil, nil, signingTime, err
	}
	if err != nil {
		return nil, sd.certs, signingTime, err
	}
	if sd.hasEContent && !bytes.Equal(sd.eContent, content) {
		return sd.cert, sd.certs, signingTime, errors.New("the content does not match the signature's encapsulated content")
	}
	signingTime, err = sd.verify(digestOf(content))
	return sd.cert, sd.certs, signingTime, err
}

// oidRSAPSS is the RSASSA-PSS signature algorithm identifier (RFC 4055), and
// oidMGF1 its mask generation function.
var (
	oidRSAPSS = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 10}
	oidMGF1   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 8}
)

// rsassaPSSParams is RSASSA-PSS-params (RFC 4055 3.1). Every field has a
// DEFAULT: SHA-1, MGF1 with SHA-1, a 20-byte salt, trailer field 1.
type rsassaPSSParams struct {
	HashAlgorithm    pkixAlgorithmIdentifier `asn1:"optional,explicit,tag:0"`
	MaskGenAlgorithm pkixAlgorithmIdentifier `asn1:"optional,explicit,tag:1"`
	SaltLength       int                     `asn1:"optional,explicit,tag:2,default:20"`
	TrailerField     int                     `asn1:"optional,explicit,tag:3,default:1"`
}

// checkSignerSignature verifies sig over signed with the signer certificate's
// key, under the signature algorithm the SignerInfo declares. digestHash is
// the SignerInfo's digest algorithm, which RFC 5652 5.4 makes the hash of the
// signed attributes for every algorithm that does not name its own.
func checkSignerSignature(cert *x509.Certificate, alg pkixAlgorithmIdentifier, digestHash crypto.Hash, signed, sig []byte) error {
	if alg.Algorithm.Equal(oidRSAPSS) {
		return checkPSS(cert, alg.Parameters, signed, sig)
	}
	sigAlgo, ok := signatureAlgorithm(cert.PublicKeyAlgorithm.String(), digestHash)
	if !ok {
		return errors.New("unsupported signature algorithm")
	}
	return cert.CheckSignature(sigAlgo, signed, sig)
}

// checkPSS verifies an RSASSA-PSS signature with the parameters the
// AlgorithmIdentifier carries (RFC 4055): its hash, its MGF1 hash and its salt
// length. Mapping PSS onto x509's fixed PSS algorithms assumed a salt as long
// as the hash, so a conforming signature with any other salt length failed to
// verify (audit 2026-09-22 C154).
func checkPSS(cert *x509.Certificate, params asn1.RawValue, signed, sig []byte) error {
	var p rsassaPSSParams
	p.SaltLength, p.TrailerField = 20, 1
	if len(params.FullBytes) > 0 && !(params.Tag == asn1.TagNull && params.Class == asn1.ClassUniversal) {
		if _, err := asn1.Unmarshal(params.FullBytes, &p); err != nil {
			return fmt.Errorf("parsing RSASSA-PSS parameters: %w", err)
		}
	}
	h := crypto.SHA1
	if len(p.HashAlgorithm.Algorithm) > 0 {
		var ok bool
		if h, ok = hashForOID(p.HashAlgorithm.Algorithm); !ok {
			return errors.New("unsupported RSASSA-PSS hash algorithm")
		}
	}
	if h == crypto.SHA1 || h == crypto.MD5 {
		return errors.New("weak RSASSA-PSS hash algorithm (SHA-1/MD5) is not accepted")
	}
	mgfHash := crypto.SHA1
	if len(p.MaskGenAlgorithm.Algorithm) > 0 {
		if !p.MaskGenAlgorithm.Algorithm.Equal(oidMGF1) {
			return errors.New("unsupported RSASSA-PSS mask generation function")
		}
		var mh pkixAlgorithmIdentifier
		if _, err := asn1.Unmarshal(p.MaskGenAlgorithm.Parameters.FullBytes, &mh); err != nil {
			return fmt.Errorf("parsing the RSASSA-PSS MGF1 hash: %w", err)
		}
		var ok bool
		if mgfHash, ok = hashForOID(mh.Algorithm); !ok {
			return errors.New("unsupported RSASSA-PSS MGF1 hash algorithm")
		}
	}
	// Go's PSS verifier uses the message hash for MGF1. A signature whose MGF1
	// hash differs is legal but cannot be checked here, and is refused rather
	// than checked under the wrong function.
	if mgfHash != h {
		return fmt.Errorf("RSASSA-PSS with an MGF1 hash (%v) other than the message hash (%v) is not supported", mgfHash, h)
	}
	if p.TrailerField != 1 {
		return fmt.Errorf("RSASSA-PSS trailer field %d is not the only defined value, 1", p.TrailerField)
	}
	if p.SaltLength < 0 {
		return errors.New("negative RSASSA-PSS salt length")
	}
	pub, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok {
		return errors.New("an RSASSA-PSS signature needs an RSA key")
	}
	hh := h.New()
	hh.Write(signed)
	// SaltLength 0 is rsa.PSSSaltLengthAuto, which accepts any salt length:
	// Go cannot demand an empty salt. The signature is still verified under
	// the key and hash; only the salt length goes unchecked in that one case.
	return rsa.VerifyPSS(pub, h, hh.Sum(nil), sig, &rsa.PSSOptions{SaltLength: p.SaltLength, Hash: h})
}

// essCertIDv2In is RFC 5035 ESSCertIDv2 in full: a hash algorithm that
// DEFAULTs to SHA-256, the certificate hash, and an optional issuerSerial.
// Reading only the hash made every attribute that names its algorithm — as
// CAdES producers and RFC 5816 time-stamp authorities using SHA-384 or
// SHA-512 do — fail to parse, and assumed SHA-256 for the rest (audit
// 2026-09-22 C57).
type essCertIDv2In struct {
	HashAlgorithm pkixAlgorithmIdentifier `asn1:"optional"`
	CertHash      []byte
	IssuerSerial  asn1.RawValue `asn1:"optional"`
}

type signingCertificateV2In struct {
	Certs    []essCertIDv2In
	Policies asn1.RawValue `asn1:"optional"`
}

// essCertIDIn and signingCertificateIn are RFC 5035 ESSCertID and
// SigningCertificate (v1, SHA-1 hashes).
type essCertIDIn struct {
	CertHash     []byte
	IssuerSerial asn1.RawValue `asn1:"optional"`
}

type signingCertificateIn struct {
	Certs    []essCertIDIn
	Policies asn1.RawValue `asn1:"optional"`
}

// checkESSCertBinding validates the ESS signing-certificate attribute, if
// present, against cert: the first ESSCertID — which RFC 5035 5.4 makes the
// signer's — must carry the hash of cert under the algorithm it names. Absence
// is permitted here (requiring it is a PAdES-baseline policy, checked in
// pades.go); a present but mismatched or unreadable attribute is a hard
// failure.
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
			var sc signingCertificateV2In
			if _, err := asn1.Unmarshal(a.Values.Bytes, &sc); err != nil || len(sc.Certs) == 0 {
				return errors.New("malformed signing-certificate-v2 attribute")
			}
			id := sc.Certs[0]
			h := crypto.SHA256
			if len(id.HashAlgorithm.Algorithm) > 0 {
				var ok bool
				if h, ok = hashForOID(id.HashAlgorithm.Algorithm); !ok {
					return errors.New("signing-certificate-v2 names an unsupported hash algorithm")
				}
			}
			if !bytes.Equal(id.CertHash, hashOf(h, cert.Raw)) {
				return errors.New("signing-certificate-v2 does not match the signer certificate")
			}
			return nil
		case a.Type.Equal(oidSigningCertificate):
			var sc signingCertificateIn
			if _, err := asn1.Unmarshal(a.Values.Bytes, &sc); err != nil || len(sc.Certs) == 0 {
				return errors.New("malformed signing-certificate attribute")
			}
			if !bytes.Equal(sc.Certs[0].CertHash, hashOf(crypto.SHA1, cert.Raw)) {
				return errors.New("signing-certificate does not match the signer certificate")
			}
			return nil
		}
	}
	return nil
}

// signedContentTypeIs reports whether the signed attributes carry a
// content-type attribute equal to want.
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
