# 0019 — Consent to invalidate signatures is held by the Document

**Status:** accepted, in force.

## Context

Every whole-file writer — `Write`, `WriteContext`, `Save`, `SaveContext` —
regenerates the file from the object graph: new offsets, a new cross-reference
section, possibly different object-stream packing. That moves every byte a
signature's `/ByteRange` covers, so a document read from a signed file and
written again no longer verifies, even with nothing changed. The writers
returned nil while doing it. `SetEncryption` marked such a document for that
rewrite, and encryption cannot be added by an incremental update at all, so
"sign, then encrypt" was a silent way to produce an unverifiable file. Only
`WriteSigned` asked first: it refused with `ErrAlreadySigned` unless given the
`InvalidatingExistingSignatures()` option.

The writers now refuse such a document with an error wrapping
`ErrWouldInvalidateSignatures`. A caller who means to discard the signatures —
to encrypt a signed file, to repair or merge it — needs a way to say so, and
four shapes were on the table:

- **A per-call write option** given to new option-taking writers, as
  `WriteSigned` takes its `SignOption`.
- **Variadic options on the existing `Write` and `Save`** (`Write(w, opts...)`).
- **A `WithContext`-style transport**, the consent carried in a context value.
- **A method on the Document** that records the consent for the writers to read.

## Decision

The consent is a method on the Document, `AllowInvalidatingSignatures()`, and
it is held there for the Document's lifetime. Without it, `Write`,
`WriteContext`, `Save`, `SaveContext` and `SetEncryption` refuse a document
whose source file carries a signature or document time-stamp, and `WriteSigned`
refuses with `ErrAlreadySigned`; with it, they proceed. `WriteSigned` still
takes `InvalidatingExistingSignatures()`, now documented as the one-call form
of the same consent. "Signed" is what the source record found at `Read`, by the
verifier's own test, whatever has since been done to the graph; a document
built in memory has no source file and is never refused.

The alternatives, and why each lost:

- **A per-call write option.** It would need option-taking twins of four
  writers (`Write`, `WriteContext`, `Save`, `SaveContext`), or a new writer whose
  name says less than `Write` does. And it cannot reach `SetEncryption`, which
  has to refuse before any writer is called: it is a mutator that marks the
  document, and once it has run there is no way to write the result without
  invalidating the signatures, nor any honest way to undo it. A per-call
  consent would leave `SetEncryption` either refusing with no way through or
  succeeding into a document only a consenting writer could write.
- **Variadic options on `Write` and `Save`.** Source-compatible at most call
  sites, but it changes the methods' types: a method value `doc.Write` stops
  being a `func(io.Writer) error`, and a caller's interface naming
  `Write(io.Writer) error` stops being satisfied. That is a compile break for
  a consent that most callers never need, and `SetEncryption` would still need
  its own form.
- **A context value.** [ADR 0010](0010-context-variants.md) keeps contexts for
  cancellation and [ADR 0009](0009-functional-options.md) rejects them as a
  transport for configuration, because a dropped context reverts silently; a
  dropped consent would turn into a refusal, which is safe, but it would still
  be configuration riding on a mechanism for a different job.

The Document is also where the question lives. What is at risk is this
document's signatures, which the document itself carries; the consent is about
them, not about a call. It sits beside `SetEncryption` and `RemoveEncryption`,
which already configure the next `Write` the same way.

## Consequences

- No existing method's type changed. The cost is one stored flag, and an
  action at a distance: `Write` succeeds because of a call made earlier, as it
  encrypts because of an earlier `SetEncryption`. A document read back from the output
  does not inherit the consent, so it cannot leak into a later file.
- The consent is coarse: it covers every later whole-file write of this
  Document, not only the next. That matches its meaning — once a caller has
  decided to discard a file's signatures, every rewrite discards them — but a
  caller who wants one consented write and refusals afterwards must read the
  file again.
- [ADR 0009](0009-functional-options.md) stays as it is: options are for
  resource limits resolved at `Read`, and this is not a limit. Nor does it
  conflict with [ADR 0010](0010-context-variants.md)'s rule against storing a
  context: what is stored is a decision about the document, not a per-call
  deadline.
- The CLI's `-invalidate-signatures` flag on `decrypt`, `encrypt`, `repair` and
  `merge` is this consent, given per run.
- The incremental writers need no consent: they keep the signed bytes. They
  refuse a document read from an encrypted file even after `RemoveEncryption`,
  since an update cannot be appended to ciphertext under a trailer that no
  longer declares it.
