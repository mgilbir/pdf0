package pdf0

import (
	"bytes"
	"encoding/binary"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/finding"
	"github.com/mgilbir/pdf0/internal/font"
	"strings"
	"testing"
)

// These tests pin the rule limits_report.go states: a check must never assert a
// violation on the basis of an incomplete result. Each one drives a resource
// guard to trip and asserts that the rules downstream of it decline to accuse
// the file, and that the trip is reported under the "limit" rule instead.
//
// The guards these drive are configurable (limits.go); limits_test.go covers
// the configuration itself. Here every guard is driven at its default bound
// except where a test lowers one deliberately, which is the same thing a caller
// does with a With* option.

// --- helpers ---

func hasMessage(msgs []string, sub string) bool {
	for _, m := range msgs {
		if strings.Contains(m, sub) {
			return true
		}
	}
	return false
}

func errMessages(errs []ValidationError) []string {
	out := make([]string, len(errs))
	for i, e := range errs {
		out[i] = e.Message
	}
	return out
}

// sfntTable is one table of a synthetic sfnt font program.
type sfntTable struct {
	tag  string
	data []byte
}

// buildTestSFNT assembles a minimal TrueType font program from the given
// tables. Only the table directory has to be right for parseSFNT.
func buildTestSFNT(tables []sfntTable) []byte {
	n := len(tables)
	dir := make([]byte, 12+16*n)
	binary.BigEndian.PutUint32(dir[0:], 0x00010000)
	binary.BigEndian.PutUint16(dir[4:], uint16(n))
	body := []byte{}
	off := len(dir)
	for i, t := range tables {
		rec := 12 + 16*i
		copy(dir[rec:], t.tag)
		binary.BigEndian.PutUint32(dir[rec+8:], uint32(off+len(body)))
		binary.BigEndian.PutUint32(dir[rec+12:], uint32(len(t.data)))
		body = append(body, t.data...)
	}
	return append(dir, body...)
}

// cmapTable wraps one subtable as a (3,1) Windows Unicode cmap table.
func cmapTable(sub []byte) []byte {
	hdr := make([]byte, 12)
	binary.BigEndian.PutUint16(hdr[2:], 1) // one subtable
	binary.BigEndian.PutUint16(hdr[4:], 3) // platform 3
	binary.BigEndian.PutUint16(hdr[6:], 1) // encoding 1
	binary.BigEndian.PutUint32(hdr[8:], 12)
	return append(hdr, sub...)
}

// --- the cmap work budget (limits.cmapWork, WithMaxCmapWork) ---

// budgetBustingCmap builds a format-4 subtable whose segments cost more work
// than defaultMaxCmapWork allows. The first five segments each span the whole
// BMP with a delta chosen so code 0x41 ('A') maps to glyph 0 and is therefore
// not recorded; the sixth segment, the only one that maps 'A' to a real glyph,
// is never reached because the budget runs out inside the fifth. The result is
// a cmap that is missing a mapping the font really declares — the exact
// condition under which "this code has no glyph" is not a fact.
func budgetBustingCmap() []byte {
	segs := make([][3]int, 0, 7)
	for i := 0; i < 5; i++ {
		segs = append(segs, [3]int{0x0000, 0xFFFE, 0x10000 - 0x41})
	}
	segs = append(segs, [3]int{0x0041, 0x0041, 100})
	segs = append(segs, [3]int{0xFFFF, 0xFFFF, 1}) // sentinel
	return buildCmapFormat4(segs)
}

func TestCmapFormat4BudgetReportsPartial(t *testing.T) {
	m, partial := font.ParseCmapSubtable(budgetBustingCmap(), core.DefaultMaxCmapWork)
	if !partial {
		t.Fatal("the work budget did not trip; the fixture no longer exercises it")
	}
	if m == nil {
		t.Fatal("a budget trip must still return the mappings it did read")
	}
	if _, ok := m[0x41]; ok {
		t.Fatal("fixture wrong: code 0x41 was mapped, so nothing is missing")
	}
}

// TestCmapWorkBudgetDoesNotCondemnGlyphs is the false positive itself: with the
// budget tripped, font.TrueTypeGID's "a non-nil cmap is authoritative" contract turns
// every unread mapping into glyph 0, so a conformant font is reported as not
// defining a glyph it does define, and as referencing .notdef.
//
// Before the fix this failed with:
//
//	glyph-coverage rules fired on a font whose cmap was truncated by the work
//	budget: [embedded TrueType font does not define a glyph referenced for
//	rendering (code 65) text showing operator references the .notdef glyph in
//	TrueType font]
func TestCmapWorkBudgetDoesNotCondemnGlyphs(t *testing.T) {
	head := make([]byte, 54)
	binary.BigEndian.PutUint16(head[18:], 1000) // unitsPerEm
	maxp := make([]byte, 6)
	binary.BigEndian.PutUint16(maxp[4:], 300) // numGlyphs
	prog := buildTestSFNT([]sfntTable{
		{"head", head},
		{"maxp", maxp},
		{"cmap", cmapTable(budgetBustingCmap())},
	})

	fd := &Dictionary{}
	fd.Set("Flags", Integer(32)) // non-symbolic: codes go through the (3,1) cmap
	fd.Set("FontFile2", IndirectRef{Number: 9})
	font := &Dictionary{}
	font.Set("Subtype", Name("TrueType"))
	font.Set("Encoding", Name("WinAnsiEncoding"))
	font.Set("FontDescriptor", fd)
	doc := &Document{Objects: map[int]*IndirectObject{
		1: {Number: 1, Value: font},
		9: {Number: 9, Value: &Stream{Dict: Dictionary{}, Data: prog}},
	}}
	doc = beginRun(doc)

	if fp := core.LoadFontProgram(doc.view(), fd); fp == nil || !fp.CmapPartial {
		t.Fatalf("fixture wrong: font program parsed=%v, cmapPartial=%v", fp != nil, fp != nil && fp.CmapPartial)
	}

	u := &core.FontTextUsage{ObjNum: 1, Strings: [][]byte{[]byte("A")}, Modes: map[int]bool{0: true}}
	msgs := errMessages(checkSimpleFontConsistency(doc.view(), PDFA1b, "6.3", font, u))
	var bad []string
	for _, m := range msgs {
		if strings.Contains(m, "does not define a glyph") || strings.Contains(m, ".notdef") {
			bad = append(bad, m)
		}
	}
	if len(bad) > 0 {
		t.Errorf("glyph-coverage rules fired on a font whose cmap was truncated by the work budget: %v", bad)
	}

	// The trip is not silently swallowed either.
	trips := doc.valCache.run.limits.Snapshot()
	if len(trips) == 0 || trips[0].Guard() != limitCmapWork {
		t.Errorf("cmap work-budget trip was not reported: %v", trips)
	}
}

// --- the CID /W range span limit (limits.cidRangeSpan, WithMaxCIDRangeSpan) ---

// TestCIDWidthRangeBudgetReportsPartial pins the parse-level contract: an
// over-wide /W range is dropped, and the map says so rather than looking like a
// font that simply declares no width for those CIDs.
func TestCIDWidthRangeBudgetReportsPartial(t *testing.T) {
	doc := &Document{Objects: map[int]*IndirectObject{}}
	if _, complete := parseCIDWidths(doc.view(), Array{Integer(0), Integer(2_000_000_000), Real(500)}); complete {
		t.Error("an over-wide /W range was dropped but the map claims to be complete")
	}
	// A malformed (inverted) range declares nothing, so nothing is missing.
	if _, complete := parseCIDWidths(doc.view(), Array{Integer(100), Integer(10), Real(500)}); !complete {
		t.Error("an inverted /W range is malformed input, not a budget trip")
	}
	if _, complete := parseCIDWidths(doc.view(), Array{Integer(0), Integer(65535), Real(500)}); !complete {
		t.Error("a full-CID-space /W range fits the budget and must count as complete")
	}
}

// TestCIDWidthBudgetDoesNotReportWidthMismatch is the false positive: with the
// range dropped, every CID in it falls back to /DW (default 1000) and is then
// compared against the font program's real advance, emitting "width information
// ... is inconsistent" against a file whose /W says exactly the right thing.
//
// Before the fix this failed with:
//
//	width rule fired on a font whose /W was dropped by the range budget:
//	[width information for glyphs used for rendering is inconsistent in
//	CIDFontType2 font]
func TestCIDWidthBudgetDoesNotReportWidthMismatch(t *testing.T) {
	// A CIDFontType2 program whose glyph 1 advances 500 units.
	head := make([]byte, 54)
	binary.BigEndian.PutUint16(head[18:], 1000)
	maxp := make([]byte, 6)
	binary.BigEndian.PutUint16(maxp[4:], 4)
	hhea := make([]byte, 36)
	binary.BigEndian.PutUint16(hhea[34:], 4) // numberOfHMetrics
	hmtx := make([]byte, 16)
	for gid := 0; gid < 4; gid++ {
		binary.BigEndian.PutUint16(hmtx[4*gid:], 500)
	}
	loca := make([]byte, 2*(4+1))
	for i := range loca {
		loca[i] = 0
	}
	glyf := make([]byte, 0)
	prog := buildTestSFNT([]sfntTable{
		{"head", head}, {"maxp", maxp}, {"hhea", hhea}, {"hmtx", hmtx},
		{"loca", loca}, {"glyf", glyf},
	})

	fd := &Dictionary{}
	fd.Set("Flags", Integer(4))
	fd.Set("FontFile2", IndirectRef{Number: 9})
	desc := &Dictionary{}
	desc.Set("Subtype", Name("CIDFontType2"))
	desc.Set("FontDescriptor", fd)
	desc.Set("CIDToGIDMap", Name("Identity"))
	// The file declares width 500 for every CID — correctly — but as one range
	// wider than the guard will expand.
	desc.Set("W", Array{Integer(0), Integer(2_000_000_000), Real(500)})
	font := &Dictionary{}
	font.Set("Subtype", Name("Type0"))
	font.Set("Encoding", Name("Identity-H"))
	font.Set("DescendantFonts", Array{desc})
	doc := &Document{Objects: map[int]*IndirectObject{
		1: {Number: 1, Value: font},
		9: {Number: 9, Value: &Stream{Dict: Dictionary{}, Data: prog}},
	}}
	doc = beginRun(doc)

	u := &core.FontTextUsage{ObjNum: 1, Strings: [][]byte{{0x00, 0x01}}, Modes: map[int]bool{0: true}}
	msgs := errMessages(checkCIDFontConsistency(doc.view(), PDFA1b, "6.3", font, u))
	var bad []string
	for _, m := range msgs {
		if strings.Contains(m, "width information") {
			bad = append(bad, m)
		}
	}
	if len(bad) > 0 {
		t.Errorf("width rule fired on a font whose /W was dropped by the range budget: %v", bad)
	}
	if trips := doc.valCache.run.limits.Snapshot(); len(trips) == 0 || trips[0].Guard() != limitCIDWidthRange {
		t.Errorf("/W range budget trip was not reported: %v", trips)
	}
}

// --- the aggregate content budget vs. the document's own identification ---

// TestMetadataSurvivesContentBudget is the widest false positive of the set: the
// /Metadata stream used to be decoded through the same budget as page content,
// so a document whose content exhausted it had its XMP read as empty and was
// then reported as carrying no PDF/A identification at all.
//
// Before the fix this failed with:
//
//	identification rules fired on a file that is fully identified, because the
//	content budget starved /Metadata: [metadata must contain pdfaid:part
//	pdfaid:conformance must be B, got ""]
func TestMetadataSurvivesContentBudget(t *testing.T) {
	doc := NewPDFADocument(PDFA2b)
	// Give it one page whose content stream is big enough to exhaust a lowered
	// budget before the identification checks run. The content paints nothing,
	// so the page adds no findings of its own.
	num := 0
	for n := range doc.Objects {
		if n > num {
			num = n
		}
	}
	pagesRef := doc.ResolveDict(doc.Trailer.Get("Root")).Get("Pages")
	pages := doc.ResolveDict(pagesRef)
	var kids Array
	for i := 0; i < 2; i++ {
		contentNum, pageNum := num+1+2*i, num+2+2*i
		doc.Objects[contentNum] = &IndirectObject{Number: contentNum, Value: &Stream{
			Dict: Dictionary{}, Data: bytes.Repeat([]byte("q Q\n"), 4096+i),
		}}
		page := &Dictionary{}
		page.Set("Type", Name("Page"))
		page.Set("Parent", pagesRef)
		page.Set("MediaBox", Array{Integer(0), Integer(0), Integer(612), Integer(792)})
		page.Set("Resources", &Dictionary{})
		page.Set("Contents", IndirectRef{Number: contentNum})
		doc.Objects[pageNum] = &IndirectObject{Number: pageNum, Value: page}
		kids = append(kids, IndirectRef{Number: pageNum})
	}
	pages.Set("Kids", kids)
	pages.Set("Count", Integer(len(kids)))

	if base := ValidatePDFA(doc, PDFA2b); len(base) > 0 {
		t.Fatalf("fixture is not conformant before the budget is lowered: %v", errMessages(base))
	}

	// Small enough that the first page's content exhausts it, so the second
	// page — and, before the fix, /Metadata — is never decoded. The budget is
	// per-Document (limits.go), so lowering it here cannot leak into any other
	// test, which is what the package-level var it replaced could not promise.
	doc.limits = resolveLimits([]Option{WithMaxDecodedContentBytes(100)})

	msgs := errMessages(ValidatePDFA(doc, PDFA2b))
	var bad []string
	for _, m := range msgs {
		if strings.Contains(m, "pdfaid") || strings.Contains(m, "XMP dc:title") {
			bad = append(bad, m)
		}
	}
	if len(bad) > 0 {
		t.Errorf("identification rules fired on a file that is fully identified, because the content budget starved /Metadata: %v", bad)
	}
	if !hasMessage(msgs, "resource limit reached (decoded-content-total)") {
		t.Errorf("the content budget tripped but was not reported: %v", msgs)
	}
}

// --- the content tokenizers' 256-byte cap ---

// TestOverlongTokenIsNotAnOperator pins that a run of binary too long to be a
// token is dropped whole. Cutting it at the cap and re-entering mid-run turned
// the tail into tokens, and a one-byte 'k' fragment reads as DeviceCMYK use.
//
// Before the fix this failed with:
//
//	a 515-byte binary run was tokenized into colour operators: rgb=false
//	cmyk=true gray=false
func TestOverlongTokenIsNotAnOperator(t *testing.T) {
	// 514 filler bytes plus a 'k': under the old chunked scan this split into
	// two 257-byte tokens and a final one-byte "k", which is the DeviceCMYK
	// operator.
	run := append(bytes.Repeat([]byte("A"), 514), []byte("k")...)
	data := append([]byte("q "), run...)
	data = append(data, []byte(" Q")...)
	rgb, cmyk, gray := core.ScanStreamForDeviceOps(core.Canceler{}, data)
	if cmyk || rgb {
		t.Errorf("a %d-byte binary run was tokenized into colour operators: rgb=%v cmyk=%v gray=%v", len(run), rgb, cmyk, gray)
	}

	// The tokenizer used by the operator whitelist must not manufacture an
	// operator out of the tail either.
	var ops []string
	core.ForEachContentToken(core.Canceler{}, data, func(tok []byte, isName bool) {
		if !isName {
			ops = append(ops, string(tok))
		}
	})
	for _, op := range ops {
		if op != "q" && op != "Q" {
			t.Errorf("core.ForEachContentToken produced %q from an over-long binary run", op)
		}
	}
}

// --- read-time trips reach the validators ---

// TestObjStmBudgetTripIsReported pins the one read-time guard the mechanism can
// reach: objects that never made it into the document because the aggregate
// object-stream decompression budget ran out. Every "X is absent" finding on
// such a file is suspect, so the trip is reported alongside them.
func TestObjStmBudgetTripIsReported(t *testing.T) {
	doc := &Document{Objects: map[int]*IndirectObject{}, Trailer: Dictionary{}}
	doc.noteReadLimit(limitObjStmTotal, "object stream 7 was not unpacked", 7)

	errs := ValidatePDFA(doc, PDFA2b)
	var trip *ValidationError
	for i := range errs {
		if errs[i].Rule == finding.LimitRule {
			trip = &errs[i]
		}
	}
	if trip == nil {
		t.Fatalf("a read-time guard trip did not reach the PDF/A report: %v", errMessages(errs))
	}
	if !IsCheckerFinding(*trip) {
		t.Error("a limit finding must be identifiable as a checker finding, not a conformance failure")
	}
	if trip.Object != 7 {
		t.Errorf("the trip lost its object anchor: %d", trip.Object)
	}

	// Every validator that reports through the shared mechanism sees it.
	uaHas := false
	for _, v := range ValidatePDFUA(doc) {
		if v.Clause == finding.LimitRule {
			uaHas = true
		}
	}
	if !uaHas {
		t.Error("a read-time guard trip did not reach the PDF/UA report")
	}
	xHas := false
	for _, v := range ValidatePDFX(doc, PDFX4) {
		if v.Rule == finding.LimitRule {
			xHas = true
		}
	}
	if !xHas {
		t.Error("a read-time guard trip did not reach the PDF/X report")
	}
}

// TestLimitRecorderIsBounded pins that the report cannot itself become the
// resource exhaustion the guards exist to prevent.
func TestLimitRecorderIsBounded(t *testing.T) {
	r := &core.Recorder{}
	for i := 0; i < 10*core.MaxRecordedTrips; i++ {
		r.Note(limitContentStream, "distinct detail", i)
	}
	trips := r.Snapshot()
	if len(trips) != core.MaxRecordedTrips+1 { // +1 for the aggregate
		t.Fatalf("recorder kept %d trips, want %d plus one aggregate", len(trips), core.MaxRecordedTrips)
	}
	if !hasMessage([]string{trips[len(trips)-1].Message()}, "further distinct guard trips") {
		t.Errorf("dropped trips were not accounted for: %q", trips[len(trips)-1].Message())
	}
	// Repeats of an identical trip collapse.
	r2 := &core.Recorder{}
	for i := 0; i < 100; i++ {
		r2.Note(core.GuardGridFills, "same", 1)
	}
	if got := len(r2.Snapshot()); got != 1 {
		t.Errorf("identical trips were not deduplicated: %d", got)
	}
}

// --- a limit ignored on the write path ---

// TestIncrementalRefusesMissingObjects pins that WriteIncremental refuses a
// document whose object streams were not unpacked, as Write already did. It was
// the one write path that computed /Size from an incomplete object set and
// emitted the result without a word.
//
// Before the fix this failed with:
//
//	WriteIncremental accepted a document with 1 unmaterialised object stream(s)
func TestIncrementalRefusesMissingObjects(t *testing.T) {
	var buf bytes.Buffer
	doc := NewPDFADocument(PDFA2b)
	if err := doc.Write(&buf); err != nil {
		t.Fatalf("writing: %v", err)
	}
	original := buf.Bytes()
	reread, err := Read(bytes.NewReader(original), int64(len(original)))
	if err != nil {
		t.Fatalf("re-reading: %v", err)
	}
	reread.brokenObjStms = append(reread.brokenObjStms, 3)

	var out bytes.Buffer
	if err := reread.WriteIncremental(&out, original, []int{1}); err == nil {
		t.Fatal("WriteIncremental accepted a document with 1 unmaterialised object stream(s)")
	} else if !strings.Contains(err.Error(), "object stream") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- a limit whose truncated result was indexed as if complete ---
//
// TestJBIG2ShortMMRDoesNotPanic covered that case — a short ccitt.Decode result
// indexed by decodeGenericMMR as if complete. It calls the decoder directly, so
// it moved to internal/jbig2 with the decoder itself.

// embeddedPDFAFixture returns the bytes of a minimal PDF/A-4 document carrying
// two pages, each with its own short content stream, plus an outer document that
// embeds those bytes as an application/pdf attachment.
//
// Two content streams rather than one is what makes the fixture useful: the
// aggregate content budget is charged as streams are decoded, so a second
// distinct stream is what a lowered WithMaxDecodedContentBytes can stop. The
// per-stream cap is deliberately *not* the guard used here — it also bounds the
// metadata stream, so lowering it makes the embedded document unidentifiable
// rather than incompletely validated, which is a different path.
func embeddedPDFAFixture(t *testing.T, lim core.Limits) (inner []byte, outer *Document) {
	t.Helper()

	doc := NewPDFADocument(PDFA4)
	next := 1
	for n := range doc.Objects {
		if n >= next {
			next = n + 1
		}
	}
	var kids Array
	for i := 0; i < 2; i++ {
		// Distinct bytes so the two streams are distinct objects, not one shared
		// stream the per-run cache would answer once for.
		content := &Stream{Dict: Dictionary{}, Data: []byte("q Q % " + strings.Repeat("x", i+1))}
		content.Dict.Set("Length", Integer(len(content.Data)))
		contentNum := next
		doc.Objects[contentNum] = &IndirectObject{Number: contentNum, Value: content}
		next++
		page := &Dictionary{}
		page.Set("Type", Name("Page"))
		page.Set("Parent", IndirectRef{Number: 2})
		page.Set("MediaBox", Array{Integer(0), Integer(0), Integer(612), Integer(792)})
		page.Set("Resources", &Dictionary{})
		page.Set("Contents", IndirectRef{Number: contentNum})
		doc.Objects[next] = &IndirectObject{Number: next, Value: page}
		kids = append(kids, IndirectRef{Number: next})
		next++
	}
	if pd := doc.ResolveDict(doc.ResolveDict(doc.Trailer.Get("Root")).Get("Pages")); pd != nil {
		pd.Set("Kids", kids)
		pd.Set("Count", Integer(len(kids)))
	}

	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatalf("writing the inner document: %v", err)
	}
	inner = buf.Bytes()

	ef := &Stream{Dict: Dictionary{}, Data: append([]byte(nil), inner...)}
	ef.Dict.Set("Type", Name("EmbeddedFile"))
	ef.Dict.Set("Subtype", Name("application/pdf"))
	ef.Dict.Set("Length", Integer(len(ef.Data)))
	fsEF := &Dictionary{}
	fsEF.Set("F", IndirectRef{Number: 2})
	fs := &Dictionary{}
	fs.Set("Type", Name("Filespec"))
	fs.Set("EF", fsEF)

	outer = &Document{
		Objects: map[int]*IndirectObject{
			1: {Number: 1, Value: fs},
			2: {Number: 2, Value: ef},
		},
		Version: "2.0",
		limits:  lim,
	}
	return inner, outer
}

// TestEmbeddedPDFAIncompleteIsNotNonConformance is the rule at the top of this
// file applied to clause 6.9. checkEmbeddedPDFA used to treat any non-empty
// result from the nested validation as "this embedded file is not PDF/A" — so a
// guard trip or a recovered panic *inside* the embedded document, which says
// only that pdf0 could not finish, became a conformance finding against the
// outer file. It also read the embedded bytes under defaultLimits() rather than
// the outer document's, so a caller's configured ceiling did not reach the one
// place a hostile file gets a whole second document validated.
func TestEmbeddedPDFAIncompleteIsNotNonConformance(t *testing.T) {
	innerBytes, _ := embeddedPDFAFixture(t, core.DefaultLimits())

	// Under the defaults the nested run completes, so its verdict is real...
	if compliant, complete := embeddedPDFACompliant(core.Canceler{}, innerBytes, core.DefaultLimits()); !complete {
		t.Fatal("the nested validation should complete under the default limits")
	} else if !compliant {
		t.Error("a PDF/A-4 document embedded in another should be reported compliant")
	}
	// ...and a file that genuinely is not PDF/A is still condemned, completely.
	// The rule must decline only when pdf0 could not look, never when it did.
	if compliant, complete := embeddedPDFACompliant(core.Canceler{}, []byte("not a PDF at all"), core.DefaultLimits()); compliant || !complete {
		t.Errorf("non-PDF bytes: compliant=%v complete=%v, want false/true", compliant, complete)
	}

	// The nested read and validation inherit the outer document's limits, so a
	// cap the embedded document cannot be validated under withholds the verdict
	// rather than turning it into a 6.9 finding.
	strict := resolveLimits([]Option{WithMaxDecodedContentBytes(1)})
	if _, complete := embeddedPDFACompliant(core.Canceler{}, innerBytes, strict); complete {
		t.Error("a nested run that reported a checker finding must be reported as incomplete")
	}

	// The same rule reaches the two exits that never see a nested finding at
	// all, because the nested run did not get far enough to produce one. A
	// lowered per-stream cap leaves the embedded document's own metadata
	// undecodable, so declaredPDFALevel reports "not PDF/A" for a file that may
	// well be one. That is the checker's doing, not the file's, and it must
	// withhold rather than condemn.
	noMeta := resolveLimits([]Option{WithMaxContentStreamBytes(1)})
	if compliant, complete := embeddedPDFACompliant(core.Canceler{}, innerBytes, noMeta); compliant || complete {
		t.Errorf("metadata undecodable under a lowered cap: compliant=%v complete=%v, want false/false",
			compliant, complete)
	}

	// End to end: no 6.9 finding, and the incompleteness reported under the
	// "limit" rule naming the embedded-pdfa guard.
	_, outer := embeddedPDFAFixture(t, strict)
	run := beginRun(outer)
	for _, e := range checkEmbeddedPDFA(run.view(), PDFA4) {
		if e.Rule == "6.9" {
			t.Errorf("6.9 asserted on the strength of an incomplete nested run: %s", e.Message)
		}
	}
	reported := false
	for _, e := range limitValidationErrors(run, PDFA4) {
		if e.Rule == finding.LimitRule && strings.Contains(e.Message, limitEmbeddedPDFA) {
			reported = true
		}
	}
	if !reported {
		t.Errorf("the declined check must be reported as a %q finding naming %q", finding.LimitRule, limitEmbeddedPDFA)
	}
}
