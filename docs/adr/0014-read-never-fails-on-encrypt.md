# 0014 — `Read` never fails on `/Encrypt`; a Locked document is carried through

**Status:** accepted, in force.

## Context

An encrypted file that pdf0 cannot decrypt — a wrong or missing password, a
security handler it does not implement, a malformed `/Encrypt` dictionary — is
still a structurally valid PDF. Failing `Read` would lose everything that does
not need the plaintext: the page count, the object graph, the trailer, the
file's own revisions, and the ability to write it back unchanged. It would also
fold three different failures, each with its own remedy, into one error.

## Decision

An `/Encrypt` dictionary never makes `Read` fail (b99b483, 2026-09-22). A file
pdf0 cannot decrypt is parsed structurally with its strings and streams left as
ciphertext, and the document is Locked: `Document.Locked` reports it, and
`LockReason` says why, wrapping `ErrWrongPassword`, `ErrEncryptionUnsupported`
or `ErrEncryptionMalformed` (7be09aa, 2026-07-27).

`Write` carries a Locked document through: the ciphertext is written unchanged
under its preserved `/Encrypt` and `/ID`, and the file's layout is regenerated
(c434c8f, 2026-07-14). It refuses when the `/Encrypt` dictionary does not
resolve, so the encryption state is unknown, and when an object stream failed to
decode, so objects would be dropped.

## Consequences

- Every caller that reads content must check `Locked` first. On a Locked
  document the validators and extractors see ciphertext, `RemoveEncryption` is a
  no-op and `SetEncryption` refuses; the CLI refuses every content command on a
  Locked file, and its `info` reports `locked: true`.
- A missing password and a wrong one are distinguishable, and encryption pdf0
  cannot open is never blamed on the password.
- Round-tripping a file pdf0 cannot decrypt is lossless in content, not in
  bytes: the layout is pdf0's.
