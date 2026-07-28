package pdf0

import (
	"bytes"
	"encoding/binary"
	"github.com/mgilbir/formalis"
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
		_ = formalis.Validate(fx.XML, fx.Profile)
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

// --- TrueType cmap ---
//
// FuzzRead reaches parseCmapSubtable only through a valid-enough PDF carrying a
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

// checkCmapInvariants asserts everything parseCmapSubtable promises about a map
// it returns, whatever the subtable claimed:
//
//   - a returned map is never empty (nil means "unreadable, or maps nothing";
//     an empty non-nil map would tell trueTypeGID the font maps no character at
//     all, i.e. that every code is .notdef);
//   - it never exceeds the work budget, so a table claiming four billion groups
//     cannot turn into an unbounded allocation;
//   - every key is a Unicode code point and every value is a real glyph index —
//     never 0, which means unmapped and must not be recorded as a mapping.
func checkCmapInvariants(t *testing.T, what string, m map[rune]int, emptyAllowed bool) {
	t.Helper()
	if m == nil {
		return
	}
	if len(m) == 0 && !emptyAllowed {
		t.Fatalf("%s: returned an empty non-nil map; unreadable subtables must return nil", what)
	}
	budget := maxCmapFormat4Work
	if maxCmapFormat12Work > budget {
		budget = maxCmapFormat12Work
	}
	if len(m) > budget {
		t.Fatalf("%s: %d entries exceeds the work budget of %d", what, len(m), budget)
	}
	for r, gid := range m {
		if r < 0 || r > 0x10FFFF {
			t.Fatalf("%s: key U+%X is not a Unicode code point", what, r)
		}
		if gid <= 0 || gid > 0xFFFF {
			t.Fatalf("%s: key U+%X maps to glyph %d, outside 1..0xFFFF", what, r, gid)
		}
	}
}

// cmapSubtableSeeds returns one subtable of every shape the hand-written tests
// cover: each supported format, the budget-tripping tables, the truncated and
// malformed variants, and a few formats the parser does not handle.
func cmapSubtableSeeds() [][]byte {
	// Disjoint groups covering the whole of Unicode: ~1.1M mappings if the
	// budget does not stop it.
	var wideOpen [][3]uint32
	for start := uint32(0); start <= 0x10FFFF; start += 4096 {
		wideOpen = append(wideOpen, [3]uint32{start, start + 4095, 1})
	}
	// Sixty-four groups each spanning all of Unicode: 71M iterations unbudgeted.
	overlapping := make([][3]uint32, 64)
	for i := range overlapping {
		overlapping[i] = [3]uint32{0, 0x10FFFF, 1}
	}
	// Format 4 with many segments each spanning almost the whole BMP.
	var wideSegs [][3]int
	for i := 0; i < 400; i++ {
		wideSegs = append(wideSegs, [3]int{1, 0xFFFE, 0})
	}

	format12 := buildCmapFormat12([][3]uint32{{0x41, 0x42, 7}})
	overstated := append([]byte(nil), format12...)
	binary.BigEndian.PutUint32(overstated[12:], 1<<20) // nGroups it does not carry
	longLength := append([]byte(nil), format12...)
	binary.BigEndian.PutUint32(longLength[4:], uint32(len(longLength))+1)

	seeds := [][]byte{
		// Format 0: byte encoding, plus the truncated form.
		buildCmapFormat0(map[byte]byte{0x00: 3, 0x41: 4, 0xFF: 5}),
		buildCmapFormat0(nil),
		make([]byte, 100),
		// Format 4: segment at code 0, terminal segment, inverted segment,
		// sentinel alone, and the budget-tripper.
		buildCmapFormat4([][3]int{{0x0000, 0x0002, 100}, {0x0041, 0x0042, 200}, {0xFFFF, 0xFFFF, 1}}),
		buildCmapFormat4([][3]int{{0xFFFE, 0xFFFF, 0x8000}}),
		buildCmapFormat4([][3]int{{0x0050, 0x0040, 300}, {0x0041, 0x0041, 200}}),
		buildCmapFormat4([][3]int{{0xFFFF, 0xFFFF, 1}}),
		buildCmapFormat4(wideSegs),
		// Format 6: trimmed table, and one running past the BMP.
		buildCmapFormat6(0x41, []int{7, 8, 9}),
		buildCmapFormat6(0xFFFE, []int{7, 8, 9, 10}),
		// Format 12: ordinary groups, astral groups, malformed groups, the two
		// budget-trippers, an empty table, and the truncated variants.
		format12,
		buildCmapFormat12([][3]uint32{{0x0000, 0x0002, 100}, {0x0041, 0x0043, 200}, {0x0100, 0x0100, 0}}),
		buildCmapFormat12([][3]uint32{{0xFFFE, 0x10001, 900}, {0x1F600, 0x1F601, 1000}}),
		buildCmapFormat12([][3]uint32{{0x50, 0x40, 300}, {0x110000, 0x110002, 400}, {0x60, 0x60, 0x10000}, {0x41, 0x41, 200}}),
		buildCmapFormat12(wideOpen),
		buildCmapFormat12(overlapping),
		buildCmapFormat12(nil), // maps nothing: the FuzzCmapSubtable finding
		// The same finding as the fuzzer minimised it, kept verbatim because
		// testdata/fuzz is gitignored and this is the only place the regression
		// can live: one group, [0x30303030, 0x30303030] → 0x30303030, entirely
		// outside Unicode, so every mapping is skipped and the map comes back
		// empty. Its shape is not the one a human would have written down.
		[]byte("\x00\x0c00\x00\x00\x00\x1c0000\x00\x00\x00\x01000000000000"),
		format12[:len(format12)-4],
		format12[:12],
		overstated,
		longLength,
		// Formats the parser does not handle, and degenerate input.
		{0, 2, 0, 0}, {0, 13, 0, 0}, {0, 14, 0, 0}, {0, 8}, {0},
		{},
	}
	return seeds
}

// FuzzCmapSubtable fuzzes parseCmapSubtable on raw subtable bytes — the deep
// target, straight at the parser that reads attacker-controlled binary out of an
// embedded font program. It asserts no panic and the invariants in
// checkCmapInvariants: the budget holds, the map is nil rather than empty, and
// every key and value is in range.
func FuzzCmapSubtable(f *testing.F) {
	for _, s := range cmapSubtableSeeds() {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		m, partial := parseCmapSubtable(data)
		checkCmapInvariants(t, "parseCmapSubtable", m, false)
		// A partial result is only meaningful alongside a map: reporting "this
		// is a prefix" while returning nothing would leave a consumer unable to
		// tell "truncated" from "unreadable", which is the distinction the flag
		// exists to draw.
		if partial && m == nil {
			t.Fatalf("parseCmapSubtable reported partial with a nil map")
		}
	})
}

// sfntCmapSeeds returns fonts carrying several cmap subtables at once, so the
// fuzzer starts from inputs where subtable ranking actually has a choice to make.
func sfntCmapSeeds() [][]byte {
	type sub = struct {
		plat, enc int
		data      []byte
	}
	bmp := buildCmapFormat4([][3]int{{0x0041, 0x0041, 100 - 0x41}, {0xFFFF, 0xFFFF, 1}})
	full := buildCmapFormat12([][3]uint32{{0x0041, 0x0041, 500}, {0x1F600, 0x1F600, 600}})
	mac := buildCmapFormat0(map[byte]byte{0x41: 9})
	symbol := buildCmapFormat4([][3]int{{0xF041, 0xF041, 12}, {0xFFFF, 0xFFFF, 1}})

	var seeds [][]byte
	for _, subs := range [][]sub{
		{{3, 1, bmp}, {3, 10, full}},
		{{3, 10, full}, {3, 1, bmp}},
		{{1, 0, mac}, {3, 1, bmp}},
		{{3, 0, symbol}, {1, 0, mac}},
		{{0, 4, full}},
		{{0, 0, bmp}, {0, 6, full}, {3, 1, bmp}},
		{{3, 1, bmp}, {3, 10, make([]byte, 16)}},       // unreadable preferred subtable
		{{3, 1, bmp}, {3, 10, buildCmapFormat12(nil)}}, // preferred subtable maps nothing
		{{3, 10, buildCmapFormat12(nil)}, {3, 1, bmp}}, // …in the other order
		{{3, 1, bmp}, {3, 10, bmp}, {0, 3, mac}, {1, 0, mac}},
		nil,
	} {
		seeds = append(seeds, buildSFNTWithCmapSubtables(subs))
	}
	// Not an sfnt at all, and a header claiming tables it does not carry.
	seeds = append(seeds, []byte("%PDF-2.0\n"), []byte{0, 1, 0, 0, 0xFF, 0xFF, 0, 0, 0, 0, 0, 0})
	return seeds
}

// FuzzSFNTCmap fuzzes parseSFNT, the smallest entry point that exercises
// subtable *selection*: the (3,10) > (3,1) > (0,x) ranking added with format 12
// runs on attacker-controlled platform ids, encoding ids and offsets, and picks
// which of several subtables becomes the font's authoritative cmap. Whatever it
// picks must satisfy the same invariants as a single subtable, and the derived
// symbol and Mac maps must carry only real glyph indices. It also drives
// trueTypeGID over the resulting font, since that is what the PDF/A rules do
// with it.
func FuzzSFNTCmap(f *testing.F) {
	for _, s := range sfntCmapSeeds() {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		fp := parseSFNT(data)
		if fp == nil {
			return
		}
		checkCmapInvariants(t, "parseSFNT cmap", fp.cmap, false)
		// The symbol and Mac maps are narrowed from a subtable by dropping the
		// codes that do not fit their key type, so they may legitimately end up
		// empty; every lookup on them is guarded by the comma-ok, so an empty one
		// is not the standing "everything is .notdef" claim that fp.cmap is.
		symbol := make(map[rune]int, len(fp.symbolCmap))
		for c, gid := range fp.symbolCmap {
			symbol[rune(c)] = gid
		}
		checkCmapInvariants(t, "parseSFNT symbolCmap", symbol, true)
		mac := make(map[rune]int, len(fp.macCmap))
		for c, gid := range fp.macCmap {
			mac[rune(c)] = gid
		}
		checkCmapInvariants(t, "parseSFNT macCmap", mac, true)
		for _, code := range []byte{0, 'A', 0xFF} {
			for _, symbolic := range []bool{false, true} {
				_, _ = trueTypeGID(fp, symbolic, code, "A")
				_, _ = trueTypeGID(fp, symbolic, code, "")
			}
		}
	})
}
