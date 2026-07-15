package pdf0

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// fuzzSeeds returns a spread of valid documents to seed the corpus: the two
// structural builders, their AES-256-encrypted forms (so the fuzzer explores the
// decrypt/re-encrypt paths, historically the most bug-prone), a few degenerate
// headers, and any reference PDFs present on disk (gitignored, so only a
// convenience for local runs).
func fuzzSeeds() [][]byte {
	seeds := [][]byte{
		buildMinimalPDF(),
		buildXRefStreamPDF(),
		encryptSeed(buildMinimalPDF()),
		encryptSeed(buildXRefStreamPDF()),
		[]byte("%PDF-2.0\n%\x80\x80\x80\x80\nstartxref\n0\n%%EOF"),
		[]byte("%PDF-2.0\n"),
		{},
	}
	for _, p := range fuzzReferencePDFs() {
		if data, err := os.ReadFile(p); err == nil {
			seeds = append(seeds, data)
		}
	}
	return seeds
}

// encryptSeed returns base encrypted with the standard AES-256 handler, or base
// unchanged if it cannot be built (seeds must never fail the fuzz setup).
func encryptSeed(base []byte) []byte {
	doc, err := Read(bytes.NewReader(base), int64(len(base)))
	if err != nil {
		return base
	}
	if err := doc.SetEncryption("", ""); err != nil {
		return base
	}
	var buf bytes.Buffer
	if doc.Write(&buf) != nil {
		return base
	}
	return buf.Bytes()
}

func fuzzReferencePDFs() []string {
	m, _ := filepath.Glob("testdata/pdf20examples/*.pdf")
	return m
}

// exercise runs every consumer of a parsed document that processes untrusted
// input. Read recovers panics internally, but these do not — a panic here is a
// robustness bug. Results are discarded; the fuzzer only cares about crashes.
func exercise(doc *Document, data []byte) {
	_ = doc.PageCount()
	_ = ValidatePDFUA(doc)
	for _, lvl := range []PDFALevel{PDFA1b, PDFA2b, PDFA3b, PDFA4} {
		_ = ValidatePDFABytes(doc, lvl, data)
	}
	_ = ValidatePDFX(doc, PDFX4)
	_ = ValidatePDFVT(doc)
	_ = ValidateDParts(doc)
	if fx := ValidateFacturX(doc, data); len(fx.XML) > 0 {
		_ = ValidateFacturXInvoice(fx.XML, fx.Profile)
	}
	var buf bytes.Buffer
	_ = doc.Write(&buf)
}

// FuzzRead asserts that Read never panics on arbitrary input, and that any
// document it returns survives every validator and Write without panicking. The
// validators other than PDF/A have no internal panic recovery, so this is their
// primary crash-safety net.
func FuzzRead(f *testing.F) {
	for _, s := range fuzzSeeds() {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		doc, err := Read(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return
		}
		if doc == nil {
			t.Fatal("Read returned a nil document and a nil error")
		}
		exercise(doc, data)
	})
}

// FuzzRoundTrip asserts the serializer's core invariants: whatever Read accepts
// and Write emits must read back cleanly and losslessly. Specifically, a written
// file must re-parse without error, must not leave any object stream undecodable,
// and must not drop objects. These catch the serializer and encryption data-loss
// classes (a file pdf0 writes but cannot read back; an object stream that fails
// to inflate on the next read; objects vanishing on round-trip) that stream-
// length normalisation on malformed input does not, so no benign-length filter is
// needed here.
func FuzzRoundTrip(f *testing.F) {
	for _, s := range fuzzSeeds() {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		doc, err := Read(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return
		}
		var buf bytes.Buffer
		if err := doc.Write(&buf); err != nil {
			return // legitimately unwritable (reserved object 0, broken object stream, …)
		}
		out := buf.Bytes()
		doc2, err := Read(bytes.NewReader(out), int64(len(out)))
		if err != nil {
			t.Fatalf("wrote a file that cannot be re-read: %v", err)
		}
		if len(doc2.brokenObjStms) > 0 {
			t.Fatalf("re-read of written output has undecodable object stream(s): %v", doc2.brokenObjStms)
		}
		if len(doc2.Objects) < len(doc.Objects) {
			t.Fatalf("objects lost on round-trip: %d written, %d read back", len(doc.Objects), len(doc2.Objects))
		}
	})
}
