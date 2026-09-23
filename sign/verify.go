package sign

import (
	"crypto"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// This file is signature verification as a whole: what each field of a Result
// promises, and the order in which it is established.
//
// A positive verdict has to mean three things (audit 2026-09-22 §3.A): the
// bytes the signer signed are the bytes the reader sees, or differ from them
// only in ways that are permitted; the signer chains to a root the caller
// chose, with a document-signing purpose; and revocation data is
// authenticated by the certificate's real issuer. Each has its own field, and
// none is implied by another.

// VerifyOptions are the caller's trust decisions.
type VerifyOptions struct {
	// Roots are the trust anchors a signer's certificate must chain to. With
	// nil roots no chain is built and no signature is trusted: TrustedChain is
	// always false. There is no default store. A web PKI store such as
	// x509.SystemCertPool() is the wrong choice: it trusts CAs that issue
	// certificates to anyone who controls a domain, and document signing needs
	// the roots of the signers you actually accept.
	Roots *x509.CertPool
	// TSARoots are the trust anchors a time-stamp authority must chain to.
	// When nil, Roots is used. A time-stamp whose authority does not chain to
	// them is still checked cryptographically, but it is not trusted: its
	// time is never used as the validation time.
	TSARoots *x509.CertPool
}

func (o VerifyOptions) tsaRoots() *x509.CertPool {
	if o.TSARoots != nil {
		return o.TSARoots
	}
	return o.Roots
}

// Result reports the outcome of verifying one signature, or one document
// time-stamp.
//
// Its fields are independent claims, and the safe verdicts combine them:
//
//   - Valid: the signed bytes are intact and the signature over them verifies.
//   - DocumentUnmodified(): Valid, and the signature covers the whole file, so
//     nothing at all was changed after signing.
//   - Intact(): Valid, and every change made after signing is a permitted one
//     (ChangesAllowed): a Document Security Store or document time-stamp added
//     for long-term validation, say. A timestamp never makes a change
//     permitted; it proves only when bytes existed.
//   - TrustedChain: the signer chains to your roots for a document-signing
//     purpose, at ValidationTime.
//   - Revocation: what authenticated revocation data in the document says about
//     the signer at ValidationTime.
type Result struct {
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
	Field string
	// DocTimestamp is set for a document time-stamp (/Type /DocTimeStamp, ISO
	// 32000-2 12.8.5): an RFC 3161 token over the file rather than a signature
	// by a person. For one, Valid means the token verifies over the bytes it
	// covers, SignerCommonName names the time-stamp authority, TimestampTime
	// is the time it asserts, and TrustedChain says whether the authority
	// chains to VerifyOptions.TSARoots.
	DocTimestamp bool
	// SignerCommonName is the Subject CN of the signing certificate: a display
	// label, filled in even when verification fails, and no identity
	// assurance on its own.
	SignerCommonName string
	// CoversWholeDocument: the /ByteRange covers every byte of the file except
	// the signature's own /Contents value.
	CoversWholeDocument bool
	// Valid: the /ByteRange has the one layout a signature may have (see
	// readSignedRange), the bytes it covers are intact, and the signature (or
	// time-stamp token) over them verifies under the embedded certificate.
	Valid bool
	// SigningTime is the signing-time signed attribute, if present. The signer
	// asserts it and nothing verifies it.
	SigningTime time.Time
	// TimestampTime is the time a verified time-stamp asserts: the signature
	// time-stamp of a PAdES B-T signature, or a document time-stamp's own. It
	// is trustworthy only when TimestampTrusted is set.
	TimestampTime time.Time
	// TimestampTrusted: the time-stamp behind TimestampTime verifies and its
	// authority chains to your time-stamp roots for the time-stamping purpose.
	TimestampTrusted bool
	// TrustedChain: the certificate chains to one of VerifyOptions.Roots
	// (TSARoots for a document time-stamp), valid at ValidationTime, with the
	// purpose the certificate is used for: document signing for a signer (a
	// key usage of digitalSignature or contentCommitment, and an extended key
	// usage that is absent or permits document signing), time-stamping for a
	// time-stamp authority. Never set without roots.
	TrustedChain bool
	// ChainErr says why the chain did not build or its purpose was refused.
	ChainErr error
	// ValidationTime is the time the chain and revocation were judged at: the
	// earliest time a trusted time-stamp proves the signature existed — its
	// own signature time-stamp, or a document time-stamp covering it — or the
	// current time when there is none.
	ValidationTime time.Time
	// Revocation is the signer certificate's status at ValidationTime, from
	// the CRLs and OCSP responses in the document's Document Security Store,
	// authenticated by the certificate's issuer: the next certificate of the
	// verified chain, or, without roots, a certificate whose key verifiably
	// issued it. A revocation from any authenticated source wins over a
	// "good" from another. Nothing is fetched from the network.
	Revocation RevocationInfo
	// Revision is the index, into the file's revisions (Source.Revisions), of
	// the revision the signature covers, or -1 when it covers none: a
	// signature whose byte range does not end at a revision boundary.
	Revision int
	// ChangesAllowed: every change made to the file after the signed revision
	// is permitted. It is set when there are none. It is false when the
	// changes could not be established, and for a signature that is not
	// Valid, whose signed revision nothing vouches for.
	ChangesAllowed bool
	// DisallowedChanges describes each change after the signature that is not
	// permitted, or why the changes could not be established.
	DisallowedChanges []string
	// Err says why the signature did not verify.
	Err error

	// Not exported: what ValidatePAdES reuses.
	num           int
	rg            signedRange
	sd            *signedData
	tsPresent     bool
	tsErr         error
	subFilter     object.Name
	hasCertInDict bool
}

// DocumentUnmodified reports the strictest verdict: the signature verifies and
// covers the whole file, so nothing was changed after signing.
func (r Result) DocumentUnmodified() bool {
	return r.Valid && r.CoversWholeDocument
}

// Intact reports that the signature verifies and every change made after it is
// permitted (see ChangesAllowed). It is the verdict to read for a document that
// has been through long-term-validation updates. It says nothing about who
// signed: read TrustedChain and Revocation for that.
func (r Result) Intact() bool {
	return r.Valid && r.ChangesAllowed
}

// VerifySignatures verifies every signature and document time-stamp in the
// document against the file it was read from. Results are ordered by the
// object number of the signature dictionary, which is stable across runs (the
// objects are held in a map, whose iteration order is not) and meaningful: in a
// document signed by successive incremental updates the later signature is the
// later object.
func VerifySignatures(d core.View, file core.SignedFile, opts VerifyOptions) []Result {
	return verifyAll(d, file, opts)
}

// verifyAll does the work of VerifySignatures and ValidatePAdES.
func verifyAll(d core.View, file core.SignedFile, opts VerifyOptions) []Result {
	entries := documentSignatures(d, true)
	if len(entries) == 0 {
		return nil
	}
	names := signatureFieldNames(d, entries)
	now := time.Now()
	ends := file.RevisionEnds()

	results := make([]Result, len(entries))
	byEnd := map[int64][]int{}
	for i, e := range entries {
		r := &results[i]
		r.num, r.Field, r.Revision = e.num, names[e.num], -1
		t, _ := d.ResolveName(e.dict.Get("Type"))
		r.DocTimestamp = t == "DocTimeStamp"
		r.subFilter, _ = d.ResolveName(e.dict.Get("SubFilter"))
		r.hasCertInDict = e.dict.Get("Cert") != nil
		rg, err := readSignedRange(d, e.dict, file)
		if err != nil {
			r.Err = err
			continue
		}
		r.rg = rg
		r.CoversWholeDocument = rg.covers(file.Len())
		if rev, ok := revisionOf(file, ends, rg); ok {
			r.Revision = rev
		} else if !r.CoversWholeDocument {
			r.Err = errors.New("the /ByteRange neither covers the whole file nor ends at the end of the revision holding the signature")
			continue
		}
		byEnd[rg.end] = append(byEnd[rg.end], i)
	}
	// At most one signature can be valid over any one range end: each covers
	// the other's value, so whichever was filled last broke the other. More
	// than one is a crafted file, and verifying them all would hash the same
	// bytes once per claimant.
	var order []int
	for end, idx := range byEnd {
		if len(idx) > 1 {
			for _, i := range idx {
				results[i].Err = fmt.Errorf("%d signature dictionaries claim the byte range ending at offset %d; at most one of them can be valid", len(idx), end)
			}
			continue
		}
		order = append(order, idx[0])
	}
	// Ascending gap start, so the hasher's prefix states only move forward.
	sort.Slice(order, func(a, b int) bool { return results[order[a]].rg.gapStart < results[order[b]].rg.gapStart })

	hasher := newRangeHasher(file.ReaderAt())
	for _, i := range order {
		r := &results[i]
		contents, _ := d.Resolve(entries[i].dict.Get("Contents")).(object.String)
		rangeDigest := func(h crypto.Hash) ([]byte, error) { return hasher.digest(h, r.rg) }
		if r.DocTimestamp {
			tok, err := verifyTimestampToken(contents.Value, rangeDigest)
			if tok != nil && tok.cert != nil {
				r.SignerCommonName = tok.cert.Subject.CommonName
			}
			if err != nil {
				r.Err = err
				continue
			}
			r.Valid = true
			r.TimestampTime = tok.genTime
			r.ValidationTime = now
			chains, err := tsaChains(tok.cert, tok.certs, opts.tsaRoots(), now)
			r.TrustedChain, r.ChainErr = err == nil && len(chains) > 0, err
			r.TimestampTrusted = r.TrustedChain
			continue
		}
		sd, err := parseSignedData(contents.Value)
		if sd != nil && sd.cert != nil {
			r.SignerCommonName = sd.cert.Subject.CommonName
		}
		r.sd = sd
		if err != nil {
			r.Err = err
			continue
		}
		if sd.hasEContent {
			// A PDF signature signs the file (ISO 32000-2 12.8.3.3: the
			// detached sub-filters); a SignedData that carries its own content
			// signs that content, not the file, whatever its digest says.
			r.Err = errors.New("the signature encapsulates content: a document signature must be detached")
			continue
		}
		if !sd.eContentType.Equal(oidData) {
			r.Err = errors.New("the signature's eContentType is not id-data")
			continue
		}
		r.SigningTime, err = sd.verify(rangeDigest)
		if err != nil {
			r.Err = err
			continue
		}
		r.Valid = true
		// The signature time-stamp of a B-T signature: an RFC 3161 token over
		// the signature value, in the SignerInfo's unsigned attributes.
		if token := attrValue(sd.si.UnsignedAttrs.Bytes, oidSignatureTimeStamp); token != nil {
			r.tsPresent = true
			tok, err := verifyTimestampToken(token, digestOf(sd.si.Signature))
			if err != nil {
				r.tsErr = err
			} else {
				r.TimestampTime = tok.genTime
				if chains, err := tsaChains(tok.cert, tok.certs, opts.tsaRoots(), now); err == nil && len(chains) > 0 {
					r.TimestampTrusted = true
				}
			}
		}
	}

	dssCerts := DSSCerts(d)
	crls, ocsps := DSSRevocationMaterial(d)
	for i := range results {
		r := &results[i]
		if !r.Valid {
			r.DisallowedChanges = nil
			continue
		}
		if !r.DocTimestamp {
			r.ValidationTime = validationTime(r, results, now)
			establishTrust(r, dssCerts, crls, ocsps, opts)
		}
		r.ChangesAllowed, r.DisallowedChanges = changesAfter(file, ends, r)
	}
	return results
}

// revisionOf finds the revision a signed range covers: the one whose %%EOF
// (with its end-of-line) closes at the range's end — a range may stop before
// the end-of-line that follows %%EOF — and whose own bytes hold the gap, the
// signature's value. The second condition is what a signature written by an
// incremental update looks like, and it bounds verification: at most a few
// ranges can end at each revision, and each one's bytes after its gap lie in
// its own revision.
func revisionOf(file core.SignedFile, ends []int64, rg signedRange) (int, bool) {
	i := sort.Search(len(ends), func(i int) bool { return ends[i] >= rg.end })
	if i == len(ends) {
		return 0, false
	}
	var start int64
	if i > 0 {
		start = ends[i-1]
	}
	if rg.end <= start || rg.gapStart < start {
		return 0, false
	}
	if n := ends[i] - rg.end; n > 0 {
		if n > 2 {
			return 0, false
		}
		buf := make([]byte, n)
		if _, err := file.ReaderAt().ReadAt(buf, rg.end); err != nil && err != io.EOF {
			return 0, false
		}
		for _, b := range buf {
			if b != '\r' && b != '\n' {
				return 0, false
			}
		}
	}
	return i, true
}

// validationTime is the time a signature is judged at: the earliest time a
// verified, trusted time-stamp proves it existed — its own signature
// time-stamp, or a document time-stamp over a later revision, which covers the
// signature's bytes — or now. A time-stamp that does not verify, or whose
// authority is not trusted, proves nothing and is not used.
func validationTime(r *Result, all []Result, now time.Time) time.Time {
	best := time.Time{}
	consider := func(t time.Time) {
		if !t.IsZero() && (best.IsZero() || t.Before(best)) {
			best = t
		}
	}
	if r.TimestampTrusted {
		consider(r.TimestampTime)
	}
	for i := range all {
		ts := &all[i]
		if ts.DocTimestamp && ts.Valid && ts.TimestampTrusted && ts.rg.gapStart >= r.rg.end {
			consider(ts.TimestampTime)
		}
	}
	if best.IsZero() || best.After(now) {
		return now
	}
	return best
}

// establishTrust builds the signer's chain at the validation time and reads
// the revocation data in the document for its issuer.
func establishTrust(r *Result, dssCerts []*x509.Certificate, crls, ocsps [][]byte, opts VerifyOptions) {
	cert := r.sd.cert
	pool := append(append([]*x509.Certificate(nil), r.sd.certs...), dssCerts...)
	var issuers []*x509.Certificate
	if opts.Roots != nil {
		chains, err := signerChains(cert, pool, opts.Roots, r.ValidationTime)
		if err != nil {
			r.ChainErr = err
		} else {
			r.TrustedChain = true
			issuers = chainIssuers(chains)
		}
	}
	if !r.TrustedChain {
		issuers = signingIssuers(cert, pool)
	}
	if len(crls) == 0 && len(ocsps) == 0 {
		return
	}
	// Several verified chains can name different issuers (a re-issued CA
	// certificate, cross-certification). A revocation through any of them
	// wins; otherwise the first "good", otherwise the first answer at all.
	var good, unknown RevocationInfo
	for _, iss := range issuers {
		info := CheckCertRevocation(cert, iss, crls, ocsps, r.ValidationTime)
		switch {
		case info.Status == RevocationRevoked:
			r.Revocation = info
			return
		case info.Status == RevocationGood && good.Status != RevocationGood:
			good = info
		case info.Source != "" && unknown.Source == "":
			unknown = info
		}
	}
	if good.Status == RevocationGood {
		r.Revocation = good
	} else {
		r.Revocation = unknown
	}
}

// changesAfter judges the changes made to the file after the revision a valid
// signature covers.
func changesAfter(file core.SignedFile, ends []int64, r *Result) (bool, []string) {
	if r.CoversWholeDocument {
		return true, nil
	}
	if r.Revision < 0 {
		return false, []string{"the signed bytes do not end at a revision of the file"}
	}
	var bad []string
	if n := len(ends); n > 0 && ends[n-1] < file.Len() {
		if msg := trailingBytes(file, ends[n-1]); msg != "" {
			bad = append(bad, msg)
		}
	}
	diff, err := file.Diff(r.Revision)
	if err != nil {
		return false, append(bad, "the changes after the signature could not be established: "+err.Error())
	}
	bad = append(bad, disallowedChanges(diff)...)
	return len(bad) == 0, bad
}

// trailingBytes describes bytes after the file's last %%EOF that are not
// whitespace, which no revision accounts for, or returns "".
func trailingBytes(file core.SignedFile, from int64) string {
	buf := make([]byte, 32<<10)
	for off := from; off < file.Len(); {
		n := int64(len(buf))
		if rem := file.Len() - off; rem < n {
			n = rem
		}
		got, err := file.ReaderAt().ReadAt(buf[:n], off)
		if int64(got) != n && err != nil {
			return "the bytes after the file's last %EOF could not be read"
		}
		for i, b := range buf[:n] {
			switch b {
			case ' ', '\t', '\r', '\n', '\f', 0:
				continue
			}
			return fmt.Sprintf("%d bytes after the file's last %%%%EOF (from offset %d) are not part of any revision", file.Len()-from, off+int64(i))
		}
		off += n
	}
	return ""
}
