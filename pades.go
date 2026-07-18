package pdf0

import (
	"encoding/asn1"
	"time"
)

// This file validates PDF Advanced Electronic Signatures (PAdES, ETSI EN 319
// 142) on top of the CMS signature verification in signatures.go. It assesses,
// per signature, whether it conforms to the PAdES baseline and which baseline
// level it reaches (B-B, B-T, B-LT, B-LTA). PAdES is CAdES carried in a PDF: the
// signature must use the ETSI.CAdES.detached sub-filter, bind the signer
// certificate through the CAdES signing-certificate attribute, put the
// certificate in the CMS (not the signature dictionary), and cover the file.

// PAdESLevel is a PAdES baseline conformance level.
type PAdESLevel string

const (
	PAdESNone PAdESLevel = ""      // not a PAdES signature (e.g. legacy adbe.*)
	PAdESBB   PAdESLevel = "B-B"   // basic: conformant CAdES-BES
	PAdESBT   PAdESLevel = "B-T"   // + a signature timestamp
	PAdESBLT  PAdESLevel = "B-LT"  // + long-term validation material (DSS)
	PAdESBLTA PAdESLevel = "B-LTA" // + a document timestamp over the DSS
)

// PAdESResult reports the PAdES assessment of one signature.
type PAdESResult struct {
	Field            string     // signature field name, if any
	SubFilter        string     // the signature /SubFilter
	IsPAdES          bool       // uses a PAdES sub-filter
	Level            PAdESLevel // baseline level reached (PAdESNone if not PAdES)
	Conformant       bool       // meets the PAdES B-B baseline requirements
	Valid            bool       // the CMS signature cryptographically verifies
	CoversDocument   bool       // the /ByteRange reaches the end of the file
	SignerCommonName string
	TimestampValid   bool      // the signature time-stamp verifies (imprint + TSA signature)
	TimestampTime    time.Time // the time asserted by a verified signature time-stamp
	Issues           []string  // PAdES conformance problems
}

// ValidatePAdES assesses every signature in the document for PAdES conformance
// against the original file bytes.
func (d *Document) ValidatePAdES(raw []byte) []PAdESResult {
	// Document-level long-term material: a catalog /DSS holds validation data
	// (certificates, OCSP, CRLs); a document timestamp (/Type /DocTimeStamp)
	// archives it for the B-LTA level.
	hasDSS := false
	if cat := getCatalog(d); cat != nil {
		hasDSS = d.ResolveDict(cat.Get("DSS")) != nil
	}
	hasDocTimestamp := false
	for _, iobj := range d.Objects {
		if dict, ok := iobj.Value.(*Dictionary); ok {
			if t, _ := dict.Get("Type").(Name); t == "DocTimeStamp" && dict.Get("ByteRange") != nil {
				hasDocTimestamp = true
			}
		}
	}

	var out []PAdESResult
	for _, iobj := range d.Objects {
		dict, ok := iobj.Value.(*Dictionary)
		if !ok || dict.Get("ByteRange") == nil || dict.Get("Contents") == nil {
			continue
		}
		t, _ := dict.Get("Type").(Name)
		if t == "DocTimeStamp" {
			continue // assessed as long-term material, not an approval signature
		}
		if t != "" && t != "Sig" {
			continue
		}
		out = append(out, d.assessPAdES(dict, raw, hasDSS, hasDocTimestamp))
	}
	return out
}

func (d *Document) assessPAdES(sig *Dictionary, raw []byte, hasDSS, hasDocTimestamp bool) PAdESResult {
	var res PAdESResult
	sub, _ := sig.Get("SubFilter").(Name)
	res.SubFilter = string(sub)

	// Reuse the CMS verification for cryptographic validity and signer identity.
	v := d.verifyOneSignature(sig, raw, nil)
	res.Valid = v.Valid
	res.CoversDocument = v.CoversWholeDocument
	res.SignerCommonName = v.SignerCommonName

	if sub != "ETSI.CAdES.detached" {
		// A legacy (adbe.*) or timestamp sub-filter is not a PAdES approval
		// signature; report it but do not assess a level.
		res.Issues = append(res.Issues, "sub-filter is not ETSI.CAdES.detached; not a PAdES signature")
		return res
	}
	res.IsPAdES = true

	// B-B baseline requirements.
	if !v.Valid {
		if v.Err != nil {
			res.Issues = append(res.Issues, "signature does not verify: "+v.Err.Error())
		} else {
			res.Issues = append(res.Issues, "signature does not verify")
		}
	}
	if sig.Get("Cert") != nil {
		res.Issues = append(res.Issues, "the signature dictionary must not contain /Cert; the certificate belongs in the CMS")
	}
	if !v.CoversWholeDocument {
		res.Issues = append(res.Issues, "the /ByteRange does not cover the whole document")
	}

	contents, _ := d.Resolve(sig.Get("Contents")).(String)
	hasSigningCert, hasSigTimestamp := cmsPAdESFacts(contents.Value)
	if !hasSigningCert {
		res.Issues = append(res.Issues, "the CMS lacks a signing-certificate attribute (required for CAdES-BES)")
	}

	// Cryptographically verify the B-T signature time-stamp: its TSA signature and
	// that its message imprint is the hash of the outer signature value.
	if hasSigTimestamp {
		if token, sigValue, ok := extractSignatureTimestamp(contents.Value); ok {
			if genTime, _, err := verifyTimestampToken(token, sigValue); err == nil {
				res.TimestampValid = true
				res.TimestampTime = genTime
			} else {
				res.Issues = append(res.Issues, "signature time-stamp does not verify: "+err.Error())
			}
		}
	}

	res.Conformant = len(res.Issues) == 0

	// Baseline level: each level requires the previous. A non-conformant B-B
	// still reports the highest material present, but Conformant stays false.
	res.Level = PAdESBB
	if hasSigTimestamp {
		res.Level = PAdESBT
		if hasDSS {
			res.Level = PAdESBLT
			if hasDocTimestamp {
				res.Level = PAdESBLTA
			}
		}
	}
	return res
}

// extractSignatureTimestamp returns the signature time-stamp token and the outer
// signature value it is computed over, from a CMS SignedData.
func extractSignatureTimestamp(der []byte) (token, sigValue []byte, ok bool) {
	var ci struct {
		ContentType asn1.ObjectIdentifier
		Content     asn1.RawValue `asn1:"explicit,optional,tag:0"`
	}
	if _, err := asn1.Unmarshal(der, &ci); err != nil || !ci.ContentType.Equal(oidSignedData) {
		return nil, nil, false
	}
	var sd struct {
		Version          int
		DigestAlgorithms asn1.RawValue
		EncapContentInfo asn1.RawValue
		Certificates     asn1.RawValue   `asn1:"optional,tag:0"`
		CRLs             asn1.RawValue   `asn1:"optional,tag:1"`
		SignerInfos      []asn1.RawValue `asn1:"set"`
	}
	if _, err := asn1.Unmarshal(ci.Content.Bytes, &sd); err != nil || len(sd.SignerInfos) == 0 {
		return nil, nil, false
	}
	var si struct {
		Version         int
		SID             asn1.RawValue
		DigestAlgorithm asn1.RawValue
		SignedAttrs     asn1.RawValue `asn1:"optional,tag:0"`
		SignatureAlgo   asn1.RawValue
		Signature       []byte
		UnsignedAttrs   asn1.RawValue `asn1:"optional,tag:1"`
	}
	if _, err := asn1.Unmarshal(sd.SignerInfos[0].FullBytes, &si); err != nil {
		return nil, nil, false
	}
	token = attrValue(si.UnsignedAttrs.Bytes, oidSignatureTimeStamp)
	if token == nil {
		return nil, nil, false
	}
	return token, si.Signature, true
}

// attrValue returns the first value of the attribute with the given type in a DER
// SET/sequence of Attribute, or nil.
func attrValue(setBytes []byte, oid asn1.ObjectIdentifier) []byte {
	rest := setBytes
	for len(rest) > 0 {
		var a struct {
			Type   asn1.ObjectIdentifier
			Values asn1.RawValue `asn1:"set"`
		}
		var err error
		rest, err = asn1.Unmarshal(rest, &a)
		if err != nil {
			return nil
		}
		if a.Type.Equal(oid) {
			var v asn1.RawValue
			if _, err := asn1.Unmarshal(a.Values.Bytes, &v); err == nil {
				return v.FullBytes
			}
		}
	}
	return nil
}

// cmsPAdESFacts reports whether a CMS SignedData carries the CAdES signing-
// certificate signed attribute and a signature-timestamp unsigned attribute.
// It is best-effort: a parse failure returns false, false.
func cmsPAdESFacts(der []byte) (hasSigningCert, hasSigTimestamp bool) {
	var ci struct {
		ContentType asn1.ObjectIdentifier
		Content     asn1.RawValue `asn1:"explicit,optional,tag:0"`
	}
	if _, err := asn1.Unmarshal(der, &ci); err != nil || !ci.ContentType.Equal(oidSignedData) {
		return
	}
	var sd struct {
		Version          int
		DigestAlgorithms asn1.RawValue
		EncapContentInfo asn1.RawValue
		Certificates     asn1.RawValue   `asn1:"optional,tag:0"`
		CRLs             asn1.RawValue   `asn1:"optional,tag:1"`
		SignerInfos      []asn1.RawValue `asn1:"set"`
	}
	if _, err := asn1.Unmarshal(ci.Content.Bytes, &sd); err != nil || len(sd.SignerInfos) == 0 {
		return
	}
	var si struct {
		Version         int
		SID             asn1.RawValue
		DigestAlgorithm asn1.RawValue
		SignedAttrs     asn1.RawValue `asn1:"optional,tag:0"`
		SignatureAlgo   asn1.RawValue
		Signature       []byte
		UnsignedAttrs   asn1.RawValue `asn1:"optional,tag:1"`
	}
	if _, err := asn1.Unmarshal(sd.SignerInfos[0].FullBytes, &si); err != nil {
		return
	}
	signed := attrTypesPresent(si.SignedAttrs.Bytes)
	hasSigningCert = signed[oidSigningCertificate.String()] || signed[oidSigningCertificateV2.String()]
	unsigned := attrTypesPresent(si.UnsignedAttrs.Bytes)
	hasSigTimestamp = unsigned[oidSignatureTimeStamp.String()]
	return
}

// attrTypesPresent returns the set of attribute-type OIDs present in a DER
// SET/sequence of Attribute, keyed by dotted OID string.
func attrTypesPresent(b []byte) map[string]bool {
	present := map[string]bool{}
	rest := b
	for len(rest) > 0 {
		var a attribute
		var err error
		rest, err = asn1.Unmarshal(rest, &a)
		if err != nil {
			break
		}
		present[a.Type.String()] = true
	}
	return present
}
