# Digital signatures

pdf0 signs PDF documents with detached CMS/PKCS#7 signatures and verifies them
again, with no cgo and no network: signing (`WriteSigned` and friends), PAdES
baseline assessment (`ValidatePAdES`), RFC 3161 time-stamps, and CRL/OCSP
revocation from the document's own Document Security Store. Reach for it to
produce a signed PDF from a certificate and a `crypto.Signer`, or to decide
whether a PDF you received is the document that was signed, by the party you
expect. The verifier lives in the `sign` package (`sign/verify.go`,
`sign/cmsverify.go`, `sign/byterange.go`, `sign/trust.go`,
`sign/revocation.go`, `sign/changes.go`, `sign/pades.go`,
`sign/timestamp.go`); the writers are `sign.go`, `doctimestamp.go` and
`incremental.go` in the root package, and `signedfile.go` gives the verifier
the file's revisions — see [architecture.md](architecture.md) for how they sit
in the package.

## Which verdict do I read?

**Accept a signed PDF only when `VerifySignatures` returns at least one
approval signature, and *every* result it returns — each signature and each
document time-stamp — is `Intact()`, has `TrustedChain`, and is not revoked.**
Integrity (`Intact()`), identity (`TrustedChain`) and status (`Revocation`) are
separate claims, and none implies another; `r.Valid` on its own is not a safe
check.

- **No results means unsigned, not "no problems".** An unsigned PDF, or one
  whose signature was stripped, gives zero results and a nil error, so a loop
  over the results alone accepts it.
- **Trust comes *only* from the roots you pass.** With `sign.VerifyOptions{}`
  (nil roots) no chain is ever built: `TrustedChain` is always `false`, and
  `ChainErr` says `no trust roots were supplied`. Without roots, a signature
  from an entirely unknown signer looks exactly like one from your CA.
- **A document time-stamp is trusted against `TSARoots`** (`Roots` when that
  is nil). Set it to the time-stamp authorities you accept, or every B-LTA
  file whose time-stamp comes from a TSA outside `Roots` fails the check.
- **Check every result, not only the field you care about.** A signature added
  after yours keeps yours `Intact()` only if that later signature is itself
  intact, but its widget can draw anything over the page you signed — a
  second party's signature box, or a full-page overlay. Your result lists such
  signatures in `LaterVisibleSignatures`; whether you trust whoever made them
  is their own result's `TrustedChain`.

<!-- snippet
var caCert, tsaCACert *x509.Certificate
-->
```go
data, _ := os.ReadFile("signed.pdf")
doc, err := pdf0.Read(bytes.NewReader(data), int64(len(data)))
if err != nil {
	return err
}
roots := x509.NewCertPool() // the roots of the signers YOU accept
roots.AddCert(caCert)       // never x509.SystemCertPool(): see below
tsaRoots := x509.NewCertPool() // the time-stamp authorities you accept
tsaRoots.AddCert(tsaCACert)

results, err := doc.VerifySignatures(sign.VerifyOptions{Roots: roots, TSARoots: tsaRoots})
if err != nil {
	return err // verification could not run; not "no signatures"
}
approvals := 0
for _, r := range results { // EVERY result, document time-stamps included
	if !r.Intact() { // Valid AND every later change permitted
		return fmt.Errorf("%s does not hold for the document as delivered: %v %v", r.Field, r.Err, r.DisallowedChanges)
	}
	if !r.TrustedChain {
		if r.DocTimestamp { // judged against TSARoots
			return fmt.Errorf("time-stamp %s: authority not trusted: %w", r.Field, r.ChainErr)
		}
		return fmt.Errorf("signer of %s is not trusted: %w", r.Field, r.ChainErr)
	}
	if r.Revocation.Status == sign.RevocationRevoked {
		return fmt.Errorf("%s: certificate was revoked at %s", r.Field, r.Revocation.RevokedAt)
	}
	if !r.DocTimestamp {
		approvals++
	}
}
if approvals == 0 {
	return errors.New("the document is not signed") // no results, or time-stamps only
}
```

If your workflow expects one signer, or only invisible signatures, also
reject a result whose `LaterVisibleSignatures` is not empty: those later
signatures are trusted by the loop above, but they still draw on the page.

How each verdict is reached, and what feeds the gate:

```mermaid
flowchart TD
    V["VerifySignatures(opts)"] --> N{"any signature or<br/>document time-stamp dictionary?"}
    N -->|"no: 0 results, nil error"| UNSIGNED["unsigned: reject"]
    N -->|"yes, each one"| BR["byte range: one layout, gap is exactly /Contents,<br/>ends at a revision end or the file end"]
    BR --> COVER["CoversWholeDocument"]
    BR --> CMS["CMS signature, or RFC 3161 token for a time-stamp:<br/>digest of the covered bytes, no SHA-1 or MD5"]
    BR -->|"fails"| ERR["Err set, Valid false:<br/>nothing else is judged"]
    CMS -->|"fails"| ERR
    CMS --> VALID["Valid"]
    VALID --> UNMOD["DocumentUnmodified() = Valid and CoversWholeDocument"]
    COVER --> UNMOD
    VALID --> CHG["changes after the signed revision,<br/>judged from the latest signature back"]
    CHG --> ADDED{"a signature added later?"}
    ADDED -->|"its own result is Intact"| PERM["permitted, and listed in<br/>LaterVisibleSignatures if a reader can see its widget"]
    ADDED -->|"it is not"| DIS["not permitted"]
    CHG --> ALLOWED["ChangesAllowed / DisallowedChanges"]
    PERM --> ALLOWED
    DIS --> ALLOWED
    VALID --> INTACT["Intact() = Valid and ChangesAllowed"]
    ALLOWED --> INTACT
    VALID --> ROOTS{"roots given?<br/>Roots, or TSARoots for a time-stamp"}
    ROOTS -->|"no"| NOROOTS["TrustedChain false<br/>ChainErr: no trust roots were supplied"]
    ROOTS -->|"yes"| CHAIN["chain, purpose and validity at ValidationTime,<br/>no revoked intermediate"]
    CHAIN --> TC["TrustedChain, or ChainErr saying why not"]
    VALID --> REV["signer's own revocation, from the /DSS"]
    REV --> RS["Revocation.Status"]
    INTACT --> GATE["accept only if EVERY result passes all three,<br/>and at least one is an approval signature"]
    TC --> GATE
    RS --> GATE
```

`ValidatePAdES` carries the same `TrustedChain`, `Revocation` and
`LaterVisibleSignatures` for each approval signature, and none of them affects
its `Conformant` (see [PAdES](#pades)).

**Do not use `x509.SystemCertPool()` or any web PKI pool as signing roots.**
Those roots vouch for domain names: any of their CAs will issue a TLS
certificate to anyone who controls a domain. The purpose check below stops a
TLS-only certificate from counting as a signer, but the roots should still be
the ones that issue the signing certificates you accept.

Every field of `sign.Result`, and the exact limit of what it promises:

| Field | Promises | Does **not** promise |
| --- | --- | --- |
| `Valid bool` | the `/ByteRange` has the one layout a signature may have, the bytes inside it are intact, and the signature (or, for a document time-stamp, the RFC 3161 token) over them verifies under the embedded certificate | anything about bytes *outside* the range, or about who the signer is |
| `CoversWholeDocument bool` | the `/ByteRange` covers every byte of the file except the signature's own `/Contents` hex string | that the signature verifies |
| `DocumentUnmodified() bool` | `Valid && CoversWholeDocument` — nothing at all was changed after signing | trust, revocation or a reliable time |
| `ChangesAllowed bool` | every change made to the file after the signed revision is permitted (see [Allowed changes](#allowed-changes)), a later signature counting as permitted only when its own result is `Intact()`; true when there are none | that anything is trusted, including whoever made a later signature; false when the changes could not be established, and for a signature that is not `Valid` |
| `DisallowedChanges []string` | a description of each change that is not permitted, or why the changes are unknown | |
| `Intact() bool` | `Valid && ChangesAllowed` — the verdict for a document that has been through long-term-validation updates or later signatures | trust or revocation |
| `LaterVisibleSignatures []string` | the `Field` names of the later signatures `ChangesAllowed` permitted whose widget a reader can see: a `/Rect` of non-zero area, neither the Hidden nor the NoView flag, and a normal appearance (`/AP /N`). Empty when there are none, and for a signature that is not `Valid` | that those signatures are trusted (read their own results), or that what they draw is harmless |
| `Revision int` | the index into `Source().Revisions()` of the revision the signature covers; -1 when its range does not end at one | |
| `TrustedChain bool` | the certificate chains to one of `VerifyOptions.Roots` (`TSARoots` for a document time-stamp), valid at `ValidationTime`, with a document-signing purpose (time-stamping for a time-stamp authority), through no revoked intermediate (see [Trust](#trust)) | anything unless you passed roots; the signer certificate's own revocation — a revoked signer still has `TrustedChain`, so read `Revocation` (a revoked time-stamp authority is not trusted); never set for a signature that is not `Valid` |
| `ChainErr error` | why `TrustedChain` is not set for a `Valid` signature: the chain did not build, its purpose was refused, an intermediate is revoked, or no roots were given (`no trust roots were supplied`, or `no trust roots were supplied for time-stamp authorities`) | anything when it is nil: it is nil when `TrustedChain` is set, and for a signature that is not `Valid` (read `Err`) |
| `ValidationTime time.Time` | the time chain and revocation were judged at: the earliest time a verified, *trusted* time-stamp proves the signature existed, or now | |
| `SignerCommonName string` | the Subject CN of the embedded certificate — a display label, filled in even when verification fails | any identity assurance on its own |
| `SigningTime time.Time` | the `signing-time` signed attribute, if the CMS carries one | a trustworthy time: the signer asserts it and nothing verifies it. It is never used as the validation time |
| `TimestampTime time.Time` | the time a verified time-stamp asserts: a B-T signature's signature time-stamp, or a document time-stamp's own | a trustworthy time unless `TimestampTrusted` |
| `TimestampTrusted bool` | that time-stamp's authority chains to your time-stamp roots with the `id-kp-timeStamping` purpose | |
| `Revocation RevocationInfo` | status at `ValidationTime` from the CRLs/OCSP responses in the document's `/DSS`, authenticated by the certificate's real issuer; a revocation from any source wins; a revoked intermediate is reported here too | any network lookup |
| `DocTimestamp bool` | the result is a document time-stamp (`/Type /DocTimeStamp`), not an approval signature | |
| `Err error` | why verification failed, when it did | |
| `Field string` | the fully qualified name (ISO 32000-2 §12.7.4.2 — the `/T` chain joined with `.`) of the form field whose `/V` references this signature, e.g. `Signature1` | a name at all when no field references the signature dictionary; then it is empty |

A signature can be `Valid` and still describe a different document: an
incremental update appends a new value for an existing page after the signed
range, leaving the original bytes — and the signature over them — perfectly
intact. `Intact()` catches it and `DisallowedChanges` names the page;
`examples/sign_verify` demonstrates exactly this.

An update can also add a *signature*: that is how a second party signs, so it
is permitted — but only when the added signature is itself intact. A
"signature" field whose value does not verify, or whose appearance was
replaced after it was signed, is a change like any other and makes the earlier
signature fail `Intact()`. One that does verify may still be signed by anyone
and draw anything over the page (the "shadow attack" class); it is named in
the earlier result's `LaterVisibleSignatures` and judged by its own result,
which is why the gate requires *every* result to pass.

## How do I sign?

Sign with a certificate and any `crypto.Signer` (a key in memory, a PKCS#11
token, an HSM); pdf0 signs with SHA-256 over RSA or ECDSA. The first signature
rewrites the whole document; everything after it — a second signer, the
long-term validation material, an archival time-stamp — is *appended* to the
signed file, so earlier signatures keep verifying. Each step reads the
previous step's output back, because an append needs the file the document
was read from:

<!-- snippet
var cert, cert2, caCert, tsaCert *x509.Certificate
var key, key2, tsaKey crypto.Signer
var crls, ocsps [][]byte
-->
```go
// PAdES B-T: a signature, plus a signature time-stamp from a TSA key you
// hold. Leave out WithSignatureTimestamp for B-B.
var signed bytes.Buffer
if err := doc.WriteSigned(&signed, cert, key, pdf0.WithSignatureTimestamp(tsaCert, tsaKey)); err != nil {
	return err
}

// A second signer appends a signature. WriteSigned would refuse here with
// ErrAlreadySigned: rewriting the file would break the first signature.
first, err := pdf0.Read(bytes.NewReader(signed.Bytes()), int64(signed.Len()))
if err != nil {
	return err
}
var cosigned bytes.Buffer
if err := first.WriteSignedIncremental(&cosigned, cert2, key2); err != nil {
	return err
}

// PAdES B-LT and B-LTA: append what a verifier will need years from now —
// certificates, CRLs and OCSP responses (DER) you fetched yourself, since pdf0
// does no network I/O — and a document time-stamp over the whole file.
both, err := pdf0.Read(bytes.NewReader(cosigned.Bytes()), int64(cosigned.Len()))
if err != nil {
	return err
}
material := pdf0.ValidationData{Certs: []*x509.Certificate{caCert, tsaCert}, CRLs: crls, OCSPs: ocsps}
var archived bytes.Buffer
if err := both.WriteArchivalTimestamp(&archived, material, tsaCert, tsaKey); err != nil {
	return err
}
```

The time-stamp authority is local: the TSA certificate must carry
`id-kp-timeStamping`, and a verifier trusts its tokens only if it chains to
that verifier's `TSARoots`. Then verify the result as
[Which verdict do I read?](#which-verdict-do-i-read) shows;
`examples/sign_verify` is a runnable sign-then-verify program. What each
writer produces, and what it refuses, is under [Signing](#signing).

## Verifying

`VerifySignatures(opts)` and `ValidatePAdES(opts)` read the file the document
was read from — its source record, `Document.Source()` — never a byte slice
passed alongside it: a signature covers bytes, and bytes that are not the
document's own file would be verified against the wrong thing. A `Document`
built in memory has no file, and no signature in it verifies. The error they
return is non-nil only when verification could not run to completion (a
failure on a hostile file, recovered rather than crashing the caller); the
results are then nil and must not be read as "no signatures".

Every dictionary with `/ByteRange` and `/Contents` whose `/Type` is absent,
`/Sig` or `/DocTimeStamp` is verified, in this order:

1. **The byte range** (`sign/byterange.go`). Exactly one layout is accepted: four
   non-negative integers `[0 len1 start2 len2]`, both spans inside the file —
   each length compared against what remains after its start, never a sum
   against the size, so no value can overflow — and the gap between them
   exactly the dictionary's own `/Contents` hex string. The range must end at
   the end of a revision (its `%%EOF`, or the end-of-line after it) whose
   bytes hold the gap, or at the end of the file. Two dictionaries claiming the
   same range end are both refused: at most one of them can be valid.
2. **The digest** is computed by streaming the covered bytes from the file,
   never by gathering them into a buffer. Signatures are hashed in file order
   and share the hash of the prefix they have in common, so verifying a file
   with thousands of revisions costs about one pass over it.
3. **The CMS** (`sign/cmsverify.go`): one `SignerInfo`, signed attributes present, a
   `message-digest` equal to the digest, a `content-type` equal to the
   `eContentType` (`id-data`, and no encapsulated content, for a document
   signature), an ESS `signing-certificate`/`signing-certificate-v2` that — when
   present — binds the signer certificate under the hash algorithm it names,
   and a signature over the signed attributes under the algorithm and
   parameters the `SignerInfo` declares (RSASSA-PSS salt length and hash
   honoured). SHA-1 and MD5 are refused as digests.
   A document time-stamp is verified as what it is, an RFC 3161 token: its
   `eContentType` must be `id-ct-TSTInfo`, its authority certificate must carry
   `id-kp-timeStamping`, and its message imprint must be the digest of the
   covered bytes. A B-T signature's signature time-stamp is checked the same
   way over the signature value.
4. **Trust and revocation** at the validation time (see [Trust](#trust) and
   [Revocation](#revocation)).
5. **The changes after the signature** (see [Allowed changes](#allowed-changes)).

## Allowed changes

A signature that does not cover the whole file covers one of its revisions,
and every incremental update after it changes the document. The verifier
compares the signed revision with the file as it stands (`signedfile.go`,
`sign/changes.go`), the analysis ISO 32000-2 §12.8.2.2.2 describes, and
classifies every object that differs:

- **Archival additions are permitted** — ETSI EN 319 142-1, and ISO 32000-2
  Table 257, which says a DSS or document time-stamp update "shall not be
  considered as changes to the document" at any certification level: a
  Document Security Store with its `/Certs`, `/CRLs`, `/OCSPs` and `/VRI`, and
  a document time-stamp field with its signature dictionary and widget (a
  time-stamp widget may carry an appearance only if its `/Rect` has no area).
  The time-stamp must itself be intact, exactly as an added signature must
  (below).
- **Signing is permitted unless a certification signature forbids it**: a new
  signature field with its widget and signature dictionary, or a signature
  value in a field the signed revision left empty, is permitted — the ordinary
  multi-signer workflow, which ISO 32000-2 §12.8.2.2 leaves unrestricted when
  no DocMDP certification applies and which ETSI EN 319 142-1 and the
  reference validators accept. It is refused when the signed revision's
  catalog `/Perms /DocMDP` names a certification signature with `P` 1, and
  signing an existing field is refused when an earlier signature's FieldMDP
  transform (`/TransformMethod /FieldMDP`, `/Action` `/All`, `/Include` or
  `/Exclude` over `/Fields`) locks that field.
  **The added signature must itself be intact**: its own result `Valid`, and
  every change after it permitted. Its presence is not enough. A field whose
  value does not verify, whose value is not a signature dictionary the
  verifier reads, or whose field or appearance changed after it was signed, is
  a change that is not permitted, and `DisallowedChanges` says why — e.g. "a
  signature field added after the signature, whose own signature does not
  verify". To judge this, the results are settled from the latest signed
  revision back. A permitted later signature whose widget a reader can see is
  listed in `LaterVisibleSignatures`: its appearance draws over the signed
  page, and nothing limits what it draws.
- **Edits to existing objects are permitted only as far as attaching those
  requires**: the catalog may change `/DSS` and `/AcroForm`; the interactive
  form may gain the new fields at the end of `/Fields` and the signature bits
  in `/SigFlags`; a page may gain the new widgets at the end of `/Annots`; a
  DSS dictionary that was already one may change. An object whose before and
  after differ in any other key is not permitted.
- **Everything else is not permitted** and is described in
  `DisallowedChanges`: a changed page, an added object nothing archival
  refers to, a deleted object, a changed trailer `/Root`, `/Encrypt` or
  `/Info`, bytes after the last `%%EOF` that belong to no revision.

**A time-stamp never makes a change permitted.** It proves when bytes existed:
a public time-stamp authority stamps any hash it is sent. An earlier version
treated a document time-stamp covering the whole file as excusing every change
before it, which let a page changed after signing come back as a conformant
PAdES signature (audit 2026-09-22 C1).

The comparison reads both states from the file's bytes, not from
`Document.Objects`: the signed state is what a reader of the file truncated at
the signed revision sees, so an update appended without a `%%EOF` of its own,
or an object changed by redefining the object stream that holds it, is still a
change. Objects are compared as the file stores them — for an encrypted file,
enciphered — so a change there is judged on its structure. The work is budgeted
per verification; past the budget the changes are reported unknown, never
permitted.

**Known gaps, reported rather than guessed:** DocMDP `P` 2 and 3 also permit
form filling, page templates and (at 3) annotation changes; recognising those
faithfully means judging each field type's value and appearance, which is not
done, so such changes are reported as not permitted.

## Trust

Chains are built with `crypto/x509` to `VerifyOptions.Roots`, using the CMS's
certificates and the DSS `/Certs` as intermediates, at the validation time.
A chain to a trusted root is not enough — the certificate must be for signing
documents (audit 2026-09-22 C22):

- the signer's key usage must include `digitalSignature` or
  `contentCommitment`, and a certificate with no key-usage extension is
  refused;
- the signer's extended key usage, and every intermediate's, must be absent
  or permit document signing: `anyExtendedKeyUsage`, `emailProtection`,
  `id-kp-documentSigning` (1.3.6.1.5.5.7.3.36), Adobe Authentic Documents
  Trust (1.2.840.113583.1.1.5) or Microsoft Document Signing
  (1.3.6.1.4.1.311.10.3.12). A TLS certificate (`serverAuth`) is refused.

A time-stamp authority must carry `id-kp-timeStamping` and chain to
`VerifyOptions.TSARoots` (or `Roots` when that is nil). A self-signed
authority not among the roots is checked cryptographically but not trusted,
and its time is not used.

The **validation time** is the earliest time a verified, trusted time-stamp
proves the signature existed — its own signature time-stamp, or a document
time-stamp over a later revision — and otherwise the current time. The
signer's own `signing-time` attribute is never used: the signer asserts it, and
a holder of an expired or revoked certificate could backdate it.

A time-stamp authority's own chain is judged the same way: at the earliest
time a trusted document time-stamp covering the token proves it existed, or
now. Document time-stamps are settled from the outermost inwards, so an
authority whose certificate has since expired stays trusted for the tokens a
later, trusted archive time-stamp preserved — the nested B-LTA case — and the
chain of trust ends at the newest time-stamp, judged now.

## Signing

None of the writers mutates the in-memory `*Document`; they work on a clone.
Each refuses an encrypted document, with one of two errors (`<op>` is
`signing`, or `timestamp` for `WriteArchivalTimestamp`):

<!-- messages -->
```
pdf0: <op>: the document is encrypted and was not decrypted (Locked); …   # read without its password
pdf0: <op>: cannot sign an encrypted document      # decrypted on Read, or marked by SetEncryption
```

pdf0 cannot produce an encrypted signed file. Sign the plaintext document, and
do not encrypt it afterwards: `SetEncryption` then `Write` succeeds, but it
rewrites the bytes the signature covers, and the signature no longer verifies.

| Call | Produces | Level |
| --- | --- | --- |
| `WriteSigned(w, cert, key, opts...)` | full serialization plus a signature field, `/SubFilter /ETSI.CAdES.detached`, SHA-256 detached CMS with `content-type`, `message-digest` and `signing-certificate-v2` attributes | PAdES B-B |
| … with `WithSignatureTimestamp(tsaCert, tsaKey)` | the same, plus an RFC 3161 signature time-stamp over the signature value as an unsigned attribute, issued in-process by the supplied TSA key | PAdES B-T |
| `WriteSignedIncremental(w, cert, key, opts...)` | the file the document was read from verbatim, then only the appended signature objects and a new xref section chaining back via `/Prev`; takes `WithSignatureTimestamp` too | PAdES B-B / B-T |
| `WriteArchivalTimestamp(w, ValidationData{Certs, CRLs, OCSPs}, tsaCert, tsaKey)` | an incremental update of the file the document was read from, adding the certificates, CRLs and OCSP responses to the catalog `/DSS` and a `/Type /DocTimeStamp` field whose RFC 3161 token covers the whole file | PAdES B-LT / B-LTA, on top of an existing B-T |

**`WriteSigned` refuses a document that already carries a signature**, with
`ErrAlreadySigned`. It rewrites the whole file — new offsets, new xref,
possibly different object-stream packing — which moves every byte an existing
signature committed to and invalidates it. Add a signature to a signed
document with `WriteSignedIncremental`; pass `InvalidatingExistingSignatures()`
only to replace the file and discard the old signatures on purpose.

Adding to a signed file is always an *append*: `WriteSignedIncremental` and
`WriteArchivalTimestamp` copy the bytes the document was read from (its source
record, `Document.Source`) out untouched and add a revision behind them; the
update can even be undone by truncation. The new objects are numbered above
every number that file uses, including its object streams and
cross-reference streams, so the update never redefines one of them.

**An existing DSS is extended, never replaced.** `WriteArchivalTimestamp` keeps
every certificate, CRL, OCSP response and `/VRI` entry the DSS already holds,
and does not add material it already holds byte for byte, so renewing a B-LTA
document's archival time-stamp keeps every earlier piece of revocation
evidence. The update touches only what the allowed-changes analysis accepts as
archival, so the signatures before it stay `Intact`.

**Only a locally held TSA key is supported.** pdf0 issues time-stamp tokens
in-process from a certificate and key you hold (`WithSignatureTimestamp`,
`WriteArchivalTimestamp`). It has no RFC 3161 HTTP client and no way to accept
a token obtained elsewhere, so a commercial time-stamp authority you reach over
the network cannot be used. It fetches no CRLs or OCSP responses either —
`ValidationData` is yours to fill.

How a signature is written: a placeholder first, then the real offsets and
the signature patched into the serialized bytes, since neither can be known
before the bytes exist (`sign.go`, `doctimestamp.go`):

```mermaid
sequenceDiagram
    participant C as caller
    participant D as Document
    participant B as serialized bytes
    participant S as sign package
    C->>D: WriteSigned(w, cert, key, opts)
    Note over D: refuse an encrypted document,<br/>and ErrAlreadySigned
    D->>D: on a clone, add a /Sig dictionary: /ByteRange placeholder,<br/>/Contents of 8192 zero bytes<br/>add the field, a zero-area widget on the first page,<br/>and extend /AcroForm
    D->>B: Write, a full rewrite
    B->>B: find the last /ByteRange placeholder,<br/>then the /Contents after it
    B->>B: patch the real /ByteRange in place: all bytes<br/>except the /Contents hex string
    B->>S: the covered bytes
    S-->>B: detached CMS over their SHA-256 digest<br/>(plus a signature time-stamp with WithSignatureTimestamp)
    B->>B: write the CMS as hex into /Contents, zero-padded,<br/>or fail if it exceeds the reserved space
    B-->>C: the signed file, written to w
    C->>D: Read it back, then WriteSignedIncremental
    D->>B: the file it was read from, byte for byte, then only<br/>the new objects and an xref section chaining back with /Prev
    Note over B,S: the same patch steps, for the new placeholder only
    C->>D: Read it back, then WriteArchivalTimestamp
    D->>B: the file it was read from, then the extended /DSS<br/>and a /Type /DocTimeStamp field, its placeholders patched alike
    B->>S: the covered bytes
    S-->>B: an RFC 3161 token over them, from the local TSA key
    B-->>C: the token in /Contents, the file written to w
```

## PAdES

`ValidatePAdES(opts)` assesses each approval signature (document time-stamps
count as long-term material) and returns a `PAdESResult`. **`Conformant` is
about form, not trust: a revoked or untrusted signer can be `Conformant` with
`TrustedChain` set.** Accept a signature on `Conformant`, `TrustedChain` and a
`Revocation.Status` that is not `sign.RevocationRevoked` together, and apply
the gate in [Which verdict do I read?](#which-verdict-do-i-read) to the
document as a whole — `ValidatePAdES` returns nothing for an unsigned file
either.

`Conformant` means the `Issues` list is empty: sub-filter
`ETSI.CAdES.detached`, the CMS verifies, no `/Cert` in the signature dictionary
(the certificate belongs in the CMS), a CAdES signing-certificate attribute is
present, the signature time-stamp (if any) verifies, a B-LTA document
time-stamp after the signature verifies, and **every change after the
signature is permitted** — a later signature counting as permitted only if it
is itself intact. ETSI EN 319 142-1 defines the levels by the signature's form
and the material it carries; whether the signer is trusted and unrevoked is
validation (ETSI EN 319 102-1), reported beside it and never folded into it:

| Field | Meaning |
| --- | --- |
| `Field` | the signature field's fully qualified name, as `sign.Result.Field` |
| `SubFilter`, `IsPAdES` | the `/SubFilter`, and whether it is the PAdES one, `ETSI.CAdES.detached` |
| `Level` | the baseline level whose material is present (B-B to B-LTA, below) |
| `Conformant`, `Issues` | conformance to that level, and each problem found |
| `Valid`, `CoversDocument`, `ChangesAllowed`, `DisallowedChanges` | as `sign.Result`'s `Valid`, `CoversWholeDocument`, `ChangesAllowed` and `DisallowedChanges` |
| `LaterVisibleSignatures` | as `sign.Result`'s: later signatures whose widgets draw over the page. They do not affect `Conformant`; who signed each is that signature's own `VerifySignatures` result |
| `SignerCommonName` | a display label, as `sign.Result`'s |
| `TrustedChain` | the signer chains to `Roots` for document signing, at the validation time, through no revoked intermediate. **It does not include the signer's own revocation** |
| `Revocation` | the signer certificate's status from the document's `/DSS`, as `sign.Result.Revocation`. A revoked signer is reported here and changes neither `TrustedChain` nor `Conformant` |
| `TimestampValid`, `TimestampTrusted`, `TimestampTime` | the signature time-stamp verifies; its authority chains to your time-stamp roots; the time it asserts |

```mermaid
flowchart LR
    BB["B-B<br/>CAdES-BES signature"] -->|signature time-stamp<br/>unsigned attribute| BT["B-T"]
    BT -->|catalog /DSS| BLT["B-LT"]
    BLT -->|/Type /DocTimeStamp<br/>in a later revision| BLTA["B-LTA"]
```

Each level requires the previous one: a `/DSS` without a signature time-stamp
still reports B-B. The DSS is where a long-term signature keeps what a verifier
will need years later — `/Certs`, `/CRLs`, `/OCSPs` — since the issuing CA's
responders will not answer forever; the document time-stamp archives it.

A B-LT or B-LTA signature never covers the whole file — the validation material
is added after it — so `CoversDocument` is false for it, and it is conformant
because `ChangesAllowed` is true, not because a time-stamp covers the rest.

## Revocation

`sign.CheckCertRevocation(cert, issuer, crls, ocsps, at)` returns a
`RevocationInfo` (`Status` — `sign.RevocationUnknown`, `sign.RevocationGood` or `sign.RevocationRevoked` — plus
`Source`, `"OCSP"` or `"CRL"`, and `RevokedAt`) for the time `at`. Inside
`VerifySignatures` the issuer is the next certificate of the verified chain
(without roots, a certificate whose key verifiably issued the signer's), never
a certificate found by name: a forged CA carrying the real one's name decides
nothing (audit 2026-09-22 C2). A source counts only if that issuer signed it —
or, for OCSP, a responder certificate the issuer issued with the OCSP-signing
EKU, valid at `at`.

Every source is read, and **a revocation from any authenticated source wins**
over a "good" from another; a revocation needs no freshness, since it is
permanent. A "good" counts only from a source current at the validation time:
`thisUpdate ≤ at ≤ nextUpdate`, with five minutes of clock skew. A CRL without
`nextUpdate` is non-conforming (RFC 5280 §5.1.2.5) and never current; an OCSP
response without one is current only at the moment it was produced (RFC 6960
§4.2.2.1 — "newer information is always available"). Compare `RevokedAt` with
`ValidationTime` when a signature made before a revocation matters.

`Document.DSSCerts()` and `Document.DSSRevocationMaterial()` read `/Certs` and
`/CRLs` + `/OCSPs` out of the catalog's `/DSS`, decoding the streams. **Nothing
is ever fetched from the network** — not OCSP, not CRL distribution points, not
AIA issuer certificates. No DSS material, or no authenticated issuer, yields
`RevocationUnknown`.

**Revocation covers the whole path** (RFC 5280 §6.1.3): every certificate
between the signer and the trust anchor is checked, each against the next
certificate of the verified chain, at the validation time. A revoked
intermediate makes the chain untrusted — `TrustedChain` false, `ChainErr`
naming it, and `Revocation` reporting that revocation. A revoked *signer*
certificate is different: it is reported in `Revocation` only, and
`TrustedChain` stays set, so a gate must read both. A time-stamp authority is
judged more strictly: a revoked authority certificate or intermediate makes
its time-stamps untrusted, whatever the time-stamp's own date — a
deliberately conservative reading, since the revocation reason (key
compromise or not) is not weighed. For live revocation, fetch the material
yourself and call `sign.CheckCertRevocation` directly.

## Limitations and edge cases

- **Result order is by object number.** Both `VerifySignatures` and
  `ValidatePAdES` sort the signature dictionaries by object number, which is
  stable across runs and meaningful: in a document signed by successive
  incremental updates the later signature is the later object. `Field` names each
  result, so results can also be matched by field name.
- **Level detection is presence-based.** The catalog's `/DSS` only has to resolve
  to a dictionary, and a `/DocTimeStamp` only has to lie in a revision after the
  signature. A reported `B-LTA` means "the material is there" —
  `TimestampValid` and `Conformant` say whether it verifies.
- **The signature must fit 8192 bytes** of DER: the CMS, holding the signer's
  certificate (the only one embedded) and any signature time-stamp token. The
  reservation is fixed: a bigger CMS overflows the `/Contents` placeholder and
  signing fails with `signing: signature (N hex) exceeds reserved space (16384)`
  (`timestamp:` for a document time-stamp).
- **Algorithms.** Signing supports RSA and ECDSA keys, always with SHA-256.
  Verification accepts RSA PKCS#1 v1.5, RSASSA-PSS (with the salt length and
  hash its parameters declare; an MGF1 hash different from the message hash is
  refused) and ECDSA over SHA-256/384/512, and rejects SHA-1/MD5 digests,
  multiple `SignerInfo`s, and signatures without signed attributes.
- **The signature field is added to the existing form, and named for the first
  free `SignatureN`.** An `/AcroForm` already in the catalog is updated in place:
  the new field is appended to its `/Fields`, the signature bits are OR-ed into
  `/SigFlags` (Table 225: bit 1 `SignaturesExist`, bit 2 `AppendOnly`), and every
  other key — `/DA`, `/DR`, `/NeedAppearances`, `/Q` — is kept, so neither an
  earlier signature's field nor an ordinary form field is dropped. The name scan
  covers the whole field tree plus field-like dictionaries the tree does not
  reach, so the first signature of a fresh document is `Signature1`, the second
  `Signature2`, and no two fields share a fully qualified name. Only a document
  with no `/AcroForm` at all gets a new form object. `WriteArchivalTimestamp`
  treats the form identically for its `/Type /DocTimeStamp` field, and names it
  from the same scan with a `TimestampN` prefix: the first document time-stamp is
  `Timestamp1`, a second `Timestamp2`. The two counters are independent — a
  time-stamp added to a signed document is still `Timestamp1` — but neither ever
  reuses a name already in the file.
- **The widget goes on the first page**, with a zero `/Rect`: signatures produced
  here are invisible, and never appear in an earlier signature's
  `LaterVisibleSignatures`. "First" means first in reading order, the page `PageList`
  reports first, found by descending into intermediate `/Pages` nodes — a tree
  whose pages all sit below one is signable, and a `/Kids` array mixing
  intermediate nodes and leaves (legal under ISO 32000-2 §7.7.3.2) does not
  confuse it. The widget's `/P` is the indirect reference to that same page
  object (Table 166), taken from the object number the writer updates, so the
  annotation and its `/P` cannot name different objects.
- **A direct `/AcroForm` is promoted; a direct catalog or page is refused.**
  Storing the interactive form as a direct dictionary in the catalog is legal, so
  it is copied into a new indirect object that the (rewritten) catalog then
  points at — it cannot be updated in place, having no object number of its own.
  The catalog and the first page are different: signing rewrites both, and ISO
  32000-2 §7.5.5 (trailer `/Root`) and §7.7.3.2 (page-tree `/Kids`) require both
  to be indirect references, so a document where either is direct is malformed
  and every writer refuses it (`the document catalog is a direct object …`, `the
  first page is a direct object …`) rather than silently promoting a broken
  structure — an incremental update cannot supersede an object that does not
  exist.
- **Encrypted documents** cannot be signed or incrementally updated at all
  (see [Signing](#signing) for the two refusals), and encrypting a signed
  document invalidates its signatures.

## For contributors

The signature placeholder is found by anchoring on `/ByteRange`, not on
`/Contents` — worth knowing if you touch `patchSignature` or
`patchDocTimestamp`. A page's `/Contents 4 0 R` precedes the signature
dictionary in essentially every real document, and an earlier signature's
`/Contents` is a filled hex blob, so the first `/Contents` in the file is never
the right target. `findSigSlots` locates the unique, still-unfilled
`/ByteRange` placeholder — which a filled signature no longer carries — and
searches forward from there. Signing a document with page content, and adding a
second signature to an already-signed file, are both covered by regression
tests in `sign_contents_test.go`; the later-signature rules of
[Allowed changes](#allowed-changes) by `sign_overlay_test.go`.

## See also

`examples/sign_verify` (a runnable sign-then-verify guard),
[architecture.md](architecture.md) (the read/write pipeline the signer sits on),
[validators.md](validators.md) (the other validators) and
[../README.md](../README.md) (the rest of the API).
