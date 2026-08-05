package pdf0

import (
	"bytes"
	"github.com/mgilbir/forme/font"
	"github.com/mgilbir/forme/fonttest"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/finding"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
	"github.com/mgilbir/pdf0/pdfx"
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

func errMessages(errs []pdfa.Violation) []string {
	out := make([]string, len(errs))
	for i, e := range errs {
		out[i] = e.Message
	}
	return out
}

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
	return fonttest.CmapFormat4(segs)
}

func hasMessage(msgs []string, sub string) bool {
	for _, m := range msgs {
		if strings.Contains(m, sub) {
			return true
		}
	}
	return false
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
	doc := NewPDFADocument(pdfa.PDFA2b)
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
	var kids object.Array
	for i := 0; i < 2; i++ {
		contentNum, pageNum := num+1+2*i, num+2+2*i
		doc.Objects[contentNum] = &object.IndirectObject{Number: contentNum, Value: &object.Stream{
			Dict: object.Dictionary{}, Data: bytes.Repeat([]byte("q Q\n"), 4096+i),
		}}
		page := &object.Dictionary{}
		page.Set("Type", object.Name("Page"))
		page.Set("Parent", pagesRef)
		page.Set("MediaBox", object.Array{object.Integer(0), object.Integer(0), object.Integer(612), object.Integer(792)})
		page.Set("Resources", &object.Dictionary{})
		page.Set("Contents", object.IndirectRef{Number: contentNum})
		doc.Objects[pageNum] = &object.IndirectObject{Number: pageNum, Value: page}
		kids = append(kids, object.IndirectRef{Number: pageNum})
	}
	pages.Set("Kids", kids)
	pages.Set("Count", object.Integer(len(kids)))

	if base := ValidatePDFA(doc, pdfa.PDFA2b); len(base) > 0 {
		t.Fatalf("fixture is not conformant before the budget is lowered: %v", errMessages(base))
	}

	// Small enough that the first page's content exhausts it, so the second
	// page — and, before the fix, /Metadata — is never decoded. The budget is
	// per-Document (limits.go), so lowering it here cannot leak into any other
	// test, which is what the package-level var it replaced could not promise.
	doc.limits = resolveLimits([]Option{WithMaxDecodedContentBytes(100)})

	msgs := errMessages(ValidatePDFA(doc, pdfa.PDFA2b))
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
	doc := &Document{Objects: map[int]*object.IndirectObject{}, Trailer: object.Dictionary{}}
	doc.noteReadLimit(limitObjStmTotal, "object stream 7 was not unpacked", 7)

	errs := ValidatePDFA(doc, pdfa.PDFA2b)
	var trip *pdfa.Violation
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
	for _, v := range ValidatePDFX(doc, pdfx.PDFX4) {
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
	doc := NewPDFADocument(pdfa.PDFA2b)
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

	doc := NewPDFADocument(pdfa.PDFA4)
	next := 1
	for n := range doc.Objects {
		if n >= next {
			next = n + 1
		}
	}
	var kids object.Array
	for i := 0; i < 2; i++ {
		// Distinct bytes so the two streams are distinct objects, not one shared
		// stream the per-run cache would answer once for.
		content := &object.Stream{Dict: object.Dictionary{}, Data: []byte("q Q % " + strings.Repeat("x", i+1))}
		content.Dict.Set("Length", object.Integer(len(content.Data)))
		contentNum := next
		doc.Objects[contentNum] = &object.IndirectObject{Number: contentNum, Value: content}
		next++
		page := &object.Dictionary{}
		page.Set("Type", object.Name("Page"))
		page.Set("Parent", object.IndirectRef{Number: 2})
		page.Set("MediaBox", object.Array{object.Integer(0), object.Integer(0), object.Integer(612), object.Integer(792)})
		page.Set("Resources", &object.Dictionary{})
		page.Set("Contents", object.IndirectRef{Number: contentNum})
		doc.Objects[next] = &object.IndirectObject{Number: next, Value: page}
		kids = append(kids, object.IndirectRef{Number: next})
		next++
	}
	if pd := doc.ResolveDict(doc.ResolveDict(doc.Trailer.Get("Root")).Get("Pages")); pd != nil {
		pd.Set("Kids", kids)
		pd.Set("Count", object.Integer(len(kids)))
	}

	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatalf("writing the inner document: %v", err)
	}
	inner = buf.Bytes()

	ef := &object.Stream{Dict: object.Dictionary{}, Data: append([]byte(nil), inner...)}
	ef.Dict.Set("Type", object.Name("EmbeddedFile"))
	ef.Dict.Set("Subtype", object.Name("application/pdf"))
	ef.Dict.Set("Length", object.Integer(len(ef.Data)))
	fsEF := &object.Dictionary{}
	fsEF.Set("F", object.IndirectRef{Number: 2})
	fs := &object.Dictionary{}
	fs.Set("Type", object.Name("Filespec"))
	fs.Set("EF", fsEF)

	outer = &Document{
		Objects: map[int]*object.IndirectObject{
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
	// Through the public entry point: checkEmbeddedPDFA is a pdfa internal, and
	// what this pins is the finding the caller sees, not the call that made it.
	for _, e := range ValidatePDFA(outer, pdfa.PDFA4) {
		if e.Rule == "6.9" {
			t.Errorf("6.9 asserted on the strength of an incomplete nested run: %s", e.Message)
		}
	}
	reported := false
	for _, e := range ValidatePDFA(outer, pdfa.PDFA4) {
		if e.Rule == finding.LimitRule && strings.Contains(e.Message, core.GuardEmbeddedPDFA) {
			reported = true
		}
	}
	if !reported {
		t.Errorf("the declined check must be reported as a %q finding naming %q", finding.LimitRule, core.GuardEmbeddedPDFA)
	}
}
