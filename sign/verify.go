package sign

import (
	"crypto"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"math"
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
//     for long-term validation, say, or a later signature that is itself
//     intact. A timestamp never makes a change permitted; it proves only when
//     bytes existed.
//   - LaterVisibleSignatures: the later signatures Intact accepted whose
//     widgets a reader can see, drawn over the pages this signature covers.
//   - TrustedChain: the signer chains to your roots for a document-signing
//     purpose, at ValidationTime, through no revoked intermediate. It does not
//     include the signer's own revocation status.
//   - Revocation: what authenticated revocation data in the document says about
//     the signer at ValidationTime.
//
// A signature you accept therefore needs Intact() (or DocumentUnmodified()),
// TrustedChain, and a Revocation status that is not RevocationRevoked; and
// every other result the document carries needs the same scrutiny, since a
// later signature can draw anything it likes over the page.
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
	// Valid: the /ByteRange has the one layout a signature may have — four
	// non-negative integers [0 len1 start2 len2] naming two spans inside the
	// file, whose gap is exactly this signature's /Contents hex string — the
	// bytes it covers are intact, and the signature (or time-stamp token) over
	// them verifies under the embedded certificate.
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
	// time-stamp authority; and no intermediate of the chain is revoked. Never
	// set without roots.
	//
	// For a signer, it does not include the signer certificate's own
	// revocation: a revoked signer still has TrustedChain set, and Revocation
	// is where its status is reported. (For a document time-stamp, a revoked
	// authority is not trusted.) It is never set for a signature that is not
	// Valid: no chain is built for one.
	TrustedChain bool
	// ChainErr says why TrustedChain is not set for a Valid signature: the
	// chain did not build, its purpose was refused, an intermediate is
	// revoked, or no roots were supplied ("no trust roots were supplied"). It
	// is nil when TrustedChain is set, and for a signature that is not Valid.
	ChainErr error
	// ValidationTime is the time the chain and revocation were judged at: the
	// earliest time a trusted time-stamp proves the signature existed — its
	// own signature time-stamp, or a document time-stamp covering it — or the
	// current time when there is none.
	ValidationTime time.Time
	// Revocation is the signer certificate's status at ValidationTime (for a
	// document time-stamp, its authority's), from the CRLs and OCSP responses
	// in the document's Document Security Store, authenticated by the
	// certificate's issuer: the next certificate of the verified chain, or,
	// without roots, a certificate whose key verifiably issued it. A
	// revocation from any authenticated source wins over a "good" from
	// another. Every intermediate of the chain is checked too: a revoked one
	// makes the chain untrusted, and its revocation is what Revocation then
	// reports, with ChainErr naming the certificate. Nothing is fetched from
	// the network.
	Revocation RevocationInfo
	// Revision is the index, into the file's revisions (Source.Revisions), of
	// the revision the signature covers, or -1 when it covers none: a
	// signature whose byte range does not end at a revision boundary.
	Revision int
	// ChangesAllowed: every change made to the file after the signed revision
	// is permitted. It is set when there are none. It is false when the
	// changes could not be established, and for a signature that is not
	// Valid, whose signed revision nothing vouches for.
	//
	// A signature field added later, or a value given later to an empty
	// signature field, is permitted only when that later signature is itself
	// intact: it verifies, and every change after it is permitted. A later
	// "signature" that does not verify, or whose field or appearance changed
	// after it was signed, is a change like any other. Who made a later
	// signature is not judged here: read its own Result.
	ChangesAllowed bool
	// DisallowedChanges describes each change after the signature that is not
	// permitted, or why the changes could not be established.
	DisallowedChanges []string
	// LaterVisibleSignatures names, by their Result.Field, the signatures
	// added after this one — new signature fields, or values given to empty
	// ones — that ChangesAllowed permitted and that a reader can see: a
	// widget of the field has a /Rect of non-zero area, neither its Hidden
	// nor its NoView flag set, and a normal appearance (/AP /N). Such a widget
	// draws over the page this signature covers, and nothing limits what it
	// draws: a second party's signature box, or a full-page overlay that
	// changes what the page appears to say. The later signature is only as
	// trustworthy as its own Result's TrustedChain and Revocation; a later
	// signer you do not trust may have drawn anything. A field with no name is
	// listed as "". It is empty when there are none, and for a signature that
	// is not Valid.
	LaterVisibleSignatures []string
	// Err says why the signature did not verify.
	Err error

	// Not exported: what ValidatePAdES reuses.
	tsTok         *timestampToken
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

// verifySignatures verifies every signature and document time-stamp in the
// document against the file it was read from. Results are ordered by the
// object number of the signature dictionary, which is stable across runs (the
// objects are held in a map, whose iteration order is not) and meaningful: in a
// document signed by successive incremental updates the later signature is the
// later object.
func verifySignatures(d core.View, file core.SignedFile, opts VerifyOptions) []Result {
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
			r.tsTok = tok
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
				r.tsTok = tok
			}
		}
	}

	m := revocationMaterial{certs: dssCerts(d)}
	m.crls, m.ocsps = dssRevocationMaterial(d)
	trustTimestamps(results, m, opts.tsaRoots(), now)
	for i := range results {
		r := &results[i]
		if r.Valid && !r.DocTimestamp {
			r.ValidationTime = validationTime(r, results, now)
			establishTrust(r, m, opts)
		}
	}
	judgeChanges(file, ends, results)
	return results
}

// judgeChanges establishes, for every valid signature and time-stamp, whether
// the changes made after it are permitted. A signature added later is a
// permitted change only when it is intact itself (see Result.ChangesAllowed),
// so the results are judged from the latest signed revision back: by the time
// a signature is judged, every signature over a later revision has been.
func judgeChanges(file core.SignedFile, ends []int64, results []Result) {
	byNum := make(map[int]int, len(results))
	var order []int
	for i := range results {
		byNum[results[i].num] = i
		if results[i].Valid {
			order = append(order, i)
		} else {
			results[i].DisallowedChanges = nil
		}
	}
	// latest ranks a result by the revision it covers; one covering the whole
	// file is the latest of all.
	latest := func(r *Result) int {
		if r.CoversWholeDocument {
			return math.MaxInt
		}
		return r.Revision
	}
	sort.SliceStable(order, func(a, b int) bool { return latest(&results[order[a]]) > latest(&results[order[b]]) })
	for _, i := range order {
		r := &results[i]
		// vouch says whether the signature dictionary num, added after r,
		// holds an intact signature, or why not. A signature not yet judged —
		// one covering no later revision than r, r itself included — still
		// has ChangesAllowed false, so it vouches for nothing.
		vouch := func(num int) error {
			j, ok := byNum[num]
			if !ok {
				return errors.New("is not a signature dictionary this verifier reads")
			}
			switch s := &results[j]; {
			case !s.Valid:
				return errors.New("does not verify")
			case !s.ChangesAllowed:
				return errors.New("is not intact: it covers no later revision, or a change made after it is not permitted")
			}
			return nil
		}
		var visible []int
		r.ChangesAllowed, r.DisallowedChanges, visible = changesAfter(file, ends, r, vouch)
		for _, num := range visible {
			r.LaterVisibleSignatures = append(r.LaterVisibleSignatures, results[byNum[num]].Field)
		}
	}
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

// establishTrust builds the signer's chain at the validation time and checks
// the revocation status of every certificate on it but the trust anchor
// (RFC 5280 6.1.3): a chain through a revoked intermediate is not trusted, and
// its revocation is reported.
func establishTrust(r *Result, m revocationMaterial, opts VerifyOptions) {
	cert := r.sd.cert
	pool := append(append([]*x509.Certificate(nil), r.sd.certs...), m.certs...)
	var issuers []*x509.Certificate
	// With nil roots signerChains builds nothing and says so; the error is
	// kept, so ChainErr is never nil for a signature that is not trusted.
	chains, err := signerChains(cert, pool, opts.Roots, r.ValidationTime)
	if err == nil {
		var revoked RevocationInfo
		chains, revoked, err = unrevokedChains(chains, m, r.ValidationTime)
		if err != nil {
			r.Revocation = revoked
		}
	}
	if err != nil {
		r.ChainErr = err
		if r.Revocation.Status == RevocationRevoked {
			return
		}
	} else {
		r.TrustedChain = true
		issuers = chainIssuers(chains)
	}
	if !r.TrustedChain {
		issuers = signingIssuers(cert, pool)
	}
	r.Revocation = leafRevocation(cert, issuers, m, r.ValidationTime)
}

// trustTimestamps decides which time-stamps are trusted: their authority
// chains to the time-stamp roots with the time-stamping purpose, and neither
// its certificate nor any intermediate is revoked. Each is judged at the
// earliest time a trusted document time-stamp covering it proves it existed —
// so an authority whose certificate has since expired stays trusted for the
// tokens an archive time-stamp preserved (the nested B-LTA case) — or now.
// Document time-stamps are settled from the outermost inwards, so every
// covering one is decided before those it covers; signature time-stamps last.
func trustTimestamps(results []Result, m revocationMaterial, roots *x509.CertPool, now time.Time) {
	var dts []int
	for i := range results {
		if results[i].DocTimestamp && results[i].tsTok != nil {
			dts = append(dts, i)
		}
	}
	sort.Slice(dts, func(a, b int) bool { return results[dts[a]].rg.gapStart > results[dts[b]].rg.gapStart })
	outer := func(end int64) time.Time {
		var best time.Time
		for _, j := range dts {
			o := &results[j]
			if o.TimestampTrusted && o.rg.gapStart >= end && (best.IsZero() || o.TimestampTime.Before(best)) {
				best = o.TimestampTime
			}
		}
		if best.IsZero() || best.After(now) {
			return now
		}
		return best
	}
	for _, i := range dts {
		r := &results[i]
		r.ValidationTime = outer(r.rg.end)
		r.TrustedChain, r.Revocation, r.ChainErr = tsaTrust(r.tsTok, m, roots, r.ValidationTime)
		r.TimestampTrusted = r.TrustedChain
	}
	for i := range results {
		r := &results[i]
		if r.DocTimestamp || r.tsTok == nil {
			continue
		}
		r.TimestampTrusted, _, _ = tsaTrust(r.tsTok, m, roots, outer(r.rg.end))
	}
}

// tsaTrust reports whether a time-stamp authority is trusted at time at, the
// status of its certificate, and why not.
func tsaTrust(tok *timestampToken, m revocationMaterial, roots *x509.CertPool, at time.Time) (bool, RevocationInfo, error) {
	pool := append(append([]*x509.Certificate(nil), tok.certs...), m.certs...)
	chains, err := tsaChains(tok.cert, pool, roots, at)
	if err != nil {
		return false, RevocationInfo{}, err
	}
	chains, revoked, err := unrevokedChains(chains, m, at)
	if err != nil {
		return false, revoked, err
	}
	info := leafRevocation(tok.cert, chainIssuers(chains), m, at)
	if info.Status == RevocationRevoked {
		return false, info, fmt.Errorf("the time-stamp authority certificate %q is revoked (%s, at %v)", tok.cert.Subject.CommonName, info.Source, info.RevokedAt)
	}
	return true, info, nil
}

// changesAfter judges the changes made to the file after the revision a valid
// signature covers. vouch says whether a signature dictionary added after it
// is an intact signature (see disallowedChanges). It also returns the
// signature dictionaries of the permitted later signatures a reader can see.
func changesAfter(file core.SignedFile, ends []int64, r *Result, vouch func(sigNum int) error) (bool, []string, []int) {
	if r.CoversWholeDocument {
		return true, nil, nil
	}
	if r.Revision < 0 {
		return false, []string{"the signed bytes do not end at a revision of the file"}, nil
	}
	var bad []string
	if n := len(ends); n > 0 && ends[n-1] < file.Len() {
		if msg := trailingBytes(file, ends[n-1]); msg != "" {
			bad = append(bad, msg)
		}
	}
	diff, err := file.Diff(r.Revision)
	if err != nil {
		return false, append(bad, "the changes after the signature could not be established: "+err.Error()), nil
	}
	disallowed, visible := disallowedChanges(diff, vouch)
	bad = append(bad, disallowed...)
	return len(bad) == 0, bad, visible
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
