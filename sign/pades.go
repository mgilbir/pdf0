package sign

import (
	"encoding/asn1"
	"time"

	"github.com/mgilbir/pdf0/internal/core"
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
	// Field is the fully qualified name of the signature field whose /V
	// references this signature, exactly as Result.Field: the /T
	// partial names of the field and its ancestors joined with "." (ISO 32000-2
	// §12.7.4.2). It is empty when no field references the signature, or when no
	// field in the chain carries a /T.
	Field     string
	SubFilter string // the signature /SubFilter
	IsPAdES   bool   // uses the PAdES sub-filter, ETSI.CAdES.detached
	// Level is the baseline level whose material is present (PAdESNone if not
	// PAdES): B-B, plus a signature time-stamp for B-T, plus a catalog /DSS
	// for B-LT, plus a document time-stamp in a later revision for B-LTA. It
	// says what is there; Conformant says whether it holds up.
	Level PAdESLevel
	// Conformant: the B-B baseline requirements hold, the material Level
	// reports verifies, and every change made after the signature is
	// permitted — the Issues list is empty. A time-stamp never excuses a
	// change: it proves when bytes existed, not that a change was allowed.
	// Trust is reported separately (TrustedChain, TimestampTrusted).
	Conformant bool
	// Valid: the CMS signature cryptographically verifies (Result.Valid).
	Valid bool
	// CoversDocument: the /ByteRange covers the whole file except the
	// signature value (Result.CoversWholeDocument). A B-LT or B-LTA signature
	// never does — the validation material is added after it — and is judged
	// by ChangesAllowed instead.
	CoversDocument bool
	// ChangesAllowed and DisallowedChanges are Result's: whether every change
	// after the signature is permitted, and each one that is not.
	ChangesAllowed    bool
	DisallowedChanges []string
	SignerCommonName  string
	// TrustedChain is Result.TrustedChain: the signer chains to your roots
	// for document signing.
	TrustedChain bool
	// TimestampValid: the signature time-stamp verifies — its authority's
	// signature, the time-stamping purpose, and an imprint of this signature.
	TimestampValid bool
	// TimestampTrusted: the time-stamp authority chains to your time-stamp
	// roots. Without it, TimestampTime is only what the token asserts.
	TimestampTrusted bool
	// TimestampTime is the time the signature time-stamp asserts.
	TimestampTime time.Time
	// Issues lists the PAdES conformance problems.
	Issues []string
}

// ValidatePAdES assesses every approval signature in the document for PAdES
// baseline conformance (ETSI EN 319 142-1), against the file the document was
// read from. Document time-stamps are long-term material rather than approval
// signatures, and are assessed as part of the signatures they archive. Results
// are ordered by the object number of the signature dictionary, the same
// deterministic order VerifySignatures uses.
func ValidatePAdES(d core.View, file core.SignedFile, opts VerifyOptions) []PAdESResult {
	all := verifyAll(d, file, opts)
	hasDSS := false
	if cat := d.Catalog(); cat != nil {
		hasDSS = d.ResolveDict(cat.Get("DSS")) != nil
	}
	var out []PAdESResult
	for i := range all {
		if all[i].DocTimestamp {
			continue
		}
		out = append(out, assessPAdES(&all[i], all, hasDSS))
	}
	return out
}

func assessPAdES(v *Result, all []Result, hasDSS bool) PAdESResult {
	res := PAdESResult{
		Field:             v.Field,
		SubFilter:         string(v.subFilter),
		Valid:             v.Valid,
		CoversDocument:    v.CoversWholeDocument,
		ChangesAllowed:    v.ChangesAllowed,
		DisallowedChanges: v.DisallowedChanges,
		SignerCommonName:  v.SignerCommonName,
		TrustedChain:      v.TrustedChain,
		TimestampTrusted:  v.TimestampTrusted,
	}
	if v.subFilter != "ETSI.CAdES.detached" {
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
	if v.hasCertInDict {
		res.Issues = append(res.Issues, "the signature dictionary must not contain /Cert; the certificate belongs in the CMS")
	}
	if v.Valid && !v.ChangesAllowed {
		for _, c := range v.DisallowedChanges {
			res.Issues = append(res.Issues, "the /ByteRange does not cover the whole document, and a change after it is not permitted: "+c)
		}
	}
	hasSigningCert := false
	if v.sd != nil {
		signed := attrTypesPresent(v.sd.si.SignedAttrs.Bytes)
		hasSigningCert = signed[oidSigningCertificate.String()] || signed[oidSigningCertificateV2.String()]
	}
	if !hasSigningCert {
		res.Issues = append(res.Issues, "the CMS lacks a signing-certificate attribute (required for CAdES-BES)")
	}
	if v.tsPresent {
		if v.tsErr != nil {
			res.Issues = append(res.Issues, "signature time-stamp does not verify: "+v.tsErr.Error())
		} else {
			res.TimestampValid = true
			res.TimestampTime = v.TimestampTime
		}
	}

	// Baseline level: each level requires the previous. A non-conformant B-B
	// still reports the highest material present, but Conformant stays false.
	res.Level = PAdESBB
	if v.tsPresent {
		res.Level = PAdESBT
		if hasDSS {
			res.Level = PAdESBLT
			// B-LTA: a document time-stamp over a later revision, which covers
			// this signature and the validation material; it must verify.
			found, verifies := false, false
			for i := range all {
				ts := &all[i]
				if ts.DocTimestamp && ts.rg.end > v.rg.end && ts.rg.gapStart >= v.rg.end {
					found = true
					verifies = verifies || ts.Valid
				}
			}
			if found {
				res.Level = PAdESBLTA
				if !verifies {
					res.Issues = append(res.Issues, "no document time-stamp after the signature verifies")
				}
			}
		}
	}
	res.Conformant = len(res.Issues) == 0
	return res
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
