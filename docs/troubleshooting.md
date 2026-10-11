# Troubleshooting

Organised by the symptom you actually have. Every message quoted in a block
below is a real string emitted by the library or the `pdf0` CLI, and every
quotation of a godoc is the godoc as it is: `TestDocQuotedMessagesExist`
(`internal/lint`) fails when one is not.

## `Read` returned an error

Most defects are *recovered*, not fatal — see the recovery ladder in
[docs/architecture.md](architecture.md#the-recovery-ladder). A wrong stream
`/Length` falls back to searching for `endstream`; an offset-shifted
cross-reference is probed absolute-vs-header-relative; a damaged cross-reference
table is reparsed from the nearest preceding `xref` keyword and, failing that,
rebuilt by scanning the file for `N G obj` headers; a missing `/Root` in a
rebuilt document is synthesized from the lowest-numbered `/Type /Catalog`
object, one inside an object stream included. So an error out of `Read` means
the ladder was exhausted, or the call itself was refused: **the file is
genuinely unusable, not merely malformed** — or one of the first three rows
below. In practice you will see one of:

| Message | Meaning |
|---|---|
| `pdf0: invalid option: WithMax…(0): a limit must be positive; …` | An option value of zero or less; nothing was read. Pass a positive value, or the type's maximum for no practical limit (`ErrInvalidOption`). |
| `reading PDF: context deadline exceeded` (or `… context canceled`, or another step named in place of `reading PDF`) | The context of `ReadContext` ended. No `Document` comes back with it; read again with a longer deadline. |
| `short read: …`, `invalid input size …`, `reading input: …` | The size you passed is wrong, or `r` failed; see [below](#short-read-got-n-of-m-bytes). |
| `PDF header not found` | No `%PDF-` marker in the first 1024 bytes. Not a PDF, or the wrong bytes were passed. |
| `startxref not found` / `no offset after startxref` / `invalid startxref offset: …` / `startxref offset N outside file (size M)` | The `startxref` pointer is missing, unparseable or past the end of the file — often a truncated download — *and* the scan that rebuilds the table found no object header anywhere in the file. |
| `parsing object N at offset M: …` / `object N xref offset M outside file (size S)` | The file's own table names an object that is not where it says, *and* the scan-rebuild retry found no object header. |
| `rebuilt cross-reference table found no document catalog` | The scan-rebuild ran and recovered objects, but none is a `/Type /Catalog`, compressed objects included, so there is no document to hand back. When the rebuild was caused by a broken cross-reference section, that section's error is returned instead. |
| `recovered from panic while reading PDF: …` | A bug. `Read` converts panics into errors so a hostile file cannot crash your process, but please report it (and see [testing.md](testing.md#fuzzing) — the fuzzers exist for exactly this). |

An object stream whose index disagrees with the cross-reference table about
which object it holds also fails the read, by design
([ADR 0015](adr/0015-objstm-index-mismatch-is-fatal.md)). An `/Encrypt`
dictionary never does: see [Encrypted files](#encrypted-files).

**`Read` succeeded, but the file was damaged.** A nil error means the file was
parsed, not that it is well formed. `doc.Source().Rebuilt()` reports a file
whose cross-reference table pdf0 had to rebuild by scanning; such a document
cannot take `WriteIncremental`, and a cross-reference stream pdf0 declined to
decode (over `WithMaxDecodedStreamBytes`, or in a filter it does not implement)
is reported by every validator as a `limit` finding, because objects only that
stream located may be missing. The validators report the file's defects.

## `short read: got N of M bytes`

`Read(r io.ReaderAt, size int64)` takes the size you claim the file is, and
refuses to proceed if `r` yields fewer bytes than that. The check is deliberate:
the unfilled tail of the buffer is zero bytes, which count as PDF whitespace and
would silently mask truncated input. When `r` can report its own length, the
claim is checked before anything is allocated:

<!-- messages -->
```
short read: got N of M bytes
short read: the source holds N of the M bytes claimed
invalid input size N
```

**What to do:** pass the real size. From a file, `fi, _ := f.Stat(); pdf0.Read(f,
fi.Size())`. From a `[]byte`, `pdf0.Read(bytes.NewReader(b), int64(len(b)))` —
never a hard-coded or estimated length. If the size is right, the file really is
truncated; re-fetch it. The same error comes from `syntax.NewLexerFromReaderAt` for the
same reason.

## Encrypted files

Two fields, and they mean different things:

- **`Document.Encrypted`** — the file *carried* an `/Encrypt` dictionary. It stays
  `true` on a file that was decrypted successfully.
- **`Document.Locked()`** — the file carries `/Encrypt` but has no usable
  security handler, and *the strings and streams are still ciphertext*.
  **`Document.LockReason()`** says why, wrapping one of `ErrWrongPassword`,
  `ErrEncryptionUnsupported` or `ErrEncryptionMalformed` (test with
  `errors.Is`); its text names the entry at fault. An `/Encrypt` dictionary
  never makes `Read` fail — a malformed one is a `Locked` document with
  `ErrEncryptionMalformed`, not an error.
- **`Document.DecryptFailures()`** — on a document that is *not* Locked, the
  objects whose content could not be decrypted: corrupt ciphertext, or a stream
  whose crypt filter pdf0 cannot apply (an embedded file under an unsupported
  `/EFF`, a `/Crypt` filter naming an undefined filter). Their bodies are empty,
  and `Write` refuses the document.

`Encrypted` alone does not tell a Locked document from a decrypted one (both
keep it `true`), so check `Locked()` before reading content, validating or
re-encrypting. On a Locked document the extractors (`ExtractText`,
`ImagesContext`, `StreamData` and the rest) return an error wrapping
`LockReason()`, `RemoveEncryption` is a no-op, `SetEncryption`, `Repair` and
the signing writers refuse, the validators skip every check that reads content
and say so with a `limit` finding (`not-decrypted`), and `Write` carries the
ciphertext unchanged under the preserved `/Encrypt` and `/ID`, and the file
layout is regenerated.

**Empty password vs `ReadWithPassword`.** `Read` tries the empty password, which
covers the common "owner-restricted, no user password" case. For anything else
use `ReadWithPassword(r, size, pw)`, which accepts either the user or the owner
password. A wrong password is *not* an error — the file parses structurally and
comes back `Locked()`. If you do not check, whatever you read from it is
ciphertext, and a validation of it is mostly skipped checks.

The CLI checks for you, and tells a missing password from a wrong one (the
password comes from `-password-file`, `PDF0_PASSWORD` or a terminal prompt,
never from the command line):

<!-- messages -->
```
FILE is encrypted and no password was supplied (give it with …)
could not decrypt FILE: the password is wrong (…)
could not decrypt FILE: …      # an unsupported or malformed /Encrypt: no password would help
FILE is encrypted; decrypt it before merging
```

**Why `Write` sometimes refuses.** `Write` does *not* refuse every encrypted
document. A document that was decrypted on `Read` is re-encrypted with the
retained key and round-trips. For a *Locked* document, `Write` carries the
ciphertext unchanged under the preserved `/Encrypt` and `/ID`, and the file
layout is regenerated. It returns nil, so a file you cannot decrypt is still
round-trippable rather than lost. That passthrough is sound only when the
encryption state is knowable and the whole object model survived, so it
refuses when either is not:

<!-- messages -->
```
cannot write encrypted document: its /Encrypt dictionary is unresolvable, so the encryption state is unknown
cannot write encrypted document: N object stream(s) failed to decode on read, so some objects are missing
cannot write encrypted document: N object stream(s) were not unpacked on read (a resource limit, an unsupported filter or undecrypted data), so some objects are missing
cannot write encrypted document: N object stream(s) failed to decode on read and M were not unpacked (…), so some objects are missing
```

The first would produce a file readers wrongly try to decrypt; the others would
silently drop the objects locked inside a container that was not unpacked.

A *decrypted* document is refused when `DecryptFailures` is non-empty — the
objects whose ciphertext did not decrypt on `Read` hold nothing:

<!-- messages -->
```
cannot write: object(s) … could not be decrypted on read, so their content is missing
```

It is also refused when a stream added since `Read` names a crypt filter the
security handler cannot apply — a `/Crypt` filter whose `/DecodeParms /Name`
the `/Encrypt` dictionary's `/CF` does not define. `DecryptFailures` is empty
then: nothing failed on read, the stream is yours. Fix the stream's
`/DecodeParms`, or drop the `/Crypt` filter so the document's own stream
filter applies:

<!-- messages -->
```
cannot write encrypted document: <object N>: the stream names a crypt filter the security handler cannot apply
```

**`SetEncryption` refusals.** It needs a non-empty user password (an empty one
opens the file for anyone, and `SetEncryption` grants every permission), and
refuses a password SASLprep prohibits — control characters, private-use or
Unicode-3.2-unassigned code points, mixed right-to-left and left-to-right text —
because no conforming reader could reproduce it. An empty *owner* password is
replaced by a random one. See [encryption.md](encryption.md#passwords). It also
refuses a signed document, whose signatures encryption would break: see
[Signatures](#signatures).

Two related refusals that are not about encryption:

<!-- messages -->
```
cannot write: N object stream(s) failed to decode on read, so some objects are missing
object number 0 is reserved and cannot be written
```

## Validation reported violations

A `pdfa.Violation` prints as `[LEVEL CLAUSE] object N: message`, e.g.

```
[PDF/A-4 6.2.10] object 12: font ... must be embedded
```

`PDF/A-4` is the level whose rules require this; `6.2.10` is the ISO 19005
clause; `object 12` is the offending object number (omitted entirely when the
violation is document-level). Look the clause up in the standard, or in the
veraPDF profiles — `go run -tags devtools ./internal/cmd/rulecoverage -v` prints
each rule's description.

**An empty result is not a conformance guarantee.** From the `ValidatePDFA`
godoc: *"An empty result means 'none of the implemented checks fired', not a
guarantee of full conformance: the validator covers a subset of ISO 19005."*

**The byte-level rules judge the file you read.** Rules like "no data after
`%%EOF`", the byte-level stream `/Length` check and the cross-reference table
layout need the file itself, not the object model. `ValidatePDFA` runs them on
the file the document was read from, which the `Document` keeps
(`Document.Source`), as `Read` found it — edits to the document since do not
change what they say. To judge the bytes of an edited document, write it and
read the result:

```go
var buf bytes.Buffer
if err := doc.Write(&buf); err != nil {
	return err
}
written, err := pdf0.Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
if err != nil {
	return err
}
errs := pdf0.ValidatePDFA(written, pdfa.PDFA4)
```

A document built in memory has no file at all: its result carries one checker
finding, **`[… limit] no file to check (no-source-file)`**, saying the byte-level
rules did not run.

**`[… internal] internal validator error: …`** means a check panicked and was
recovered so the rest could run. It is a bug in pdf0, not in your file.

**`[… limit] resource limit reached (…)`** is the same kind of finding: it says
pdf0 stopped short, not that your file is wrong. The message names the guard
that tripped and whether the bound was pdf0's default or one you configured. So
is **`[… limit] the run was cancelled before it finished (context-canceled)`**,
which is what a `…Context` variant returns when your deadline expired.

`IsCheckerFinding` is the predicate for both reserved identifiers — filter with
it before deciding whether a file is conformant, because a checker finding means
**unknown**, not *failed*:

```go
var real []pdf0.Violation
for _, e := range pdf0.ValidatePDFA(doc, pdfa.PDFA4) {
	if !pdf0.IsCheckerFinding(e) {
		real = append(real, e)
	}
}
```

Each checker finding names its guard in parentheses. A `limit` finding is not
on its own a sign of a hostile file: besides resource budgets, the guards
include coverage gaps that ordinary documents produce — `no-source-file`,
`not-decrypted`, `predefined-cmap`, `unsupported-filter`. What each means and
what to do about it is in
[untrusted-input.md](untrusted-input.md#reading-a-limit-finding); every guard
is in [limits.md](limits.md#the-inventory).

Finally: validating a `Locked()` document checks almost nothing — every check
that reads content is skipped, and the result carries a `limit` finding
(`not-decrypted`) saying so. Check `doc.Locked()` first.

## Validation is taking too long

Cost is set by the document, not by you: a 71 MB, 1256-page file takes about ten
seconds to validate, and a hostile one can sit at the resource ceilings for
longer. Two independent levers, and they answer different questions.

- **"This must not cost more than X."** Lower the limits at `Read` time —
  `pdf0.Read(r, size, pdf0.WithMaxDecodedStreamBytes(48<<20))` and the other
  `With*` options, or `WithMaxWork` for the run as a whole. Where an option's
  godoc gives the largest real value measured, stay above it; where it says
  none has been measured, lower it only against your own documents. The resolved values are stored on the `Document`, so every
  later validation and extraction inherits them, and a check they stop is
  reported as a `limit` finding saying the bound was yours.
- **"I need an answer within X seconds, whatever it costs."** Use the `…Context`
  variants: `ValidatePDFAContext`, `ReadContext`, `ExtractTextContext` and the
  rest. A cancelled validation returns the findings it had gathered plus a
  `limit` finding recording the cancellation, so it can never be mistaken for a
  clean result; `Read`, `Write` and the extractors return an error wrapping
  `ctx.Err()`. Cancellation takes effect within about 60 ms on that 71 MB file.

Both, with the generated tables of options and of `…Context` variants, are in
[untrusted-input.md](untrusted-input.md).

Abandoning the goroutine is not a third option — the work carries on burning a
core and holding its memory until it finishes by itself. That case is exactly
what the context variants exist for.

## Extraction returned an error

The extractors have no finding channel, so whatever left their result short is
an error, beside the partial result; the result is complete exactly when the
error is nil. Test it with `errors.Is` and `errors.As`, not by its text.

**`errors.Is(err, pdf0.ErrResourceLimit)`.** A resource limit stopped the
work: the run's work budget (`WithMaxWork`) or a decode size limit. The message
names the guard and says whether its bound was pdf0's default or yours, so
raise the option if it was yours and you trust the file:

<!-- messages -->
```
resource limit reached (<guard>): <what> exceeds the bound of N <unit> (<whose>)
resource limit reached (<guard>): <what> needs N <unit>, over the bound of M (<whose>)
```

`<whose>` is `pdf0's default` or `configured by the caller`.

**A `*PageTextError` from `ExtractText` or `ExtractTextContext`.** The page it
names is left out of the text, and the text of the others is returned, still
separated by form feeds. Its `Err` says why: a limit, as above; content pdf0
declined to read — the page's or a form's content stream, or a font's CMap,
over a size limit, in a filter pdf0 does not decode, or not decrypted; or a
recovered panic, which is a bug in pdf0. When the work budget runs out, one
more error covers every page after the one it stopped in:

<!-- messages -->
```
text of page N not extracted: <reason>
text of the pages after page N not extracted: <reason>
resource limit reached (<guard>): <what> was not decoded
not implemented (<guard>): <what> uses a filter pdf0 does not decode  # unsupported-filter
not decrypted (<guard>): <what> did not decrypt under the document's key  # not-decrypted
internal error in text extraction: …
```

`<what>` is the page's content, a form XObject's or a font's CMap, with its
object number when it has one. A stream that decodes but holds corrupt data is
not an error: the page contributes what could be read.

**`ImagesContext` yielded an error.** The iteration stopped before it reached
every image, and that last yield carries a zero image: the images before it are
all that were reached. The error wraps `ctx.Err()` for a context that ended,
matches `ErrResourceLimit` for a spent work budget, and wraps `LockReason()`
for a Locked document, which yields no images. `ExtractImagesContext` returns
the same error beside the images it reached. `ExtractImages` (deprecated) and
`Images` stop silently in the same cases, so prefer the `…Context` forms
wherever a missing image matters.

**An error wrapping `LockReason()`** from any extractor: the document is
Locked, so its content is ciphertext and nothing was extracted. See
[Encrypted files](#encrypted-files).

## `0 fix(es) applied` / violations remain after repair

`pdf0 repair` prints `FILE: N fix(es) applied, M violation(s) remain` and exits 1
when `M > 0`, with:

<!-- messages -->
```
M violation(s) remain after repair (run: pdf0 validate -level LEVEL FILE)
```

This is expected for most inputs. From `Repair`'s godoc:

> It never touches page content or fonts, so it cannot
> make a conformant document non-conformant.
>
> It is not a substitute for validation: run ValidatePDFA afterwards to see what
> remains (missing embedded fonts, device colour without an output intent, and
> the like need information Repair does not have).

So `Repair` removes what the validator forbids at the level you give it —
encryption, the additional-actions (`/AA`) entries that level forbids on the
catalog, pages, annotations and form fields — and synthesizes a missing `/ID`,
and will do nothing at all about an unembedded font, because embedding one
requires a font program it does not have. Zero fixes means none of those
document-level defects were present, not that the file is clean. It refuses a
Locked document and an unknown level with an error rather than changing
anything.

## Signatures

Each symptom leads with what to do. The full verdict rules, and a gate to copy,
are in [signing.md](signing.md#which-verdict-do-i-read).

**Your check accepted a document it should not have.** Gate on *every* result
`VerifySignatures` returns — `Intact()`, `TrustedChain`, and a `Revocation`
that is not `sign.RevocationRevoked` — and require at least one approval
signature (a result with `DocTimestamp` false). `sign.Result.Valid` means only
that the bytes *inside* the signed `/ByteRange` are intact and were signed by
the embedded certificate's key: it says nothing about bytes outside that range,
nor about who signed. From the godoc on `sign.Result.Intact`:

> Intact reports that the signature verifies and every change made after it
> is permitted (see ChangesAllowed). It is the verdict to read for a document
> that has been through long-term-validation updates. It says nothing about
> who signed: read TrustedChain and Revocation for that.

Checking only the one field you care about is not enough either: a signature
added later keeps an earlier one `Intact()` only if it is intact itself, but it
may be anyone's, and its widget can draw anything over the signed page. The
earlier result's `LaterVisibleSignatures` names such signatures; their own
results say who made them.

**`VerifySignatures` returned no results and a nil error.** The document is
unsigned — reject it. No results means "no signatures", not "no problems"; a
loop over the results alone accepts an unsigned or stripped file. (A non-nil
error is different: verification could not run, and the results are nil.)

**`TrustedChain` is false and `ChainErr` says `no trust roots were supplied`.**
Pass the roots of the signers you accept: `sign.VerifyOptions{Roots: roots}`.
There is no default store, and never use `x509.SystemCertPool()` — it vouches
for domain names, not for signers. Any other `ChainErr` says why the chain
failed: it did not build to your roots, the certificate is not for document
signing, or an intermediate is revoked. `ChainErr` is nil when `TrustedChain`
is set, and for a signature that is not `Valid` (read `Err`).

**A B-LTA document's time-stamp is untrusted** (the result with `DocTimestamp`
true has `TrustedChain` false, `ChainErr` naming its authority). Put the
time-stamp authorities you accept in `sign.VerifyOptions.TSARoots`. A document
time-stamp is judged against `TSARoots`, and against `Roots` only when
`TSARoots` is nil — so the signer roots alone reject a TSA outside them. Do not
add the TSA's root to `Roots` to get round it: every document-signing
certificate that root issues would then count as a trusted signer.

**`ValidatePAdES` says `Conformant` and `TrustedChain`, but the signer was
revoked.** Read `PAdESResult.Revocation`. `Conformant` is about the signature's
form, and `TrustedChain` covers the chain, its purpose and validity, and its
intermediates — not the signer's own revocation.

**`WriteSigned` returned `ErrAlreadySigned`.** The document already carries a
signature or document time-stamp, and rewriting the file would invalidate it.
Add your signature with `WriteSignedIncremental` on the document read from the
signed file; pass `InvalidatingExistingSignatures()` to `WriteSigned` only to
discard the old signatures on purpose:

<!-- messages -->
```
signing: the document already carries signatures, which rewriting it would invalidate; use WriteSignedIncremental to add a signature, or pass InvalidatingExistingSignatures
```

**Signing failed with "exceeds reserved space".** Sign with a smaller
certificate: the reservation for the signature's `/Contents` is a fixed 8192
bytes and cannot be raised. The CMS pdf0 writes holds the signature, the
signer's certificate (the only one it embeds) and, for B-T, the time-stamp
token with its authority's certificate, so an unusually large certificate is
what overflows it. (`<op>` is `signing`, or `timestamp` for a document
time-stamp.)

<!-- messages -->
```
<op>: signature (N hex) exceeds reserved space (M)
```

**Signing refused an encrypted document.** pdf0 cannot produce an encrypted
signed file: sign the plaintext document. A document read without its password
(Locked) gets the first message, one decrypted on `Read` or marked by
`SetEncryption` the second; `<op>` is `signing`, or `timestamp` for
`WriteArchivalTimestamp`:

<!-- messages -->
```
pdf0: <op>: the document is encrypted and was not decrypted (Locked); …
pdf0: <op>: cannot sign an encrypted document
```

**`Write`, `Save` or `SetEncryption` refused a signed document.** The file
the document was read from carries a signature or document time-stamp, and
every one of these rewrites the whole file — `Write` even with nothing
changed — which would break it. The error wraps
`ErrWouldInvalidateSignatures` (test with `errors.Is`), and nothing was
written or changed. To keep the signatures, append your changes with
`WriteIncremental` (and add signatures and time-stamps with
`WriteSignedIncremental` and `WriteArchivalTimestamp`), all of which leave the
signed bytes as they were. To discard them on purpose — to encrypt the
document, say — call `AllowInvalidatingSignatures()` on the document first;
the output then carries the old signature dictionaries, and none of them
verifies. An encrypted signed file cannot be updated incrementally at all,
even after `RemoveEncryption`, so for it the consent is the only way to
write:

<!-- messages -->
```
cannot write: the file the document was read from carries signatures or document time-stamps (N), which rewriting the whole file would invalidate; write the changes with WriteIncremental to keep them, or call AllowInvalidatingSignatures first to rewrite the file anyway
cannot write: the file the document was read from carries signatures or document time-stamps (N), which rewriting the whole file would invalidate; an encrypted file cannot be updated incrementally, so call AllowInvalidatingSignatures first to rewrite it anyway
cannot encrypt: the file the document was read from carries signatures or document time-stamps (N), which rewriting the whole file would invalidate; encryption cannot be added by an incremental update, so call AllowInvalidatingSignatures first to encrypt it anyway
incremental update of an encrypted file is not supported, even after RemoveEncryption: the update keeps the file's bytes, which stay encrypted; write the decrypted document whole with Write instead
```

The last is `WriteIncremental`, `WriteSignedIncremental` or
`WriteArchivalTimestamp` after `RemoveEncryption` on a document read from an
encrypted file. Earlier versions wrote the update, and every reader then took
the file's ciphertext for its content.

## An image came back with `Decoded=false`

`ExtractedImage.Decoded` reports whether `Image` holds pixels. When it is false,
`Encoded` holds the raw stream bytes and `Note` says why — so read `Note` first:

<!-- messages -->
```
JPEG decode failed: <err>
CCITTFaxDecode preceding filter chain could not be reversed (<reason>); the raw encoded bytes are provided
CCITTFaxDecode failed: <err>
JBIG2Decode preceding filter chain could not be reversed (<reason>); the raw encoded bytes are provided
JBIG2Decode not decoded (<err>); the raw encoded bytes are provided
JPXDecode not decoded; raw bytes provided
image data not decoded (<reason>); raw bytes provided
unsupported CCITT sample layout
unsupported JBIG2 sample layout
unsupported sample layout (colour space <cs>, <n> bpc)
internal error while decoding the image, which was not decoded: <panic>
```

The last is a recovered panic: a bug in pdf0, not in your file.

The `unsupported … sample layout` notes mean the codec decoded fine but the
colour space / bit-depth combination has no renderer — the samples are there,
just not turned into an `image.Image`. See [docs/images.md](images.md).

Unrelated but easy to hit: `ExtractImages` holds every decoded image in memory at
once, which is unbounded on a large scan document. Use the `ImagesContext`
iterator to keep at most one decoded image live, and to be told when it stopped
short.

## Round-trip surprises: the bytes changed

`Write` **regenerates** the file layout rather than preserving it. A file read
from a cross-reference stream is written back as one, with compressible objects
repacked into an `/ObjStm`; a traditional-table file is written with a table. The
object model round-trips faithfully (`DocumentEqual` holds), but object order,
which objects share a stream, and the exact byte offsets are all regenerated.
Byte-for-byte comparison of input and output will fail, and that is by design.

Consequences worth knowing:

- **`Write` refuses a signed document.** Regenerating the layout invalidates
  every signature over the original bytes, and so does encrypting it with
  `SetEncryption` first, so both refuse a document whose file carries one
  unless `AllowInvalidatingSignatures()` was called (see
  [Signatures](#signatures)).
- **To amend a file, use `WriteIncremental`.** It writes the bytes of the file
  the document was read from verbatim, followed by only the objects listed in
  changed, a new cross-reference section whose /Prev chains back to the file's
  newest one, and a new trailer. The original bytes are preserved exactly, so
  any signature over them stays valid and the update can be undone by
  truncation. The document must have been read from a file: a document built in
  memory is refused (`this document was built in memory, so use Write`), as is
  one whose cross-reference data was rebuilt by scanning, an encrypted one
  (`incremental update of an encrypted document is not supported`), one read
  from an encrypted file even after `RemoveEncryption`, and an empty
  change list (`incremental update with no changed objects`). Number new objects
  with `Add`, which never reuses a number the file uses.
- **`Read` normalizes structure.** It drops `/XRef` and `/ObjStm` objects and
  strips the xref-stream-only trailer keys, so a second `Read` of your output
  will not show them where the input did. Compare with `DocumentEqual`, not with
  `bytes.Equal`.
