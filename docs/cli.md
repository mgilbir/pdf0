# The `pdf0` command-line tool

**pdf0 is a library.** `cmd/pdf0` is a small command-line front end over it, useful for
poking at files during development and for shell-scriptable checks, but it is not the
supported surface and it reaches only a fraction of the API — if you are choosing between
the two, use the library. This page documents the tool as it actually behaves.

`cmd/pdf0` is a thin front end over a deliberately small slice of the pdf0 library:
inspecting structure, running the PDF/A and PDF/UA-1 validators, adding or removing
standard-security encryption, extracting plain text, applying the document-level PDF/A
repairs, and concatenating page trees. Use it when you want a shell-scriptable preflight or
conversion step — its exit codes distinguish "the file has conformance violations" from
"the run failed". Use the library for anything else: the CLI exposes only a fraction of
what `github.com/mgilbir/pdf0` implements ([what it does not
expose](#what-the-cli-does-not-expose)).

```
go build ./cmd/pdf0                              # builds ./pdf0 in the repo
go install github.com/mgilbir/pdf0/cmd/pdf0@latest
```

The install path is `github.com/mgilbir/pdf0/cmd/pdf0` (module `github.com/mgilbir/pdf0`,
command in `cmd/pdf0`). The repository carries **no version tags**, so `@latest` resolves
to a pseudo-version of the default branch.

## Synopsis

`pdf0 -h`, `pdf0 --help`, `pdf0 help`, and `pdf0` with no arguments all print this. It
goes to **stderr**, not stdout:

```
pdf0 — inspect, validate, and (de)encrypt PDF files

usage:
  pdf0 info     [-password PW] <file>
  pdf0 validate [-level 1b|2b|3b|4] [-password PW] <file>
  pdf0 decrypt  [-password PW] <in> <out>
  pdf0 encrypt  -user PW [-owner PW] <in> <out>
  pdf0 extract  [-password PW] <file>
  pdf0 repair   [-level 1b|2b|3b|4] [-password PW] <in> <out>
  pdf0 merge    <out> <in1> <in2> [in3 ...]
  pdf0 ua       [-password PW] <file>

exit codes: 0 success, 1 violations reported, 2 usage error,
3 read/write, parse, or encryption error
```

The three help spellings exit **0**. Bare `pdf0` exits **2**, and an unknown command prints
`unknown command "bogus"` followed by the same block, also exit **2**.

## Exit codes

The split between 1 and 3 is the point of the design: a conformance failure is a
*result*, not a crash, and a script must tell them apart.

| Code | Meaning | Emitted by |
|---|---|---|
| 0 | Success — the run completed and no checks reported violations | all commands |
| 1 | Checks reported violations; the file was read (and written) fine | `validate`, `ua`, `repair` |
| 2 | Usage error: bad/missing operand, unknown command, unknown flag, unknown `-level` | all commands |
| 3 | Operational error: I/O, parse failure, wrong password, encryption-state conflict | all commands |

Violation lines go to **stdout**; the trailing `error: …` summary goes to **stderr**:

```sh
pdf0 validate -level 2b in.pdf > report.txt
case $? in
  0) echo conformant ;;
  1) echo "violations:"; cat report.txt ;;
  2) echo "invoked pdf0 wrong" ;;
  3) echo "could not process the file" ;;
esac
```

---

## `info`

`pdf0 info [-password PW] <file>` — `-password` (default `""`) takes the user *or* owner
password.

```
$ pdf0 info simple.pdf
version:   2.0
objects:   6
pages:     1
encrypted: false
```

`pages` comes from the page tree (`Document.PageCount`), not a scan for `/Type /Page`, so
an orphan page object outside the tree does not inflate it. `objects` is the size of the
object map after `Read` normalisation. `info` is the only read command that does **not**
refuse a locked file; consequently `encrypted` reflects the mere presence of an `/Encrypt`
dictionary and stays `true` even when you supply the right password.

Exit codes: 0; 2 (no operand, or more than one); 3 — `error: open nosuch.pdf: no such file
or directory`, `error: PDF header not found`, `error: read /tmp: is a directory`.

## `validate`

`pdf0 validate [-level 1b|2b|3b|4] [-password PW] <file>`

| Flag | Default | Meaning |
|---|---|---|
| `-level` | `2b` | PDF/A level: `1b`, `2b`, `3b`, or `4` — nothing else |
| `-password` | `""` | user or owner password |

Runs `ValidatePDFABytes`: the object model *and* the raw bytes, so the byte-level clause
6.1 file-structure rules apply.

```
$ pdf0 validate clean2b.pdf
clean2b.pdf: no violations found for PDF/A-2b            # exit 0

$ pdf0 validate -level 1b clean2b.pdf
[PDF/A-1b 6.2.2] /OutputIntents[0] ICC profile version 4.3 not allowed for PDF/A-1b (max 2.x)
[PDF/A-1b 6.2.3] object 5: ICCBased profile version 4.x not allowed (max 2.x)
[PDF/A-1b 6.7.11] pdfaid:part must be 1, got 2
error: 3 violation(s) found                              # stderr, exit 1
```

Any other `-level` is a **usage** error (exit 2), including the Level A names the library
does implement: `error: unknown level "1a" (want 1b, 2b, 3b, or 4)`.

A locked file is refused rather than validated against ciphertext — `error: could not read
enc.pdf: it is encrypted (supply -password)`, exit **3**. With the password it validates
normally and correctly reports the encryption itself
(`[PDF/A-2b 6.1.3] trailer must not contain /Encrypt`).

## `ua`

`pdf0 ua [-password PW] <file>` — `-password` (default `""`). Runs `ValidatePDFUA`,
**PDF/UA-1 only**: there is no `-level`, and `ValidatePDFUA2` is unreachable from the CLI.

```
$ pdf0 ua simple.pdf
[PDF/UA-1 7.1] document is not marked as tagged (/MarkInfo << /Marked true >>)
[PDF/UA-1 7.1] document has no structure tree (/StructTreeRoot)
[PDF/UA-1 7.2] document does not specify a default language (catalog /Lang)
[PDF/UA-1 7.1] /ViewerPreferences /DisplayDocTitle must be true
[PDF/UA-1 5] document has no XMP metadata (a PDF/UA identifier is required)
[PDF/UA-1 7.21.4.1] object 6: font used for rendering is not embedded
[PDF/UA-1 6.1] PDF/UA-1 requires a PDF 1.x header, got 2.0
[PDF/UA-1 7.1] object 3: page contains text that is neither tagged nor marked as an /Artifact
error: 8 PDF/UA violation(s)                             # stderr, exit 1
```

A clean run prints `<file>: no PDF/UA violations found (foundational checks)` — the
parenthetical is the tool's own honesty about coverage. Exit codes 0, 1, 2, 3 (a locked
file without a password is 3, same message as `validate`).

## `decrypt`

`pdf0 decrypt [-password PW] <in> <out>` — `-password` accepts either the user or the
owner password. Reads `<in>`, calls `RemoveEncryption`, writes plaintext to `<out>`, and
succeeds silently (`pdf0 decrypt -password secret enc.pdf dec.pdf`, exit 0; `info dec.pdf`
then shows `encrypted: false`). Three distinct failures, all exit **3**:

```
error: simple.pdf is not encrypted                                          # nothing to do
error: could not decrypt enc.pdf: wrong password or unsupported encryption  # bad/absent password
error: open /nonexistentdir/out.pdf: no such file or directory              # unwritable <out>
```

Omitting `-password` on a protected file yields the same "wrong password" message, because
the empty user password is what was tried.

## `encrypt`

`pdf0 encrypt -user PW [-owner PW] <in> <out>`

| Flag | Default | Meaning |
|---|---|---|
| `-user` | `""` | user password |
| `-owner` | `""` | owner password; defaults to the user password |

Applies `SetEncryption` and writes `<out>`; silent on success, exit 0. There is **no
`-password` flag**: `encrypt` always reads `<in>` with the empty password, so an
already-encrypted input is rejected.

```
$ pdf0 encrypt -user secret simple.pdf enc.pdf           # exit 0, no output
$ pdf0 encrypt -user secret enc.pdf enc2.pdf
error: enc.pdf is already encrypted; decrypt it first    # exit 3
```

Although the synopsis shows `-user` as mandatory, the guard is `user == "" && owner == ""`,
so **`-owner PW` alone is accepted**, producing a file with an empty user password.
Supplying neither is a usage error (exit 2): `error: encrypt requires -user (and optionally -owner)`.

## `extract`

`pdf0 extract [-password PW] <file>`

Extracts **plain text only** — `Document.ExtractText`, which walks the page tree and each
page's content stream, recursing into invoked form XObjects. It does not extract images,
embedded files, attachments, or pages.

```
$ pdf0 extract simple.pdf

Hello, PDF 2.0!
```

Piping details: pages are separated by a form feed (`\f`, 0x0C) and there is **no trailing
newline**; the blank line above is text the document actually contains, not a separator. A
document with no text prints nothing and exits 0. A locked file is refused, exit 3.

## `repair`

`pdf0 repair [-level 1b|2b|3b|4] [-password PW] <in> <out>` — `-level` defaults to `2b`
("target PDF/A level"), `-password` to `""`.

Calls `Document.Repair(level)`, which removes forbidden **document-level** constructs only
— it is not a general fixer. It then writes `<out>`, **re-reads its own output and
re-validates it**, and reports what is left.

```
$ pdf0 repair -level 2b broken2b.pdf rep2.pdf
fixed: removed catalog additional-actions (/AA)
rep2.pdf: 1 fix(es) applied, 0 violation(s) remain                  # exit 0

$ pdf0 repair -level 1b broken2b.pdf rep1.pdf
fixed: removed catalog additional-actions (/AA)
rep1.pdf: 1 fix(es) applied, 3 violation(s) remain
error: 3 violation(s) remain after repair (run: pdf0 validate -level 1b rep1.pdf)  # exit 1
```

The output file is always written, even when violations remain: exit 1 means "written but
still non-conformant", not "nothing happened". A no-op run still prints its summary
(`0 fix(es) applied, 0 violation(s) remain`) rather than exiting silently. With a password,
removing encryption counts as a repair (`fixed: removed document encryption (/Encrypt)`).

Unlike `validate`/`ua`/`extract`, `repair` does **not** check `Locked()`. Run on an
encrypted file without the password it does not refuse: `Write` emits a byte-faithful
passthrough of the still-encrypted document, 0 fixes are applied, and it reports the
violations visible from a locked read. The output stays openable with the original
password — but nothing was repaired and the tool never says so. Bad `-level` is exit 2 with
a terser message than `validate`'s: `error: unknown level "9z"`.

## `merge`

`pdf0 merge <out> <in1> <in2> [in3 ...]` — **no flags at all**. The *first* operand is the
output; the rest are inputs, read left to right and concatenated with `AppendPages`.

```
$ pdf0 merge merged3.pdf simple.pdf a4.pdf simple17.pdf   # exit 0, no output
$ pdf0 info merged3.pdf
version:   2.0
objects:   16
pages:     3
encrypted: false
```

The result inherits the **first** input's header version — merging a 1.7 file first with a
2.0 file second yields `version: 1.7`; it does not take the maximum. `merge` accepts **no
password**: `-password` is rejected by the flag parser (`flag provided but not defined:
-password`, exit 2), and every input is checked, so an encrypted file in any position is an
operational error — copying ciphertext into a plaintext container would corrupt the result:

```
$ pdf0 merge m.pdf simple.pdf enc.pdf
error: enc.pdf is encrypted; decrypt it before merging    # exit 3
```

The synopsis implies at least two inputs, but the check is `NArg() < 2`, so **`pdf0 merge
out.pdf one.pdf` is accepted** and simply rewrites the single input (exit 0). Only
`pdf0 merge out.pdf` with no input is a usage error.

---

## What the CLI does not expose

Everything below is implemented in the library and has **no** CLI surface. Absence from
`cmd/pdf0` is not absence from pdf0.

| Capability | Library entry point |
|---|---|
| PDF/A Level A (1a, 2a, 3a) | `PDFA1a`/`PDFA2a`/`PDFA3a` with `ValidatePDFA(Bytes)` — the constants exist; `-level` rejects those names with exit 2 |
| Model-only PDF/A (skips clause 6.1 byte rules) | `ValidatePDFA(doc, level)` — the CLI always uses `ValidatePDFABytes` |
| PDF/UA-2 | `ValidatePDFUA2(doc)`; `ua` only ever calls `ValidatePDFUA` (UA-1) |
| PDF/X, PDF/VT, PDF/VT-2, PDF/R | `ValidatePDFX(doc, PDFXLevel)`, `ValidatePDFVT`, `ValidatePDFVT2`, `ValidatePDFR` |
| DPart / document-part hierarchy | `ValidateDParts(doc)` |
| Factur-X / ZUGFeRD, Order-X | `ValidateFacturX`, `ValidateOrderX`, `EmbedFacturX` |
| Signature verification, PAdES | `VerifySignatures`, `VerifySignaturesWithRoots`, `ValidatePAdES(raw)`, `CheckCertRevocation`, `DSSCerts`, `DSSRevocationMaterial` |
| Signing and timestamping | `WriteSigned`, `WriteSignedIncremental`, `WriteSignedTimestamped`, `WriteArchivalTimestamp` |
| Image extraction | `ExtractImages()`, `Images()` (lazy iterator) |
| Page extraction / subsetting | `ExtractPages(indices)`; per-page text via `ExtractPageText(page)` |
| Incremental write | `WriteIncremental(w, original, changed)` |
| Building conformant documents | `NewPDFADocument`, `NewPDFADocumentWithInfo`, `GenerateXMPMetadata`, `DefaultSRGBProfile` |
| Comparison, low-level parsing | `DocumentEqual`, `Equal`, `NewLexer`, `NewParser`, `NewSerializer`, `ParseXRefTable`, `ParseXRefStream` |

Two encryption nuances the CLI flattens: `decrypt` is exactly `RemoveEncryption()` +
`Write()` on a document unlocked at `Read` time — it cannot keep an `/Encrypt` dictionary
while changing passwords; and `encrypt`'s `SetEncryption(user, owner)` exposes no
permission bits, algorithm, or key-length choice (that is the library's full signature too).
The README's claim that the CLI "wraps" the library's feature list is an overstatement: it
wraps eight entry points and none of the rest.

## Edge cases and surprises

- **All help output goes to stderr**, including `pdf0 <cmd> -h`, so `pdf0 -h > file`
  captures nothing. Top-level help exits 0; bare `pdf0` prints the same text and exits 2.
- **Subcommand `-h` lists only that command's flags** (Go's `flag` package): `pdf0 merge -h`
  prints just `Usage of merge:`, no flags, exit 0. An unknown or argument-less flag
  (`pdf0 info -bogus`, `pdf0 validate -level`) is handled by `flag.ExitOnError` and exits 2
  — consistent with the usage contract, but formatted differently from the tool's own
  `error: …` lines.
- **`repair` on a locked file silently no-ops** — the one place where an operation that
  cannot do its job still exits 1 rather than 3.
- **`encrypt -owner PW` without `-user`**, and **`merge` with a single input**, are both
  accepted despite the synopsis showing otherwise.
- **`info` never fails on encryption** and always reports `encrypted: true` for a file with
  an `/Encrypt` dictionary, password or not.
- A non-PDF input fails at parse with exit 3 (`error: PDF header not found`), so exit 3
  covers both "cannot open" and "not a PDF".
