package pdf0

import (
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
func buildTimestampToken(imprint []byte, tsaCert *x509.Certificate, tsaKey crypto.Signer, genTime time.Time) ([]byte, error) {
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

// verifyTimestampToken verifies a time-stamp token: it checks the TSA signature
// over the embedded TSTInfo and that the token's message imprint matches imprint
// (the bytes that were supposed to be time-stamped). It returns the asserted time
// and the TSA certificate.
func verifyTimestampToken(tokenDER, imprint []byte) (time.Time, *x509.Certificate, error) {
	tstDER, err := extractEContent(tokenDER)
	if err != nil {
		return time.Time{}, nil, err
	}
	// Verify the TSA's signature over the TSTInfo.
	cert, _, _, err := verifyCMS(tokenDER, tstDER)
	if err != nil {
		return time.Time{}, cert, fmt.Errorf("time-stamp token signature: %w", err)
	}
	var info tstInfo
	if _, err := asn1.Unmarshal(tstDER, &info); err != nil {
		return time.Time{}, cert, fmt.Errorf("parsing TSTInfo: %w", err)
	}
	hashFn, ok := hashForOID(info.MessageImprint.HashAlgorithm.Algorithm)
	if !ok {
		return time.Time{}, cert, errors.New("unsupported message-imprint hash algorithm")
	}
	h := hashFn.New()
	h.Write(imprint)
	if !bytesEqual(h.Sum(nil), info.MessageImprint.HashedMessage) {
		return time.Time{}, cert, errors.New("time-stamp message imprint does not match the signature")
	}
	return info.GenTime, cert, nil
}

// extractEContent returns the eContent bytes of a CMS SignedData (the payload of
// a time-stamp token: the TSTInfo DER).
func extractEContent(der []byte) ([]byte, error) {
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
		Rest             asn1.RawValue `asn1:"optional"`
	}
	if _, err := asn1.Unmarshal(ci.Content.Bytes, &sd); err != nil {
		return nil, fmt.Errorf("parsing SignedData: %w", err)
	}
	var eci struct {
		ContentType asn1.ObjectIdentifier
		EContent    []byte `asn1:"explicit,optional,tag:0"`
	}
	if _, err := asn1.Unmarshal(sd.EncapContentInfo.FullBytes, &eci); err != nil {
		return nil, fmt.Errorf("parsing EncapContentInfo: %w", err)
	}
	if len(eci.EContent) == 0 {
		return nil, errors.New("time-stamp token carries no content")
	}
	return eci.EContent, nil
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
