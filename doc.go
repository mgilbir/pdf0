// Package pdf0 is a PDF 2.0 parser, serializer, and PDF/A validator. Its only
// dependencies are the author's own pure-Go modules (formalis for EN 16931
// invoice rules, golittlecms for ICC profiles, gopenjpeg for JPEG 2000).
//
// The core is four entry points:
//
//   - Read parses PDF bytes into a typed object model (see Document), recovering
//     from common malformations rather than crashing on hostile input.
//   - Document.Write serializes the object model back to conformant PDF bytes.
//   - ValidatePDFA and ValidatePDFABytes check a document against PDF/A
//     conformance levels: PDF/A-1a, -1b, -2a, -2b, -3a, -3b, and -4. The Level A
//     levels are Level B plus the accessibility requirements.
//   - NewPDFADocument (and NewPDFADocumentWithInfo) build a minimal PDF/A
//     document.
//
// Built on those: encryption (ReadWithPassword, Document.SetEncryption,
// Document.RemoveEncryption), digital signatures (Document.WriteSigned,
// Document.VerifySignatures, Document.ValidatePAdES), extraction
// (Document.ExtractText, Document.ExtractImages, Document.Images), page
// operations (Document.ExtractPages, Document.AppendPages), conformance repair
// (Document.Repair), incremental writing (Document.WriteIncremental), and nine
// conformance validators besides PDF/A.
//
// # Reading and writing
//
//	doc, err := pdf0.Read(bytes.NewReader(data), int64(len(data)))
//	if err != nil { /* malformed beyond recovery */ }
//	var out bytes.Buffer
//	err = doc.Write(&out)
//
// # Resource limits
//
// pdf0 parses untrusted input, so every unbounded loop and every allocation
// sized by a number the file supplies is capped. The defaults are safe for
// hostile input and no real file measured across the veraPDF corpus or a
// 978-file Common Crawl sample comes within 2x of any of them, so a caller who
// configures nothing needs to do nothing.
//
// Eleven of those caps are settable per document, as variadic Option values on
// Read, ReadWithPassword, their Context variants and ParseXRefStream (see
// WithMaxDecodedStreamBytes and the other With* functions). They resolve once
// and are stored on the Document, so validation and extraction inherit whatever
// Read was given, and two documents read with different limits never interfere:
//
//	doc, err := pdf0.Read(r, size,
//		pdf0.WithMaxDecodedStreamBytes(8<<20),
//		pdf0.WithMaxDecodedContentBytes(64<<20),
//	)
//
// When a cap does stop a check, the validators say so rather than guess: the
// trip is reported as a finding under the rule identifier "limit", which
// IsCheckerFinding separates from a real non-conformance. A caller asking "is
// this file conformant?" should read such a finding as "unknown", never as a
// failure.
//
// # Cancellation
//
// The work a document can cost is set by the document, not by the caller: a
// 71 MB, 1256-page file takes about ten seconds to validate. The long-running
// entry points therefore have Context variants — ReadContext,
// ReadWithPasswordContext, Document.WriteContext, ValidatePDFAContext,
// ValidatePDFABytesContext, ValidatePDFUAContext, ValidatePDFUA2Context,
// ValidatePDFXContext, ValidatePDFVTContext, ValidatePDFVT2Context,
// ValidatePDFRContext, ValidateDPartsContext, ValidateFacturXContext,
// ValidateOrderXContext, Document.ExtractTextContext and
// Document.ExtractImagesContext:
//
//	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
//	defer cancel()
//	for _, e := range pdf0.ValidatePDFAContext(ctx, doc, pdf0.PDFA4) { ... }
//
// A context is a first parameter rather than a With* Option deliberately. An
// Option is stored on the Document and inherited by every later call, which is
// the wrong lifetime for a context and would make cancellation invisible at the
// call site; limits describe what a document may cost, contexts describe how
// long an operation may take. Every original signature is unchanged, and an
// entry point whose cost is bounded rather than document-scale — ExtractPageText
// (one page), Images (an iterator the caller can break out of),
// Document.VerifySignatures — deliberately has no variant. ValidateFacturX and
// ValidateOrderX had none until formalis v0.2.0, for reasons that have both
// lapsed: their findings were formalis.Violation values, which IsCheckerFinding
// could not classify, and the invoice half of the work was a rule engine that
// took no context. The findings are now this package's own types and the engine
// takes a context, so both halves of a Factur-X validation are cancellable and a
// cancelled run reports the reserved "limit" rule like every other. See
// docs/architecture.md#cancellation for the full table.
//
// A cancelled validation returns the findings it had gathered plus one under the
// rule "limit", the same channel a tripped resource cap uses, so a cancelled run
// can never be mistaken for a clean bill of health. Read, Write and the
// extractors have no finding channel, so they return an error wrapping
// ctx.Err() instead. Cancellation is checked at coarse boundaries — per check,
// per page, per content stream, per megabyte scanned or decompressed — which on
// that 71 MB file takes effect within about 60 ms.
//
// Encrypted files using the standard security handler are decrypted on Read
// when the (empty or supplied) user or owner password is correct: RC4 and
// AES-128 at revisions 2-4, and AES-256 at revision 6. Revision 5 is a
// deprecated draft and is rejected. Document.Encrypted reports the presence of
// an /Encrypt dictionary; a decrypted document retains its file key and
// re-encrypts on Write, so the object model round-trips — though the bytes do
// not, because AES draws a fresh random initialisation vector per object on
// every write. Document.RemoveEncryption drops the encryption so Write emits
// plaintext. A document whose scheme or password could not be handled stays
// encrypted (Document.Locked reports this) and is written back unchanged as a
// lossless passthrough. Write regenerates the
// on-disk layout, emitting a cross-reference stream when the source used one and
// a traditional cross-reference table otherwise.
//
// # Validating
//
//	for _, e := range pdf0.ValidatePDFA(doc, pdf0.PDFA4) {
//	    fmt.Println(e) // e.g. [PDF/A-4 6.2.10] object 12: font ... must be embedded
//	}
//
// An empty result means no implemented check fired, not a guarantee of full
// conformance: the validator covers a subset of ISO 19005. Validation does not
// mutate its Document and is safe to run concurrently on the same Document. Use
// ValidatePDFABytes when you have the raw file bytes and want the byte-level
// file-structure checks (e.g. no data after %%EOF) as well.
//
// The other PDF standards follow the same shape: each validator is a free
// function taking the *Document as its first parameter (ValidatePDFUA,
// ValidatePDFUA2, ValidatePDFX, ValidatePDFVT, ValidatePDFVT2, ValidatePDFR,
// ValidateDParts), and every finding type satisfies the Violation interface, so
// findings from different validators can be collected together. ValidateFacturX
// and ValidateOrderX differ only in shape: they return a result struct, because
// they also carry the extracted invoice XML, the conformance level and what the
// invoice rule engine did not evaluate, but the findings inside it are
// FacturXViolation and OrderXViolation values and satisfy Violation like the
// rest. See the Violation documentation.
//
// Every validator returns its findings in a deterministic order (by rule, then
// object, then message) and runs its checks under a recover boundary: a check
// that panics on hostile input is reported as a finding whose rule is
// "internal" rather than crashing the caller. A stack overflow from unbounded
// recursion is fatal and is not recoverable; those are prevented at the source.
//
// # Signatures
//
// Document.VerifySignatures reports one SignatureResult per signature. Read the
// verdict with SignatureResult.DocumentUnmodified, which is Valid AND
// CoversWholeDocument: Valid alone accepts a document whose content was changed
// by a post-signing incremental update. VerifySignatures performs no trust-chain
// check at all — use Document.VerifySignaturesWithRoots to populate TrustedChain.
//
// See docs/architecture.md for how bytes flow through Read and Write,
// docs/validators.md for the validator family, and docs/signing.md for signing
// and verification.
package pdf0
