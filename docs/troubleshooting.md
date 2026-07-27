# Troubleshooting

Organised by the symptom you actually have. Every message quoted below is a real
string emitted by the library or the `pdf0` CLI.

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
| `encryption: …` | The `/Encrypt` dictionary itself is malformed. See below — a *wrong password* does not produce this. |
| `recovered from panic while reading PDF: …` | A bug. `Read` converts panics into errors so a hostile file cannot crash your process, but please report it (and see [testing.md](testing.md#fuzzing) — the fuzzers exist for exactly this). |

## `short read: got N of M bytes`

`Read(r io.ReaderAt, size int64)` takes the size you claim the file is, and
refuses to proceed if `r` yields fewer bytes than that. The check is deliberate:
the unfilled tail of the buffer is zero bytes, which count as PDF whitespace and
would silently mask truncated input.

**What to do:** pass the real size. From a file, `fi, _ := f.Stat(); pdf0.Read(f,
fi.Size())`. From a `[]byte`, `pdf0.Read(bytes.NewReader(b), int64(len(b)))` —
never a hard-coded or estimated length. If the size is right, the file really is
truncated; re-fetch it. The same error comes from `NewLexerFromReaderAt` for the
same reason.

## Encrypted files

Two fields, and they mean different things:

- **`Document.Encrypted`** — the file *carried* an `/Encrypt` dictionary. It stays
  `true` on a file that was decrypted successfully.
- **`Document.Locked()`** — `Encrypted && no usable security handler`: the
  password was wrong or the scheme is unsupported, and *the strings and streams
  are still ciphertext*.

From the godoc on `Locked`:

> Encrypted alone does not distinguish this from a successfully decrypted file
> (both keep Encrypted true). Callers that intend to read content, validate,
> extract, or re-encrypt should check Locked first: on a locked document
> RemoveEncryption is a no-op, ExtractText and the validators see ciphertext, and
> SetEncryption/Write refuse.

**Empty password vs `ReadWithPassword`.** `Read` tries the empty password, which
covers the common "owner-restricted, no user password" case. For anything else
use `ReadWithPassword(r, size, pw)`, which accepts either the user or the owner
password. A wrong password is *not* an error — the file parses structurally and
comes back `Locked()`. If you do not check, you will validate ciphertext and get
nonsense violations.

The CLI checks for you, per subcommand:

```
could not read FILE: it is encrypted (supply -password)      # validate, extract, ua
could not decrypt FILE: wrong password or unsupported encryption   # decrypt
FILE is encrypted; decrypt it before merging                  # merge
```

**Why `Write` sometimes refuses.** `Write` does *not* refuse every encrypted
document. A document that was decrypted on `Read` is re-encrypted with the
retained key and round-trips. A *locked* document is passed through verbatim —
its already-encrypted content is written back under the preserved `/Encrypt` and
`/ID`, so a file you cannot decrypt is still round-trippable rather than lost.
That passthrough is sound only when the encryption state is knowable and the
whole object model survived, so it refuses in exactly two cases:

```
cannot write encrypted document: its /Encrypt dictionary is unresolvable, so the encryption state is unknown
cannot write encrypted document: N object stream(s) could not be decrypted, so some objects are missing
```

The first would produce a file readers wrongly try to decrypt; the second would
silently drop the objects locked inside the undecryptable container.

Two related refusals that are not about encryption:

```
cannot write: N object stream(s) failed to decode on read, so some objects are missing
object number 0 is reserved and cannot be written
```

## Validation reported violations

A `ValidationError` prints as `[LEVEL CLAUSE] object N: message`, e.g.

```
[PDF/A-4 6.2.10] object 12: font ... must be embedded
```

`PDF/A-4` is the level whose rules require this; `6.2.10` is the ISO 19005
clause; `object 12` is the offending object number (omitted entirely when the
violation is document-level). Look the clause up in the standard, or in the
veraPDF profiles — `make rule-coverage` prints each clause's description.

**An empty result is not a conformance guarantee.** From the `ValidatePDFA`
godoc: *"An empty result means 'none of the implemented checks fired', not a
guarantee of full conformance: the validator covers a subset of ISO 19005."*

**Use `ValidatePDFABytes` when you have the file bytes.** `ValidatePDFA(doc,
level)` is exactly `ValidatePDFABytes(doc, level, nil)`, and passing `nil` skips
**every byte-level file-structure rule** — things like "no data after `%%EOF`",
the byte-level stream `/Length` check, and hex-string scanning, which need the
raw file and cannot be recovered from the object model. If you read the file into
a `[]byte` anyway, always pass it:

```go
errs := pdf0.ValidatePDFABytes(doc, pdf0.PDFA4, data)
```

**`[… internal] internal validator error: …`** means a check panicked and was
recovered so the rest could run. It is a bug in pdf0, not in your file.

Finally: validating a `Locked()` document validates ciphertext. Check
`doc.Locked()` first.

## `0 fix(es) applied` / violations remain after repair

`pdf0 repair` prints `FILE: N fix(es) applied, M violation(s) remain` and exits 1
when `M > 0`, with:

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

So `Repair` will drop encryption, catalog/page/annotation `/AA` dictionaries, and
synthesize a missing `/ID` — and will do nothing at all about an unembedded font,
because embedding one requires a font program it does not have. Zero fixes means
none of those document-level defects were present, not that the file is clean.

## A signature says `Valid` but you should not trust it yet

`SignatureResult.Valid` means only that the bytes *inside* the signed
`/ByteRange` are intact and were signed by the embedded certificate's key. It
says nothing about bytes outside that range, and nothing about whether the
certificate is trustworthy. From the godoc:

> A signed document can be modified after signing by an incremental update — the
> original signed range stays intact (Valid == true) while the rendered content
> changes (CoversWholeDocument == false). Use DocumentUnmodified for the combined
> "signed and nothing was changed" verdict.

Use `result.DocumentUnmodified()` (`Valid && CoversWholeDocument`) as your
baseline verdict. For trust, `VerifySignatures` builds no chain at all — you must
call `VerifySignaturesWithRoots(raw, roots)` and check `TrustedChain` / `ChainErr`,
which are reported separately and never affect `Valid`. For long-term validation
(PAdES B-T through B-LTA, timestamps, revocation) see
[docs/signing.md](signing.md).

## An image came back with `Decoded=false`

`ExtractedImage.Decoded` reports whether `Image` holds pixels. When it is false,
`Encoded` holds the raw stream bytes and `Note` says why — so read `Note` first:

```
JPEG decode failed: <err>
CCITTFaxDecode preceding filter chain could not be reversed; the raw encoded bytes are provided
CCITTFaxDecode failed: <err>
JBIG2Decode preceding filter chain could not be reversed; the raw encoded bytes are provided
JBIG2Decode not decoded (<err>); the raw encoded bytes are provided
JPXDecode not decoded; raw bytes provided
unsupported CCITT sample layout
unsupported JBIG2 sample layout
unsupported sample layout (colour space <cs>, <n> bpc)
```

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
- **To amend a file, use `WriteIncremental`.** It writes "the original file bytes
  verbatim followed by only the objects listed in changed, a new cross-reference
  section whose /Prev chains back to the original, and a new trailer. The
  original bytes are preserved exactly, so any signature over them stays valid
  and the update can be undone by truncation." It refuses encrypted documents
  (`incremental update of an encrypted document is not supported`) and an empty
  change list (`incremental update with no changed objects`).
- **`Read` normalizes structure.** It drops `/XRef` and `/ObjStm` objects and
  strips the xref-stream-only trailer keys, so a second `Read` of your output
  will not show them where the input did. Compare with `DocumentEqual`, not with
  `bytes.Equal`.
