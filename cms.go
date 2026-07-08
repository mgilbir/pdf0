package pdf0

import "encoding/asn1"

// cmsSignedData summarizes the parts of a PKCS#7/CMS SignedData blob (RFC 5652)
// that PDF/A signature validation cares about.
type cmsSignedData struct {
	parsed          bool // the bytes are a well-formed SignedData ContentInfo
	hasCertificate  bool // the certificates field carries at least one certificate
	signerInfoCount int  // number of SignerInfo entries
}

// oidSignedData is id-signedData (RFC 5652 §5.1): 1.2.840.113549.1.7.2.
var oidSignedData = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}

// parseCMSSignedData decodes a DER-encoded CMS/PKCS#7 SignedData structure far
// enough to report whether it embeds a signing certificate and how many
// SignerInfos it contains. It never errors: a blob that is not SignedData (or is
// truncated) simply comes back with parsed=false, since the raw signature bytes
// of an adbe.x509.rsa_sha1 signature are not CMS.
func parseCMSSignedData(der []byte) cmsSignedData {
	// ContentInfo ::= SEQUENCE { contentType OID, content [0] EXPLICIT ANY }
	var ci struct {
		ContentType asn1.ObjectIdentifier
		Content     asn1.RawValue `asn1:"explicit,optional,tag:0"`
	}
	if _, err := asn1.Unmarshal(der, &ci); err != nil {
		return cmsSignedData{}
	}
	if !ci.ContentType.Equal(oidSignedData) || len(ci.Content.Bytes) == 0 {
		return cmsSignedData{}
	}

	// SignedData ::= SEQUENCE {
	//   version, digestAlgorithms SET, encapContentInfo SEQUENCE,
	//   certificates [0] IMPLICIT OPTIONAL, crls [1] IMPLICIT OPTIONAL,
	//   signerInfos SET OF SignerInfo }
	var sd struct {
		Version          int
		DigestAlgorithms asn1.RawValue
		EncapContentInfo asn1.RawValue
		Certificates     asn1.RawValue   `asn1:"optional,tag:0"`
		CRLs             asn1.RawValue   `asn1:"optional,tag:1"`
		SignerInfos      []asn1.RawValue `asn1:"set"`
	}
	if _, err := asn1.Unmarshal(ci.Content.Bytes, &sd); err != nil {
		return cmsSignedData{}
	}
	return cmsSignedData{
		parsed:          true,
		hasCertificate:  len(sd.Certificates.Bytes) > 0,
		signerInfoCount: len(sd.SignerInfos),
	}
}
