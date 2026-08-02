package sign

import "encoding/asn1"

// This file is a deliberately shallow reader of CMS/PKCS#7 SignedData
// (RFC 5652), covering only the structural questions PDF/A signature validation
// asks of a /Contents blob — is it SignedData at all, does it embed a
// certificate, how many SignerInfos does it hold — without verifying anything.
// Cryptographic verification lives in signatures.go. Parsing never fails
// loudly: an adbe.x509.rsa_sha1 signature stores a bare signature value rather
// than CMS, so "not SignedData" is an ordinary answer here, not an error.

// core.CMSSignedData summarizes the parts of a PKCS#7/CMS SignedData blob (RFC 5652)
// that PDF/A signature validation cares about.

// oidSignedData is id-signedData (RFC 5652 §5.1): 1.2.840.113549.1.7.2.
var oidSignedData = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}
