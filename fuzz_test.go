package pdf0

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"github.com/mgilbir/pdf0/sign"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mgilbir/formalis"
	"github.com/mgilbir/pdf0/pdfa"
	"github.com/mgilbir/pdf0/pdfx"
)

// fuzzSeeds returns a spread of valid documents to seed the corpus: the two
// structural builders, their AES-256-encrypted forms (so the fuzzer explores the
// decrypt/re-encrypt paths, historically the most bug-prone), a few degenerate
// headers, a signed document (so signature verification has a /ByteRange and a
// CMS blob to mutate), every hostile extraction input the audits have built
// (hostileExtractionInputs — the shapes that have broken extraction before,
// generated here rather than committed), and any reference PDFs present on disk
// (gitignored, so only a convenience for local runs).
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
	if s := signedSeed(); s != nil {
		seeds = append(seeds, s)
	}
	for _, in := range hostileExtractionInputs() {
		seeds = append(seeds, in.build())
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
	// An empty user password, so FuzzRead's password-less Read decrypts the
	// seed; SetEncryption refuses that, so install it directly.
	if err := setOpenEncryption(doc); err != nil {
		return base
	}
	var buf bytes.Buffer
	if doc.Write(&buf) != nil {
		return base
	}
	return buf.Bytes()
}

// signedSeed is the minimal document signed with a throwaway self-signed key,
// or nil if it cannot be built (seeds must never fail the fuzz setup).
func signedSeed() []byte {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "fuzz seed"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil
	}
	base := buildMinimalPDF()
	doc, err := Read(bytes.NewReader(base), int64(len(base)))
	if err != nil {
		return nil
	}
	var buf bytes.Buffer
	if doc.WriteSigned(&buf, cert, key) != nil {
		return nil
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
//
// The extractors are here since the 2026-09-22 audit, which found four crashes
// in them (C11, C13, C15, C16) that the fuzzer had never been pointed at. They
// now recover per image and per page, so a crash inside them would not reach
// the fuzzer as a panic; what the fuzzer can still see is a recovered one, so
// exercise fails on it — the recover is defence in depth, and every panic
// behind it is a bug to fix at its source.
func exercise(t *testing.T, doc *Document, data []byte) {
	t.Helper()
	_ = doc.PageCount()
	if _, err := doc.ExtractText(); err != nil {
		if s := err.Error(); containsInternal(s) {
			t.Fatalf("text extraction recovered a panic: %v", err)
		}
	}
	for im := range doc.Images() {
		if containsInternal(im.Note) {
			t.Fatalf("image extraction recovered a panic: %s", im.Note)
		}
	}
	// Signature verification reads the file the document was read from, and
	// its error means only one thing: a panic it recovered.
	if _, err := doc.VerifySignatures(sign.VerifyOptions{}); err != nil {
		t.Fatalf("signature verification recovered a panic: %v", err)
	}
	if _, err := doc.ValidatePAdES(sign.VerifyOptions{}); err != nil {
		t.Fatalf("PAdES validation recovered a panic: %v", err)
	}
	_ = ValidatePDFUA(doc)
	for _, lvl := range []pdfa.Level{pdfa.PDFA1b, pdfa.PDFA2b, pdfa.PDFA3b, pdfa.PDFA4} {
		_ = ValidatePDFABytes(doc, lvl, data)
	}
	_ = ValidatePDFX(doc, pdfx.PDFX4)
	_ = ValidatePDFVT(doc)
	_ = ValidateDParts(doc)
	if fx := ValidateFacturX(doc, data); len(fx.XML) > 0 {
		_, _ = formalis.Validate(context.Background(), fx.XML, fx.Profile)
	}
	_ = ValidateOrderX(doc, data)
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
		doc, err := Read(bytes.NewReader(data), int64(len(data)), fuzzLimits...)
		if err != nil {
			return
		}
		if doc == nil {
			t.Fatal("Read returned a nil document and a nil error")
		}
		exercise(t, doc, data)
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

// --- TrueType cmap ---
//
// FuzzRead reaches font.ParseCmapSubtable only through a valid-enough PDF carrying a
// valid-enough embedded sfnt carrying a cmap table, which no random mutation is
// going to assemble. The two targets below hand the parser its bytes directly:
// one the subtable, one the whole font, so that subtable *selection* — the
// (3,10) > (3,1) > (0,x) ranking, which reads attacker-supplied platform and
// encoding ids and offsets — is fuzzed as well as subtable parsing.
//
// Neither target asserts a wall-clock bound. A time limit inside a fuzz target
// is a flake: workers run in parallel on a loaded machine, seed replay runs
// under -race, and the threshold that never fires spuriously is so high it no
// longer distinguishes "slow" from "hung". What the budgets actually promise is
// bounded *work*, and the deterministic proxy for that is the size of the map
// they hand back, which is asserted below; a genuine hang still surfaces, as the
// test binary's own -timeout. The fixed-input timing assertions stay where they
// can be made reliably, in TestCmapFormat4Budget and TestCmapFormat12Budget.

// checkCmapInvariants asserts everything font.ParseCmapSubtable promises about a map
// it returns, whatever the subtable claimed:
//
//   - a returned map is never empty (nil means "unreadable, or maps nothing";
//     an empty non-nil map would tell font.TrueTypeGID the font maps no character at
//     all, i.e. that every code is .notdef);
//   - it never exceeds the work budget, so a table claiming four billion groups
//     cannot turn into an unbounded allocation;
//   - every key is a Unicode code point and every value is a real glyph index —
//     never 0, which means unmapped and must not be recorded as a mapping.

// fuzzLimits bound what one fuzz input may cost, so that the fuzz body cannot
// run unbounded.
//
// It cannot run under internal/hostile: hostile.Run re-executes the test
// binary for the named test, and a fuzz worker's input is not something the
// child can be handed — it would run the seed corpus instead, and a
// subprocess per input would slow fuzzing a thousandfold besides. So the body
// bounds itself: every budget is set well below its default, so that a
// compression bomb or a hostile geometry trips a guard in milliseconds rather
// than spending the default's hundreds of megabytes, and each worker stays
// small. The fuzz process as a whole runs under a memory-capped cgroup (see
// docs/testing.md), which catches what a budget misses.
//
// Tripping a guard is an outcome the fuzzer should explore — the trip paths
// are code too — so the budgets are small, not tiny.
var fuzzLimits = []Option{
	WithMaxDecodedStreamBytes(8 << 20),
	WithMaxDecodedContentBytes(32 << 20),
	WithMaxObjectStreamBytes(32 << 20),
	WithMaxContentStreamBytes(8 << 20),
	WithMaxICCProfileBytes(1 << 20),
	WithMaxXMPPacketBytes(1 << 20),
	WithMaxImagePixels(1 << 22),
}

// containsInternal reports whether a message is a recovered panic from the
// extractors.
func containsInternal(s string) bool {
	return bytes.Contains([]byte(s), []byte("internal error"))
}
