// Package pdf0 reads, validates, builds, signs and writes PDF files, in pure
// Go.
//
// Read takes a file of any PDF version into one object model, the one ISO
// 32000-2 (PDF 2.0) defines, and Document.Write serializes it back. Around that
// core the package validates conformance (PDF/A, PDF/UA, PDF/X, PDF/VT, PDF/R,
// DPart hierarchies and the Factur-X and Order-X invoice containers), builds
// new documents with text, fonts and images, signs and verifies digital
// signatures, encrypts and decrypts with the standard security handler, and
// extracts text and images. The htmlpdf package renders HTML and CSS into a
// document.
//
// Its dependencies are the author's own modules (forme for text shaping, font
// programs and the HTML/CSS layout engine, formalis for EN 16931 invoice rules,
// golittlecms for ICC profiles, gopenjpeg for JPEG 2000) and one from the Go
// project, golang.org/x/text, whose Unicode normalisation SASLprep needs to
// prepare PDF 2.0 passwords.
//
// # Reading and writing
//
//	doc, err := pdf0.Read(bytes.NewReader(data), int64(len(data)))
//	if err != nil {
//		return err // too little left to build a document from
//	}
//	var out bytes.Buffer
//	err = doc.Write(&out)
//
// Read is built for untrusted input. It never panics on a malformed file, and
// it repairs the damage it can work around — a cross-reference table with
// wrong offsets, a stream whose /Length is wrong — rather than refusing the
// file, so a nil error means the file was parsed, not that it is well formed.
// Write regenerates the file's structure from the object model: the header,
// the object layout, the cross-reference section and the trailer. It refuses,
// before writing anything, a document it cannot represent faithfully. It does
// not make a document conformant: a document with no /ID, which PDF 2.0
// requires, is written without one (NewDocument starts a document that has
// one).
//
// Document.ExtractText and Document.ImagesContext read content out,
// Document.ExtractPages and Document.AppendPages move pages between documents,
// Document.WriteIncremental appends an update to the file a document was read
// from, and Document.Repair fixes common PDF/A failures in place.
//
// # Validating
//
//	for _, e := range pdf0.ValidatePDFA(doc, pdfa.PDFA4) {
//		fmt.Println(e) // e.g. [PDF/A-4 6.2.10.4.1] object 7: font Helvetica must be embedded (no FontFile/FontFile2/FontFile3 in descriptor)
//	}
//
// The validators are free functions in this package taking the *Document
// first: ValidatePDFA, ValidatePDFUA, ValidatePDFUA2, ValidatePDFX,
// ValidatePDFVT, ValidatePDFVT2, ValidatePDFR and ValidateDParts each return a
// slice of findings whose type lives in the standard's own package
// (pdfa.Violation, pdfua.Violation and so on). Every finding type satisfies
// the Violation interface, so findings from different validators can be
// collected together. ValidateFacturX and ValidateOrderX return a result
// struct instead, because they also carry the extracted invoice XML, the
// conformance level and what the invoice rule engine did not evaluate, but the
// findings inside it satisfy Violation like the rest.
//
// An empty result means no implemented check fired, not a guarantee of full
// conformance: each validator covers a subset of its standard. A result may
// also hold checker findings, under the reserved rules "limit" (a check could
// not finish: a resource limit, a cancelled context, content pdf0 cannot read)
// and "internal" (a check panicked on hostile input and was recovered rather
// than crashing the caller). IsCheckerFinding tells them apart from a real
// non-conformance; read one as "unknown", never as a failure. The byte-level
// file-structure checks (e.g. no data after %%EOF) read the file the document
// was read from (Document.Source); a document built in memory has none, and
// its result carries a checker finding saying they did not run.
//
// Validation does not mutate its Document and is safe to run concurrently on
// the same Document. Every validator returns its findings in a deterministic
// order (by rule, then object, then message). A stack overflow from unbounded
// recursion is fatal and cannot be recovered, so no walk over the document
// recurses without a bound: each is iterative, or held to one depth guard
// (1024 levels) that stops the run and reports the "limit" rule (guard
// "walk-depth"), and a lint test (internal/lint) fails for a recursion over
// the object graph that is neither.
//
// # Building documents
//
// NewDocument starts an empty PDF 2.0 document, and AddPage adds a page drawn
// with a content.Builder. A face from the fonts package sets text, and naming
// it in Page.Faces embeds it, subset to the glyphs the page set:
//
//	doc := pdf0.NewDocument()
//	face, err := fonts.NotoSans() // bundled with the module
//	if err != nil {
//		return err
//	}
//	var page content.Builder
//	page.BeginText().SetFont("F1", 24).MoveText(72, 720)
//	face.DrawShaped(&page, "Hello, world", 24)
//	page.EndText()
//	if _, err := doc.AddPage(pdf0.Page{
//		Width: 595, Height: 842, Content: &page,
//		Faces: map[object.Name]*fonts.Face{"F1": face},
//	}); err != nil {
//		return err
//	}
//	return doc.Save(w)
//
// images.Embed adds an image XObject, which a page names in Page.XObjects and
// draws with content.Builder.Draw. NewPDFADocument starts a document that
// claims a PDF/A level instead, and Document.Save writes a document only if it
// passes the level it claims; one that claims none is written as Write would
// write it.
//
// # Signatures
//
// Document.WriteSigned signs a document, Document.WriteSignedIncremental adds a
// signature to a signed one, and Document.WriteArchivalTimestamp appends
// long-term validation material and a document time-stamp.
// Document.VerifySignatures reports one sign.Result per signature and document
// time-stamp, verified against the file the document was read from. Read the
// integrity verdict with sign.Result.Intact — Valid, and every change made
// after signing a permitted one (see sign.Result.ChangesAllowed) — or
// sign.Result.DocumentUnmodified when nothing may have changed at all: Valid
// alone accepts a document whose content was changed by a post-signing
// incremental update. Trust comes only from the roots passed in
// sign.VerifyOptions: with none, TrustedChain is always false. Pass the roots
// of the signers you accept, never a web PKI pool such as x509.SystemCertPool.
//
// # Encryption
//
// Encrypted files using the standard security handler are decrypted on Read
// when the (empty or supplied) user or owner password is correct: RC4 and
// AES-128 at revisions 2-4, and AES-256 at revision 6. Revision 5 is a
// deprecated draft and is rejected. ReadWithPassword supplies a password.
// Document.Encrypted reports the presence of an /Encrypt dictionary; a
// decrypted document retains its file key and re-encrypts on Write, so the
// object model round-trips — though the bytes do not, because AES draws a
// fresh random initialisation vector per object on every write.
// Document.RemoveEncryption drops the encryption so Write emits plaintext, and
// Document.SetEncryption encrypts a document with AES-256.
//
// A document whose scheme or password could not be handled stays encrypted:
// Document.Locked reports this, and Document.LockReason says why — a wrong
// password, an unsupported scheme, or a malformed /Encrypt dictionary. An
// /Encrypt dictionary never makes Read fail. Writing a Locked document carries
// its ciphertext unchanged under the preserved /Encrypt and /ID, and the file
// layout is regenerated.
//
// # Resource limits
//
// pdf0 parses untrusted input, so nearly every unbounded loop and every
// allocation sized by a number the file supplies is capped, and the defaults
// are safe for hostile input: a caller who configures nothing needs to do
// nothing. The caps a caller has a reason to change are settable per document,
// as variadic Option values on Read, ReadWithPassword, their Context variants
// and ParseXRefStream (see WithMaxDecodedStreamBytes and the other With*
// functions). They resolve once and are stored on the Document, so validation
// and extraction inherit whatever Read was given, and two documents read with
// different limits never interfere. Where a real-world maximum has been
// measured, the option's documentation gives it, and a cap set below it
// refuses real documents. These are stricter than the defaults and still above
// those maxima:
//
//	doc, err := pdf0.Read(r, size,
//		pdf0.WithMaxDecodedStreamBytes(48<<20),
//		pdf0.WithMaxDecodedContentBytes(256<<20),
//	)
//
// When a cap stops a validator, the trip is reported as a finding under the
// rule "limit"; when one stops an extraction or a stream decode, the error
// matches ErrResourceLimit. A run also has a work budget, WithMaxWork: every
// walk over the document, expansion, tokenisation and function evaluation of
// one validation or one extraction is charged to one meter, whose default
// grows with the size of the file. Every other limit bounds one unit of work —
// one stream, one range, one evaluation — and the meter bounds their product,
// which is what a small hostile file controls. A validation that spends it
// stops, reports the "limit" rule (guard "work"), and never reports a finding
// from the check it stopped in. See
// https://github.com/mgilbir/pdf0/blob/main/docs/limits.md.
//
// # Cancellation
//
// The work a document can cost is set by the document, not by the caller: a
// 71 MB, 1256-page file takes about ten seconds to validate. The long-running
// entry points therefore have Context variants, named for the call they wrap:
// for example ReadContext, Document.WriteContext, Document.SaveContext,
// ValidatePDFAContext, Document.ExtractTextContext, Document.ImagesContext
// and Document.StreamDataContext. The complete table is in
// https://github.com/mgilbir/pdf0/blob/main/docs/untrusted-input.md.
//
//	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
//	defer cancel()
//	for _, e := range pdf0.ValidatePDFAContext(ctx, doc, pdfa.PDFA4) {
//		fmt.Println(e)
//	}
//
// A context is a first parameter rather than a With* Option deliberately. An
// Option is stored on the Document and inherited by every later call, which is
// the wrong lifetime for a context and would make cancellation invisible at the
// call site: limits describe what a document may cost, contexts describe how
// long an operation may take. An entry point whose cost is bounded rather than
// document-scale, such as ExtractPageText (one page) or
// Document.VerifySignatures, has no variant.
//
// A cancelled validation returns the findings it had gathered plus one under the
// rule "limit", the same channel a tripped resource cap uses, so a cancelled run
// can never be mistaken for a clean bill of health. The Context variants of
// Read, Write and the extractors have no finding channel, so they return an
// error wrapping ctx.Err() instead. Cancellation is checked at coarse
// boundaries — per check, per page, per content stream, per megabyte scanned
// or decompressed — and inside every check, wherever the run's work meter is
// charged, which on that 71 MB file takes effect within about 60 ms.
//
// # Further reading
//
// https://github.com/mgilbir/pdf0/blob/main/docs/README.md indexes the
// documentation: architecture.md for how bytes flow through Read and Write,
// validators.md for the validator family, signing.md for signing and
// verification, and text.md for setting text.
package pdf0
