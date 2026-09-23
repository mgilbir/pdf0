package sign

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"fmt"
	"math/big"
	"time"
)

// This file builds and verifies RFC 3161 time-stamp tokens as used by PAdES B-T
// (a signature time-stamp) and B-LTA (a document time-stamp). A token is a CMS
// SignedData whose encapsulated content is a TSTInfo binding a hash (the message
// imprint) to the time-stamp authority's asserted time.

// oidTSTInfo is id-ct-TSTInfo (RFC 3161 §2.4.2): the eContentType of a token.
var oidTSTInfo = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 16, 1, 4}

// oidTSAPolicy is a placeholder time-stamp policy identifier for tokens this
// package issues (used only when acting as a local TSA in tests or in-house).
var oidTSAPolicy = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 1}

type messageImprint struct {
	HashAlgorithm pkixAlgorithmIdentifier
	HashedMessage []byte
}

// tstInfo is RFC 3161 TSTInfo up to the fields this package reads; trailing
// optional fields (accuracy, nonce, tsa, extensions) are parsed manually so they
// can be ignored.
type tstInfo struct {
	Version        int
	Policy         asn1.ObjectIdentifier
	MessageImprint messageImprint
	SerialNumber   *big.Int
	GenTime        time.Time `asn1:"generalized"`
}

var oidSHA256 = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1}

// buildTimestampToken issues a time-stamp token over imprint (the bytes to be
// time-stamped, typically a signature value) signed by the TSA key, asserting
// genTime. It is the local-TSA path used for B-T/B-LTA signing and tests.
func BuildTimestampToken(imprint []byte, tsaCert *x509.Certificate, tsaKey crypto.Signer, genTime time.Time) ([]byte, error) {
	h := crypto.SHA256.New()
	h.Write(imprint)
	info := tstInfo{
		Version:        1,
		Policy:         oidTSAPolicy,
		MessageImprint: messageImprint{HashAlgorithm: pkixAlgorithmIdentifier{Algorithm: oidSHA256}, HashedMessage: h.Sum(nil)},
		SerialNumber:   big.NewInt(genTime.UnixNano()),
		GenTime:        genTime.UTC(),
	}
	tstDER, err := asn1.Marshal(info)
	if err != nil {
		return nil, err
	}
	return buildSignedDataEmbedded(tsaCert, tsaKey, tstDER, oidTSTInfo)
}

// buildSignedDataEmbedded builds a CMS SignedData whose eContent carries content
// (of type contentType), signed by key. Unlike buildSignedData it embeds the
// content, as a time-stamp token requires.
func buildSignedDataEmbedded(cert *x509.Certificate, key crypto.Signer, content []byte, contentType asn1.ObjectIdentifier) ([]byte, error) {
	hashFn := crypto.SHA256
	dh := hashFn.New()
	dh.Write(content)
	digest := dh.Sum(nil)

	ctVal, err := asn1.Marshal(contentType)
	if err != nil {
		return nil, err
	}
	mdVal, err := asn1.Marshal(digest)
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
	attrsSet := derSet([][]byte{ctAttr, mdAttr, scAttr})

	ah := hashFn.New()
	ah.Write(attrsSet)
	sig, err := key.Sign(rand.Reader, ah.Sum(nil), hashFn)
	if err != nil {
		return nil, err
	}
	signedAttrsImplicit := append([]byte(nil), attrsSet...)
	signedAttrsImplicit[0] = 0xA0

	sigAlgo, ok := sigAlgoOID(cert.PublicKeyAlgorithm.String())
	if !ok {
		return nil, errors.New("unsupported public key algorithm for signing")
	}
	si := signerInfoMarshal{
		Version:         1,
		SID:             issuerAndSerial{Issuer: asn1.RawValue{FullBytes: cert.RawIssuer}, Serial: cert.SerialNumber},
		DigestAlgorithm: pkixAlgorithmIdentifier{Algorithm: oidSHA256},
		SignedAttrs:     asn1.RawValue{FullBytes: signedAttrsImplicit},
		SignatureAlgo:   pkixAlgorithmIdentifier{Algorithm: sigAlgo},
		Signature:       sig,
	}
	siDER, err := asn1.Marshal(si)
	if err != nil {
		return nil, err
	}

	// EncapContentInfo with an embedded eContent OCTET STRING under [0] EXPLICIT.
	eci := struct {
		ContentType asn1.ObjectIdentifier
		EContent    []byte `asn1:"explicit,tag:0"`
	}{ContentType: contentType, EContent: content}
	eciDER, err := asn1.Marshal(eci)
	if err != nil {
		return nil, err
	}

	sd := struct {
		Version          int
		DigestAlgorithms asn1.RawValue
		EncapContentInfo asn1.RawValue
		Certificates     asn1.RawValue
		SignerInfos      asn1.RawValue
	}{
		Version:          3,
		DigestAlgorithms: asn1.RawValue{FullBytes: derSet([][]byte{mustMarshal(pkixAlgorithmIdentifier{Algorithm: oidSHA256})})},
		EncapContentInfo: asn1.RawValue{FullBytes: eciDER},
		Certificates:     asn1.RawValue{Class: 2, Tag: 0, IsCompound: true, Bytes: cert.Raw},
		SignerInfos:      asn1.RawValue{FullBytes: derSet([][]byte{siDER})},
	}
	sdDER, err := asn1.Marshal(sd)
	if err != nil {
		return nil, err
	}
	return asn1.Marshal(contentInfoMarshal{ContentType: oidSignedData, Content: asn1.RawValue{Class: 2, Tag: 0, IsCompound: true, Bytes: sdDER}})
}

// timestampToken is what verifying an RFC 3161 time-stamp token established:
// the time the authority asserts, and the authority's certificate with the
// others the token carried, for building its chain.
type timestampToken struct {
	genTime time.Time
	cert    *x509.Certificate
	certs   []*x509.Certificate
}

// verifyTimestampToken verifies an RFC 3161 time-stamp token over the data
// whose digest imprint returns:
//
//   - it is a SignedData whose eContentType is id-ct-TSTInfo (RFC 3161 2.4.2;
//     a SignedData of any other content is not a time-stamp, however it is
//     signed — audit 2026-09-22 C154);
//   - the authority's signature over the TSTInfo verifies;
//   - the authority's certificate carries the id-kp-timeStamping extended key
//     usage (RFC 3161 2.3);
//   - the TSTInfo's message imprint is the digest of the data, under the
//     imprint's own algorithm, which must not be SHA-1 or MD5.
//
// It does not establish that the authority is trusted — that is a chain to
// the caller's roots (trust.go) — so the time it returns is only what the
// token asserts.
func verifyTimestampToken(tokenDER []byte, imprint contentDigest) (*timestampToken, error) {
	sd, err := parseSignedData(tokenDER)
	if sd == nil {
		return nil, fmt.Errorf("time-stamp token: %w", err)
	}
	tok := &timestampToken{cert: sd.cert, certs: sd.certs}
	if err != nil {
		return tok, fmt.Errorf("time-stamp token: %w", err)
	}
	if !sd.eContentType.Equal(oidTSTInfo) {
		return tok, errors.New("time-stamp token content is not id-ct-TSTInfo")
	}
	if !sd.hasEContent || len(sd.eContent) == 0 {
		return tok, errors.New("time-stamp token carries no TSTInfo")
	}
	if _, err := sd.verify(digestOf(sd.eContent)); err != nil {
		return tok, fmt.Errorf("time-stamp token signature: %w", err)
	}
	// Without the EKU check any certificate would do as a TSA, letting anyone
	// assert any time (audit 2026-07-26 C10).
	if err := requireTimeStampingEKU(sd.cert); err != nil {
		return tok, err
	}
	var info tstInfo
	if _, err := asn1.Unmarshal(sd.eContent, &info); err != nil {
		return tok, fmt.Errorf("parsing TSTInfo: %w", err)
	}
	hashFn, ok := hashForOID(info.MessageImprint.HashAlgorithm.Algorithm)
	if !ok {
		return tok, errors.New("unsupported message-imprint hash algorithm")
	}
	if hashFn == crypto.SHA1 || hashFn == crypto.MD5 {
		return tok, errors.New("weak message-imprint hash algorithm (SHA-1/MD5) is not accepted")
	}
	want, err := imprint(hashFn)
	if err != nil {
		return tok, err
	}
	if !bytes.Equal(want, info.MessageImprint.HashedMessage) {
		return tok, errors.New("time-stamp message imprint does not match the time-stamped data")
	}
	tok.genTime = info.GenTime
	return tok, nil
}

// requireTimeStampingEKU reports whether a certificate is usable as a TSA: it
// must carry the id-kp-timeStamping extended key usage (RFC 3161 2.3).
func requireTimeStampingEKU(cert *x509.Certificate) error {
	if cert == nil {
		return errors.New("time-stamp token has no signer certificate")
	}
	for _, eku := range cert.ExtKeyUsage {
		if eku == x509.ExtKeyUsageTimeStamping {
			return nil
		}
	}
	return errors.New("time-stamp certificate lacks the id-kp-timeStamping extended key usage")
}
