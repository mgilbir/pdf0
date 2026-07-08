// Package pdf0 is a dependency-free PDF 2.0 parser, serializer, and PDF/A
// validator.
//
// It offers four things:
//
//   - Read parses PDF bytes into a typed object model (see Document), recovering
//     from common malformations rather than crashing on hostile input.
//   - Document.Write serializes the object model back to conformant PDF bytes.
//   - ValidatePDFA and ValidatePDFABytes check a document against PDF/A
//     conformance levels (PDF/A-1b, -2b, -3b, and -4).
//   - NewPDFADocument (and NewPDFADocumentWithInfo) build a minimal PDF/A
//     document.
//
// # Reading and writing
//
//	doc, err := pdf0.Read(bytes.NewReader(data), int64(len(data)))
//	if err != nil { /* malformed beyond recovery */ }
//	var out bytes.Buffer
//	err = doc.Write(&out)
//
// Encrypted files are parsed structurally but not decrypted: Document.Encrypted
// reports the presence of an /Encrypt dictionary, strings and streams stay in
// their encrypted form, and Write refuses such documents. Write always emits a
// traditional cross-reference table, regenerating the on-disk layout.
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
// See docs/architecture.md for how bytes flow through Read, Write, and the
// validation pipeline.
package pdf0
