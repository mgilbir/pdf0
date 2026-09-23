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
go install ./cmd/pdf0                             # from a checkout: into $GOBIN or $(go env GOPATH)/bin
go install github.com/mgilbir/pdf0/cmd/pdf0@latest
```

Prefer `go install` (or `go build -o <somewhere-else> ./cmd/pdf0`) to a bare
`go build ./cmd/pdf0` in a checkout: that drops a `pdf0` executable in the repository
root, which is how compiled binaries once ended up in a release
(`scripts/check-no-binaries.sh` now refuses them).

The install path is `github.com/mgilbir/pdf0/cmd/pdf0` (module `github.com/mgilbir/pdf0`,
command in `cmd/pdf0`). The repository has two usable version tags, `v0.1.0` and `v0.3.2`;
`v0.2.0` through `v0.3.1` are retracted (see `go.mod`), so `@latest` resolves to `v0.3.2`.
The behaviour on this page — passwords off the command line, the overwrite guard, `-`
for stdin and stdout — is newer than `v0.3.2`; until a release carries it, install
`@main` to get it.

## Synopsis

`pdf0 -h`, `pdf0 --help`, `pdf0 help`, and `pdf0` with no arguments all print this. It
goes to **stderr**, not stdout:

```
pdf0 — inspect, validate, and (de)encrypt PDF files

usage:
  pdf0 info     [-password-file F] <file>
  pdf0 validate [-level LEVEL|declared] [-password-file F] <file>
  pdf0 decrypt  [-force] [-password-file F] <in> <out>
  pdf0 encrypt  [-force] [-user-password-file F] [-owner-password-file F] <in> <out>
  pdf0 extract  [-password-file F] <file>
  pdf0 repair   [-force] [-level LEVEL] [-password-file F] <in> <out>
  pdf0 merge    [-force] <out> <in1> <in2> [in3 ...]
  pdf0 ua       [-password-file F] <file>

"-" is stdin as an input and stdout as an output. An existing output is
replaced only with -force, and never when it is one of the inputs.

passwords are never taken on the command line. Each comes from, in order:
its file flag ("-" reads stdin), the environment (PDF0_PASSWORD,
PDF0_USER_PASSWORD, PDF0_OWNER_PASSWORD), or a no-echo prompt when stdin
is a terminal.

exit codes: 0 success, 1 violations reported, 2 usage error or refused
overwrite, 3 read/write, parse, or encryption error
```

The three help spellings exit **0**. Bare `pdf0` exits **2**, and an unknown command prints
`unknown command "bogus"` followed by the same block, also exit **2**.

Flags come before the operands (Go's `flag` package stops at the first operand), so
`pdf0 decrypt -force in.pdf out.pdf`, not `pdf0 decrypt in.pdf out.pdf -force`.

## Exit codes

The split between 1 and 3 is the point of the design: a conformance failure is a
*result*, not a crash, and a script must tell them apart.

| Code | Meaning | Emitted by |
|---|---|---|
| 0 | Success — the run completed and no checks reported violations | all commands |
| 1 | Checks reported violations; the file was read (and written) fine | `validate`, `ua`, `repair` |
| 2 | Usage error: bad/missing operand, unknown command or flag, unknown `-level`, a password given on the command line, an output that exists (without `-force`) or is one of the inputs | all commands |
| 3 | Operational error: I/O, parse failure, missing or wrong password, encryption-state conflict, a result that could not be written to stdout | all commands |

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

A command whose stdout cannot be written — redirected to a full disk, say — exits **3**
with `error: writing to stdout: …`, whatever it would otherwise have returned: a report
nobody received is not a success.

## Passwords

A password is **never** taken from the command line. Anything in argv is readable by every
user on the machine (`ps`, `/proc/<pid>/cmdline`) and is saved in shell history. Each
password a command takes has three sources, tried in this order:

| Source | Reading a file (`info`, `validate`, `decrypt`, `extract`, `repair`, `ua`) | `encrypt`: user password | `encrypt`: owner password |
|---|---|---|---|
| 1. file flag | `-password-file F` | `-user-password-file F` | `-owner-password-file F` |
| 2. environment | `PDF0_PASSWORD` | `PDF0_USER_PASSWORD` | `PDF0_OWNER_PASSWORD` |
| 3. prompt | when the file turns out to be locked | always, twice to confirm | never; defaults to the user password |

- **File flag.** The file's whole contents are the password, minus **one** trailing
  newline (`\n` or `\r\n`), so `echo secret > pw` works; a password that itself ends in a
  newline cannot be expressed. `F` may be `-` for stdin — `pass show pdf | pdf0 decrypt
  -password-file - in.pdf out.pdf` — unless stdin is also the PDF input (`stdin cannot be
  used for both …`, exit 2) or is a terminal, where it would echo; omit the flag to be
  prompted instead. A file flag wins over the environment.
- **Environment.** An empty variable counts as unset. The environment is not visible to
  other users, but it is inherited by child processes and readable by your own.
- **Prompt.** Only when stdin is a terminal and is not the PDF input. Echo is switched off
  before the prompt appears and restored afterwards, including when the prompt is
  interrupted with Ctrl-C (exit 130). The prompt is written to stderr. It is implemented on
  Linux and macOS; elsewhere there is no prompt and a password comes from a file or the
  environment.
- **No terminal, no password.** With stdin not a terminal (a script, a pipe, CI) there is
  no prompt; a locked file fails at once with exit 3 rather than hanging.

A password is bounded at 4096 bytes. ISO 32000-2 uses only the first 127 bytes of one (32
for the older revisions), so the bound only stops a mistaken `-password-file /dev/zero`.

The old `-password`, `-user` and `-owner` flags are still recognised, but only to refuse
them (exit 2) with a message naming the replacements; the value given is never used or
printed. By the time pdf0 sees it the password has already been exposed, so change it if
it mattered.

Why a file stayed locked comes from the library (`Document.LockReason`), and each reason
has its own message, all exit 3. A **missing** password and a **wrong** one are told apart,
and encryption pdf0 cannot open is never blamed on the password:

```
error: enc.pdf is encrypted and no password was supplied (give it with -password-file FILE, PDF0_PASSWORD, or run from a terminal to be prompted)
error: could not decrypt enc.pdf: the password is wrong (the password is neither the user nor the owner password)
error: could not decrypt unk.pdf: unsupported encryption: /Filter …: only the standard security handler is implemented
```

A malformed `/Encrypt` dictionary is reported the same way as unsupported encryption, with
the library's description of the entry at fault.

## Inputs and outputs

`-` is **stdin** as an input and **stdout** as an output, for every command. Only one input
may be `-`. A PDF is never written to a terminal: `-` as the output with stdout on a
terminal is refused (exit 2).

Every command that writes a file — `decrypt`, `encrypt`, `repair`, `merge` — follows the
same rules:

- **An output is never one of the inputs**, compared by file identity rather than by
  spelling: `./dir/../in.pdf`, a symlink to `in.pdf` and a hard link to it are all
  refused, exit 2, and `-force` does not change that. (`pdf0 merge a.pdf b.pdf` once
  replaced `a.pdf` with a copy of `b.pdf`.)
- **An existing output is replaced only with `-force`**; without it the command exits 2
  before doing any work: `error: output out.pdf already exists; pass -force to replace
  it`. This is stricter than `cp`, deliberately: the output is a positional operand next
  to the inputs, and swapping two operands, or reusing a name, should cost a flag rather
  than a file. The check is repeated atomically when the file is put in place, so a file
  created in the meantime is not overwritten either.
- **The output appears whole or not at all.** It is written to a temporary file in the same
  directory (`.<name>.pdf0-<random>.tmp`), synced, and then renamed or linked into place;
  a failure part-way — a full disk, a file-size limit — leaves any existing output as it
  was and no temporary file behind. (A process killed with SIGKILL mid-write can leave the
  temporary file.)
- **Through a symlink**, `-force` replaces the file the link points to and keeps the link.
- **Permissions.** Output that holds decrypted content — `decrypt`, and `repair` of an
  encrypted file — is created **0600** whatever the umask. Other output is created 0666
  less the umask, as a shell redirect would. A replaced file does not keep the old file's
  mode.

When the output is stdout, the PDF is the only thing written to it: `repair`'s report moves
to stderr.

Two things pdf0 cannot prevent: `pdf0 decrypt in.pdf - > in.pdf` has already lost `in.pdf`
when pdf0 starts, because the shell truncates it first (pdf0 notices and says so, exit 2);
and reading stdin is unbounded, as is reading any input file.

---

## `info`

`pdf0 info [-password-file F] <file>`

```
$ pdf0 info simple.pdf
version:   2.0
objects:   6
pages:     1
encrypted: false
locked:    false
```

`pages` comes from the page tree (`Document.PageCount`), not a scan for `/Type /Page`, so
an orphan page object outside the tree does not inflate it. `objects` is the size of the
object map after `Read` normalisation. `info` is the only read command that does **not**
refuse a locked file, and it never prompts: it reports structure, which a locked file
still has. `encrypted` reflects the presence of an `/Encrypt` dictionary and stays `true`
with the right password; `locked` says whether the password supplied (from the file flag
or `PDF0_PASSWORD`, if any) opened it.

Exit codes: 0; 2 (no operand, or more than one); 3 — `error: open nosuch.pdf: no such file
or directory`, `error: x.txt: PDF header not found`, `error: read /tmp: is a directory`.

## `validate`

`pdf0 validate [-level LEVEL|declared] [-password-file F] <file>`

| Flag | Default | Meaning |
|---|---|---|
| `-level` | `2b` | PDF/A level: `1b`, `1a`, `2b`, `2u`, `2a`, `3b`, `3u`, `3a`, `4`, `4e` or `4f`; or `declared`, the level the file's own metadata declares |
| `-password-file` | — | see [Passwords](#passwords) |

Runs `ValidatePDFA` on the document read from the file: the object model *and*,
from the same file, the byte-level clause 6.1 file-structure rules.

```
$ pdf0 validate clean2b.pdf
clean2b.pdf: no violations found for PDF/A-2b            # exit 0

$ pdf0 validate -level 1b clean2b.pdf
[PDF/A-1b 6.2.2] /OutputIntents[0] ICC profile version 4.3 not allowed for PDF/A-1b (max 2.x)
[PDF/A-1b 6.7.11] pdfaid:part must be 1, got 2
error: 2 violation(s) found                              # stderr, exit 1

$ pdf0 validate -level declared clean2b.pdf
clean2b.pdf: no violations found for PDF/A-2b            # exit 0
```

Any other `-level` is a **usage** error (exit 2): `error: unknown level "5b" (want 1b, 1a,
2b, 2u, 2a, 3b, 3u, 3a, 4, 4e, 4f, or declared)`. With `-level declared`, a file that
declares no PDF/A level is reported with one `limit` finding and not validated.

A locked file is refused rather than validated against ciphertext, exit **3**, with the
[missing- or wrong-password message](#passwords). With the password it validates normally
and correctly reports the encryption itself (`[PDF/A-2b 6.1.3] trailer must not contain
/Encrypt`).

## `ua`

`pdf0 ua [-password-file F] <file>`. Runs `ValidatePDFUA`, **PDF/UA-1 only**: there is no
`-level`, and `ValidatePDFUA2` is unreachable from the CLI.

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
file is 3, as for `validate`).

## `decrypt`

`pdf0 decrypt [-force] [-password-file F] <in> <out>` — the password may be the user or the
owner password. Reads `<in>`, calls `RemoveEncryption`, writes plaintext to `<out>` with
mode **0600**, and succeeds silently (`PDF0_PASSWORD=secret pdf0 decrypt enc.pdf dec.pdf`,
exit 0; `info dec.pdf` then shows `encrypted: false`). Failures:

```
error: simple.pdf is not encrypted                                          # exit 3
error: enc.pdf is encrypted and no password was supplied (…)                # exit 3
error: could not decrypt enc.pdf: the password is wrong (…)                 # exit 3
error: output dec.pdf already exists; pass -force to replace it             # exit 2
error: creating the output in /nonexistentdir: open /nonexistentdir/.out.pdf.pdf0-….tmp: no such file or directory  # exit 3
```

## `encrypt`

`pdf0 encrypt [-force] [-user-password-file F] [-owner-password-file F] <in> <out>`

Applies `SetEncryption` and writes `<out>`; silent on success, exit 0.

- The **user password** is required and must not be empty (an empty user password opens
  the file for anyone: encrypted, but not protected). With neither `-user-password-file`
  nor `PDF0_USER_PASSWORD`, `encrypt` prompts twice on a terminal and refuses if the two
  answers differ; with no terminal it exits 2 (`encrypt needs a user password: …`).
- The **owner password** comes from `-owner-password-file` or `PDF0_OWNER_PASSWORD`, and
  otherwise defaults to the user password. It is never prompted for.
- `<in>` is always read without a password, so an already-encrypted input is rejected:

```
$ PDF0_USER_PASSWORD=secret pdf0 encrypt simple.pdf enc.pdf   # exit 0, no output
$ PDF0_USER_PASSWORD=secret pdf0 encrypt enc.pdf enc2.pdf
error: enc.pdf is already encrypted; decrypt it first    # exit 3
```

## `extract`

`pdf0 extract [-password-file F] <file>`

Extracts **plain text only** — `Document.ExtractText`, which walks the page tree and each
page's content stream, recursing into invoked form XObjects. It does not extract images,
embedded files, attachments, or pages.

```
$ pdf0 extract simple.pdf

Hello, PDF 2.0!
```

Piping details: pages are separated by a form feed (`\f`, 0x0C) and there is **no trailing
newline**; the blank line above is text the document actually contains, not a separator. A
document with no text prints nothing and exits 0. A locked file is refused, exit 3. A page
whose text could not be extracted — the content budget ran out, or the extractor failed
internally — is left out: the other pages' text is printed, still separated by form feeds,
and the command reports the page on stderr and exits 3, so the output is never taken for
the whole document.

## `repair`

`pdf0 repair [-force] [-level LEVEL] [-password-file F] <in> <out>` — `-level`
defaults to `2b` ("target PDF/A level").

Calls `Document.Repair(level)`, which removes forbidden **document-level** constructs only
— it is not a general fixer. It then serialises the result, **re-reads and re-validates
those bytes**, writes `<out>`, and reports what is left.

```
$ pdf0 repair -level 2b broken2b.pdf rep2.pdf
fixed: removed catalog additional-actions (/AA)
rep2.pdf: 1 fix(es) applied, 0 violation(s) remain                  # exit 0

$ pdf0 repair -level 1b broken2b.pdf rep1.pdf
fixed: removed catalog additional-actions (/AA)
rep1.pdf: 1 fix(es) applied, 2 violation(s) remain
error: 2 violation(s) remain after repair (run: pdf0 validate -level 1b rep1.pdf)  # exit 1
```

The output file is written even when violations remain: exit 1 means "written but still
non-conformant", not "nothing happened". A no-op run still prints its summary
(`0 fix(es) applied, 0 violation(s) remain`) rather than exiting silently. The report is
printed after the file is in place, so a `fixed:` line never describes a file that was not
written.

- **A locked file is refused**, exit 3, with the [password messages](#passwords):
  repairing ciphertext would report "0 fixes" for work never attempted.
- **With the password**, removing encryption counts as a repair
  (`fixed: removed document encryption (/Encrypt)`), and the output is created **0600**,
  since it holds the decrypted content.
- **If the repaired document does not read back**, that is an operational error (exit 3,
  `the repaired document does not read back (nothing written): …`) and no output is
  written — never "0 violation(s) remain".
- **To stdout** (`<out>` is `-`), the `fixed:` lines and the summary go to stderr.

Bad `-level` is exit 2: `error: unknown level "9z" (want 1b, 1a, 2b, 2u, 2a, 3b, 3u, 3a, 4, 4e, 4f)`.

## `merge`

`pdf0 merge [-force] <out> <in1> <in2> [in3 ...]` — the *first* operand is the output; the
rest are inputs, read left to right and concatenated with `AppendPages`. At least **two**
inputs are required: with one, `pdf0 merge a.pdf b.pdf` reads naturally as "merge a and b"
but would replace `a.pdf`, so it is a usage error (exit 2).

```
$ pdf0 merge merged3.pdf simple.pdf a4.pdf simple17.pdf   # exit 0, no output
$ pdf0 info merged3.pdf
version:   2.0
objects:   16
pages:     3
encrypted: false
locked:    false
```

The result keeps the **first** input's catalog (its outline, metadata, form, …) and takes the
**highest** PDF version of the inputs — merging a 1.7 file first with a 2.0 file second
yields `version: 2.0`, since the 2.0 pages may use 2.0 features. Each appended page is
standalone: the attributes it inherited in its own file (`/Resources`, `/MediaBox`,
`/CropBox`, `/Rotate`) are written on it, its form fields join the merged form (renamed
when the name is taken), and links between its pages follow the copies. What an input
had that the merge does not carry is said on stderr, one line each, and is not an error:

```
$ pdf0 merge m.pdf s17.pdf annotated.pdf                  # exit 0
annotated.pdf: not carried: Metadata: a document-level entry of the source; a page import does not carry it
```

`merge` takes **no password**, and every input is checked, so an encrypted file in any
position is an operational error — copying ciphertext into a plaintext container would
corrupt the result:

```
$ pdf0 merge m.pdf simple.pdf enc.pdf
error: enc.pdf is encrypted; decrypt it before merging    # exit 3
```

---

## What the CLI does not expose

Everything below is implemented in the library and has **no** CLI surface. Absence from
`cmd/pdf0` is not absence from pdf0.

| Capability | Library entry point |
|---|---|
| PDF/A Level A (1a, 2a, 3a) | `PDFA1a`/`PDFA2a`/`PDFA3a` with `ValidatePDFA` — the constants exist; `-level` rejects those names with exit 2 |
| PDF/UA-2 | `ValidatePDFUA2(doc)`; `ua` only ever calls `ValidatePDFUA` (UA-1) |
| PDF/X, PDF/VT, PDF/VT-2, PDF/R | `ValidatePDFX(doc, pdfx.Level)`, `ValidatePDFVT`, `ValidatePDFVT2`, `ValidatePDFR` |
| DPart / document-part hierarchy | `ValidateDParts(doc)` |
| Factur-X / ZUGFeRD, Order-X | `ValidateFacturX`, `ValidateOrderX`, `EmbedFacturX` |
| Signature verification, PAdES | `VerifySignatures`, `ValidatePAdES`, `DSSCerts`, `DSSRevocationMaterial`; `sign.CheckCertRevocation` |
| Signing and timestamping | `WriteSigned`, `WriteSignedIncremental` (with `WithSignatureTimestamp` for B-T), `WriteArchivalTimestamp` |
| Image extraction | `ExtractImages()`, `Images()` (lazy iterator) |
| Page extraction / subsetting | `ExtractPages(indices)` (returns the new document and an `ImportReport` of what was not carried); per-page text via `ExtractPageText(page)` |
| Incremental write | `WriteIncremental(w, changed)`; the file it appends to is `Source()` |
| Building conformant documents | `NewPDFADocument`, `NewPDFADocumentWithInfo`, `NewPDFADocumentWith` (bring your own output intent); `pdfa.GenerateXMPMetadata`, `pdfa.DefaultSRGBProfile` |
| Comparison, low-level parsing | `DocumentEqual`, `ParseXRefTable`, `ParseXRefStream`; `object.Equal`, `syntax.NewLexer`, `syntax.NewParser`, `syntax.NewSerializer` |

Two encryption nuances the CLI flattens: `decrypt` is exactly `RemoveEncryption()` +
`Write()` on a document unlocked at `Read` time — it cannot keep an `/Encrypt` dictionary
while changing passwords; and `encrypt`'s `SetEncryption(user, owner)` exposes no
permission bits, algorithm, or key-length choice (that is the library's full signature too).
In short, the tool wraps eight entry points and none of the rest — which is why the README
presents it as a development aid rather than the library's surface.

## Edge cases and surprises

- **All help output goes to stderr**, including `pdf0 <cmd> -h`, so `pdf0 -h > file`
  captures nothing. Top-level help exits 0; bare `pdf0` prints the same text and exits 2.
- **Subcommand `-h` lists only that command's flags** (Go's `flag` package), exit 0. An
  unknown or argument-less flag (`pdf0 info -bogus`, `pdf0 validate -level`) is handled by
  the `flag` package and exits 2 — consistent with the usage contract, but formatted
  differently from the tool's own `error: …` lines.
- **Re-running a command that writes needs `-force`** the second time, because the first
  run's output now exists.
- **`info` never fails on encryption** and always reports `encrypted: true` for a file with
  an `/Encrypt` dictionary; `locked` is the line that says whether it could be read.
- A non-PDF input fails at parse with exit 3 (`error: x.txt: PDF header not found`), so
  exit 3 covers both "cannot open" and "not a PDF".
