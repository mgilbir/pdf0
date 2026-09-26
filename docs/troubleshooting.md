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
rebuilt document is synthesized from the first `/Type /Catalog` object. So an
error out of `Read` means the ladder was exhausted: **the file is genuinely
unusable, not merely malformed.** In practice you will see one of:

| Message | Meaning |
|---|---|
| `PDF header not found` | No `%PDF-` marker anywhere in the input. Not a PDF, or the wrong bytes were passed. |
| `startxref not found` / `no offset after startxref` / `invalid startxref offset: …` | The trailer's `startxref` pointer is missing or unparseable, and there is nothing to start the ladder from. |
| `startxref offset N outside file (size M)` | The pointer lands past the end of the file — usually a truncated download. |
| `rebuilt cross-reference table found no document catalog` | The scan-rebuild ran and recovered objects, but none is a `/Type /Catalog`, so there is no document to hand back. |
| `parsing object N at offset M: …` | An individual object failed to parse *and* the scan-rebuild retry also failed. |
| `recovered from panic while reading PDF: …` | A bug. `Read` converts panics into errors so a hostile file cannot crash your process, but please report it (and see [testing.md](testing.md#fuzzing) — the fuzzers exist for exactly this). |

## `short read: got N of M bytes`

`Read(r io.ReaderAt, size int64)` takes the size you claim the file is, and
refuses to proceed if `r` yields fewer bytes than that. The check is deliberate:
the unfilled tail of the buffer is zero bytes, which count as PDF whitespace and
would silently mask truncated input.

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

From the godoc on `Locked`:

> Encrypted alone does not distinguish this from a successfully decrypted file
> (both keep Encrypted true). Callers that intend to read content, validate,
> extract, or re-encrypt should check Locked first: on a locked document
> RemoveEncryption is a no-op, ExtractText and the validators see ciphertext,
> SetEncryption refuses, and Write writes the file back verbatim.

**Empty password vs `ReadWithPassword`.** `Read` tries the empty password, which
covers the common "owner-restricted, no user password" case. For anything else
use `ReadWithPassword(r, size, pw)`, which accepts either the user or the owner
password. A wrong password is *not* an error — the file parses structurally and
comes back `Locked()`. If you do not check, you will validate ciphertext and get
nonsense violations.

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
retained key and round-trips. A *locked* document's ciphertext is written back
unchanged under the preserved `/Encrypt` and `/ID` — the layout is regenerated,
the encrypted content is not touched, and `Write` returns nil — so a file you
cannot decrypt is still round-trippable rather than lost. That passthrough is
sound only when the encryption state is knowable and the whole object model
survived, so it refuses when either is not:

<!-- messages -->
```
cannot write encrypted document: its /Encrypt dictionary is unresolvable, so the encryption state is unknown
cannot write encrypted document: N object stream(s) failed to decode on read, so some objects are missing
cannot write encrypted document: N object stream(s) were not unpacked on read (a resource limit, an unsupported filter or undecrypted data), so some objects are missing
```

The first would produce a file readers wrongly try to decrypt; the others would
silently drop the objects locked inside a container that was not unpacked. A
*decrypted* document is refused when `DecryptFailures` is non-empty, and when a
stream added since `Read` names a crypt filter the handler does not define:

<!-- messages -->
```
cannot write: object(s) … could not be decrypted on read, so their content is missing
```

**`SetEncryption` refusals.** It needs a non-empty user password (an empty one
opens the file for anyone, and `SetEncryption` grants every permission), and
refuses a password SASLprep prohibits — control characters, private-use or
Unicode-3.2-unassigned code points, mixed right-to-left and left-to-right text —
because no conforming reader could reproduce it. An empty *owner* password is
replaced by a random one. See [encryption.md](encryption.md#passwords).

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

Neither fires on any file in the veraPDF corpus. If you see one, either the
input is adversarial or a bound needs revisiting — [limits.md](limits.md) says
which guard is which and how to raise it.

Finally: validating a `Locked()` document validates ciphertext. Check
`doc.Locked()` first.

## Validation is taking too long

Cost is set by the document, not by you: a 71 MB, 1256-page file takes about ten
seconds to validate, and a hostile one can sit at the resource ceilings for
longer. Two independent levers, and they answer different questions.

- **"This must not cost more than X."** Lower the limits at `Read` time —
  `pdf0.Read(r, size, pdf0.WithMaxDecodedStreamBytes(48<<20))` and the other
  `With*` options, keeping each above the largest real value its documentation
  gives. The resolved values are stored on the `Document`, so every later
  validation and extraction inherits them. See
  [architecture.md](architecture.md#resource-limits).
- **"I need an answer within X seconds, whatever it costs."** Use the `…Context`
  variants: `ValidatePDFAContext`, `ReadContext`, `ExtractTextContext` and the
  rest. A cancelled validation returns the findings it had gathered plus a
  `limit` finding recording the cancellation, so it can never be mistaken for a
  clean result; `Read`, `Write` and the extractors return an error wrapping
  `ctx.Err()`. Cancellation takes effect within about 60 ms on that 71 MB file.
  See [architecture.md](architecture.md#cancellation).

Abandoning the goroutine is not a third option — the work carries on burning a
core and holding its memory until it finishes by itself. That case is exactly
what the context variants exist for.

## `0 fix(es) applied` / violations remain after repair

`pdf0 repair` prints `FILE: N fix(es) applied, M violation(s) remain` and exits 1
when `M > 0`, with:

<!-- messages -->
```
M violation(s) remain after repair (run: pdf0 validate -level LEVEL FILE)
```

This is expected for most inputs. From `Repair`'s godoc:

> Repair never touches page content or fonts — it only removes forbidden
> document-level constructs — so it cannot make a conformant document
> non-conformant.
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

## A signature says `Valid` but you should not trust it yet

`sign.Result.Valid` means only that the bytes *inside* the signed
`/ByteRange` are intact and were signed by the embedded certificate's key. It
says nothing about bytes outside that range, and nothing about whether the
certificate is trustworthy. From the godoc on `sign.Result.Intact`:

> Intact reports that the signature verifies and every change made after it
> is permitted (see ChangesAllowed). It is the verdict to read for a document
> that has been through long-term-validation updates. It says nothing about
> who signed: read TrustedChain and Revocation for that.

Use `result.Intact()` (`Valid && ChangesAllowed`) as your baseline verdict, or
`result.DocumentUnmodified()` (`Valid && CoversWholeDocument`) when nothing may
have changed at all; `DisallowedChanges` names what changed. For trust,
`VerifySignatures` builds a chain only to the roots you pass in
`sign.VerifyOptions{Roots: …}` — with none, `TrustedChain` is always false —
and reports `TrustedChain` / `ChainErr` separately; they never affect `Valid`.
For long-term validation (PAdES B-T through B-LTA, timestamps, revocation) see
[docs/signing.md](signing.md).

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
once, which is unbounded on a large scan document. Use the `Images` iterator to
keep at most one decoded image live.

## Round-trip surprises: the bytes changed

`Write` **regenerates** the file layout rather than preserving it. A file read
from a cross-reference stream is written back as one, with compressible objects
repacked into an `/ObjStm`; a traditional-table file is written with a table. The
object model round-trips faithfully (`DocumentEqual` holds), but object order,
which objects share a stream, and the exact byte offsets are all regenerated.
Byte-for-byte comparison of input and output will fail, and that is by design.

Consequences worth knowing:

- **Do not re-`Write` a signed document.** Regenerating the layout invalidates
  every signature over the original bytes.
- **To amend a file, use `WriteIncremental`.** It writes the bytes of the file
  the document was read from verbatim, followed by only the objects listed in
  changed, a new cross-reference section whose /Prev chains back to the file's
  newest one, and a new trailer. The original bytes are preserved exactly, so
  any signature over them stays valid and the update can be undone by
  truncation. The document must have been read from a file: a document built in
  memory is refused (`this document was built in memory, so use Write`), as is
  one whose cross-reference data was rebuilt by scanning, an encrypted one
  (`incremental update of an encrypted document is not supported`) and an empty
  change list (`incremental update with no changed objects`). Number new objects
  with `Add`, which never reuses a number the file uses.
- **`Read` normalizes structure.** It drops `/XRef` and `/ObjStm` objects and
  strips the xref-stream-only trailer keys, so a second `Read` of your output
  will not show them where the input did. Compare with `DocumentEqual`, not with
  `bytes.Equal`.
