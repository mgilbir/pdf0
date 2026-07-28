package pdf0

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// These tests pin the four properties cancel.go promises.
//
//  1. A cancelled call returns *promptly* — measured, not asserted by the fact
//     that it returned at all. A cancellation that takes 25 seconds to take
//     effect is the bug this work exists to fix.
//  2. An already-cancelled context does no work.
//  3. A cancelled validation never looks like a clean bill of health. This is
//     the property that matters most: the failure it prevents is a caller
//     concluding "no findings, therefore conformant" from a run that stopped
//     before it had looked.
//  4. The work actually stops — the goroutine running it returns, rather than
//     being abandoned to burn a core.

// --- fixtures ---

// heavyContent returns roughly n bytes of content-stream tokens: real
// operators and operands, so the scanners do their real work rather than
// skipping a run of whitespace.
func heavyContent(n int) []byte {
	unit := []byte("q 1 0 0 RG 0.5 g 10 10 m 200 200 l S /F1 12 Tf (some text) Tj Q\n")
	var b bytes.Buffer
	b.Grow(n + len(unit))
	for b.Len() < n {
		b.Write(unit)
	}
	return b.Bytes()
}

// heavyDoc builds a document of pages whose content streams are each
// bytesPerPage of real content tokens, stored uncompressed so the test measures
// scanning rather than inflation.
func heavyDoc(pages, bytesPerPage int) *Document {
	objs := map[int]*IndirectObject{}
	cat := &Dictionary{}
	cat.Set("Type", Name("Catalog"))
	cat.Set("Pages", IndirectRef{Number: 2})
	objs[1] = &IndirectObject{Number: 1, Value: cat}

	pagesDict := &Dictionary{}
	pagesDict.Set("Type", Name("Pages"))
	pagesDict.Set("Count", Integer(pages))

	var kids Array
	num := 3
	content := heavyContent(bytesPerPage)
	for i := 0; i < pages; i++ {
		contentNum := num
		st := &Stream{Data: content}
		st.Dict.Set("Length", Integer(len(content)))
		objs[contentNum] = &IndirectObject{Number: contentNum, Value: st}
		num++

		pageNum := num
		page := &Dictionary{}
		page.Set("Type", Name("Page"))
		page.Set("Parent", IndirectRef{Number: 2})
		page.Set("MediaBox", Array{Integer(0), Integer(0), Integer(612), Integer(792)})
		page.Set("Contents", IndirectRef{Number: contentNum})
		page.Set("Resources", &Dictionary{})
		objs[pageNum] = &IndirectObject{Number: pageNum, Value: page}
		kids = append(kids, IndirectRef{Number: pageNum})
		num++
	}
	pagesDict.Set("Kids", kids)
	objs[2] = &IndirectObject{Number: 2, Value: pagesDict}

	return &Document{Version: "2.0", Objects: objs,
		Trailer: dictWith("Root", IndirectRef{Number: 1})}
}

// cancelledCtx returns a context that is already done.
func cancelledCtx() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// hasCancelFinding reports whether v contains the checker finding a cancelled
// run must report: rule "limit", guard "context-canceled", and recognised by
// the package's own IsCheckerFinding predicate.
func hasCancelFinding[T Violation](v []T) bool {
	for _, e := range v {
		if e.RuleID() == limitRule && strings.Contains(e.Error(), limitCanceled) && IsCheckerFinding(e) {
			return true
		}
	}
	return false
}

// --- 1. promptness ---

// TestCancelValidationLatency measures how long a cancelled validation takes to
// return, and compares it against how long the same validation takes when it is
// allowed to finish. Asserting only "it returned" would pass even if
// cancellation did nothing at all.
func TestCancelValidationLatency(t *testing.T) {
	doc := heavyDoc(24, 2<<20) // ~48 MB of content across 24 pages

	start := time.Now()
	full := ValidatePDFA(doc, PDFA2b)
	baseline := time.Since(start)
	t.Logf("uncancelled ValidatePDFA: %v (%d findings)", baseline, len(full))
	if baseline < 300*time.Millisecond {
		t.Skipf("fixture validates in %v, too fast to measure a cancellation against", baseline)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	start = time.Now()
	errs := ValidatePDFAContext(ctx, doc, PDFA2b)
	latency := time.Since(start)
	cancel()
	t.Logf("cancelled after 20ms, returned in %v (%d findings)", latency, len(errs))

	// The bound is generous because the granularity is deliberately coarse (per
	// check, per stream, per megabyte scanned), but it must be far below the
	// full run: a cancellation that saves nothing is not a cancellation.
	if latency > baseline/2 {
		t.Errorf("cancellation saved almost nothing: returned in %v against a %v full run", latency, baseline)
	}
	if latency > time.Second {
		t.Errorf("cancellation took %v to take effect; the coarsest boundary should be well under a second", latency)
	}
	if !hasCancelFinding(errs) {
		t.Error("cancelled run did not report the cancellation as a checker finding")
	}
}

// TestCancelInsideOneHugeStream pins the finer of the two granularities. With
// all the content in a single stream there is exactly one per-check and one
// per-stream boundary in the whole run, so a prompt return can only come from
// the check inside the token scanners.
func TestCancelInsideOneHugeStream(t *testing.T) {
	doc := heavyDoc(1, 48<<20) // one page, one 48 MB content stream

	start := time.Now()
	ValidatePDFA(doc, PDFA2b)
	baseline := time.Since(start)
	t.Logf("uncancelled ValidatePDFA over one 48 MB stream: %v", baseline)
	if baseline < 300*time.Millisecond {
		t.Skipf("fixture validates in %v, too fast to measure a cancellation against", baseline)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	start = time.Now()
	errs := ValidatePDFAContext(ctx, doc, PDFA2b)
	latency := time.Since(start)
	cancel()
	t.Logf("cancelled mid-stream, returned in %v", latency)

	if latency > baseline/2 {
		t.Errorf("cancellation inside a single stream saved almost nothing: %v against a %v full run", latency, baseline)
	}
	if !hasCancelFinding(errs) {
		t.Error("cancelled run did not report the cancellation as a checker finding")
	}
}

// TestCancelLargeRealFile measures cancellation latency on a real multi-hundred-
// megabyte document when one is available. Set PDF0_LARGE_PDF to a path; the
// test skips otherwise, so it never blocks a normal run.
func TestCancelLargeRealFile(t *testing.T) {
	path := os.Getenv("PDF0_LARGE_PDF")
	if path == "" {
		t.Skip("set PDF0_LARGE_PDF to a large PDF to measure cancellation on real input")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("cannot read %s: %v", path, err)
	}
	doc, err := Read(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	t.Logf("%s: %d bytes, %d pages, %d objects", path, len(data), doc.PageCount(), len(doc.Objects))

	start := time.Now()
	ValidatePDFA(doc, PDFA2b)
	baseline := time.Since(start)
	t.Logf("uncancelled ValidatePDFA: %v", baseline)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	start = time.Now()
	errs := ValidatePDFAContext(ctx, doc, PDFA2b)
	latency := time.Since(start)
	cancel()
	t.Logf("cancelled after 50ms, returned in %v (%d findings, full run had more)", latency, len(errs))
	if latency > baseline/4 {
		t.Errorf("cancellation latency %v is not meaningfully below the %v full run", latency, baseline)
	}
	if !hasCancelFinding(errs) {
		t.Error("cancelled run did not report the cancellation as a checker finding")
	}
}

// --- 2. an already-cancelled context does no work ---

func TestAlreadyCancelledDoesNoWork(t *testing.T) {
	doc := heavyDoc(24, 2<<20)

	start := time.Now()
	errs := ValidatePDFAContext(cancelledCtx(), doc, PDFA2b)
	elapsed := time.Since(start)
	t.Logf("already-cancelled ValidatePDFA returned in %v with %d findings", elapsed, len(errs))

	if elapsed > 100*time.Millisecond {
		t.Errorf("an already-cancelled run took %v; it should do no work at all", elapsed)
	}
	// No check ran, so the only finding can be the cancellation itself.
	if len(errs) != 1 {
		t.Fatalf("expected exactly the cancellation finding, got %d: %v", len(errs), errMessages(errs))
	}
	if !hasCancelFinding(errs) {
		t.Errorf("the single finding is not the cancellation finding: %q", errs[0].Message)
	}
}

// --- 3. a cancelled run never looks clean ---

// TestCancelledRunIsNeverClean is the property that matters most. Every
// validator, given an already-cancelled context, must return a non-empty result
// whose findings are all checker findings — so a caller testing len(result) == 0
// for "conformant" gets "not conformant", and a caller filtering with
// IsCheckerFinding gets "unknown". What neither can get is "clean".
func TestCancelledRunIsNeverClean(t *testing.T) {
	doc := heavyDoc(2, 4096)
	ctx := cancelledCtx()

	assertNotClean(t, "ValidatePDFA", ValidatePDFAContext(ctx, doc, PDFA2b))
	assertNotClean(t, "ValidatePDFABytes", ValidatePDFABytesContext(ctx, doc, PDFA2b, []byte("%PDF-2.0\n")))
	assertNotClean(t, "ValidatePDFUA", ValidatePDFUAContext(ctx, doc))
	assertNotClean(t, "ValidatePDFUA2", ValidatePDFUA2Context(ctx, doc))
	assertNotClean(t, "ValidatePDFX", ValidatePDFXContext(ctx, doc, PDFX4))
	assertNotClean(t, "ValidatePDFVT", ValidatePDFVTContext(ctx, doc))
	assertNotClean(t, "ValidatePDFVT2", ValidatePDFVT2Context(ctx, doc))
	assertNotClean(t, "ValidatePDFR", ValidatePDFRContext(ctx, doc))
	assertNotClean(t, "ValidateDParts", ValidateDPartsContext(ctx, doc))
}

// assertNotClean checks one validator's cancelled result: non-empty, carrying
// the cancellation finding, and carrying nothing that claims to be a real
// conformance failure — because no check ran, so none can have found one.
func assertNotClean[T Violation](t *testing.T, name string, v []T) {
	t.Helper()
	if len(v) == 0 {
		t.Errorf("%s: cancelled run returned an empty result — indistinguishable from a clean bill of health", name)
		return
	}
	sawCancel, real := false, 0
	for _, e := range v {
		if e.RuleID() == limitRule && strings.Contains(e.Error(), limitCanceled) {
			sawCancel = true
		}
		if !IsCheckerFinding(e) {
			real++
		}
	}
	if !sawCancel {
		t.Errorf("%s: cancelled run reported no %q finding naming %q", name, limitRule, limitCanceled)
	}
	if real != 0 {
		t.Errorf("%s: cancelled run reported %d non-checker findings; no check ran, so it cannot have found any", name, real)
	}
}

// TestCancelledLevelAIsNeverClean covers the Level A pipeline, which reaches the
// Level B checks by a different route.
func TestCancelledLevelAIsNeverClean(t *testing.T) {
	doc := heavyDoc(2, 4096)
	errs := ValidatePDFAContext(cancelledCtx(), doc, PDFA2a)
	if len(errs) == 0 {
		t.Fatal("cancelled Level A run returned an empty result")
	}
	if !hasCancelFinding(errs) {
		t.Errorf("cancelled Level A run reported no cancellation finding: %v", errMessages(errs))
	}
}

// TestCancelFindingIsNotAConformanceStatement pins the wording. The message must
// not be readable as a claim about the document.
func TestCancelFindingIsNotAConformanceStatement(t *testing.T) {
	errs := ValidatePDFAContext(cancelledCtx(), heavyDoc(1, 1024), PDFA2b)
	if len(errs) != 1 {
		t.Fatalf("expected one finding, got %d", len(errs))
	}
	msg := errs[0].Message
	for _, want := range []string{"cancelled", "neither confirmed conformant nor non-conformant"} {
		if !strings.Contains(msg, want) {
			t.Errorf("cancellation message %q does not contain %q", msg, want)
		}
	}
}

// --- 4. the work really stops ---

// TestCancelStopsTheWork asserts what abandoning a goroutine never did: the
// goroutine running the validation returns, and the process is left with no
// extra goroutines.
func TestCancelStopsTheWork(t *testing.T) {
	doc := heavyDoc(24, 2<<20)

	settle := func() int {
		for i := 0; i < 50; i++ {
			runtime.GC()
			time.Sleep(10 * time.Millisecond)
			if n := runtime.NumGoroutine(); i > 3 {
				return n
			}
		}
		return runtime.NumGoroutine()
	}
	before := settle()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		ValidatePDFAContext(ctx, doc, PDFA2b)
	}()
	time.Sleep(20 * time.Millisecond)
	start := time.Now()
	cancel()
	select {
	case <-done:
		t.Logf("validation goroutine returned %v after cancel", time.Since(start))
	case <-time.After(5 * time.Second):
		t.Fatal("validation goroutine did not return within 5s of cancellation — the work was not stopped")
	}

	after := settle()
	if after > before+1 {
		t.Errorf("goroutine count went from %d to %d; the cancelled run leaked", before, after)
	}
}

// --- the loud paths: Read, Write, and the extractors ---

func TestReadContextCancelled(t *testing.T) {
	doc := heavyDoc(4, 4096)
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatalf("Write: %v", err)
	}
	data := buf.Bytes()

	got, err := ReadContext(cancelledCtx(), bytes.NewReader(data), int64(len(data)))
	if err == nil {
		t.Fatal("ReadContext with a cancelled context returned no error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error does not unwrap to context.Canceled: %v", err)
	}
	if got != nil {
		t.Error("ReadContext returned a partial Document; it must return nil so a truncated object graph is never mistaken for the file's own contents")
	}

	// A deadline must be distinguishable from a cancellation.
	dctx, dcancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer dcancel()
	time.Sleep(time.Millisecond)
	_, err = ReadContext(dctx, bytes.NewReader(data), int64(len(data)))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("a deadline should unwrap to context.DeadlineExceeded, got %v", err)
	}
}

func TestReadContextBackgroundUnaffected(t *testing.T) {
	doc := heavyDoc(4, 4096)
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatalf("Write: %v", err)
	}
	data := buf.Bytes()

	plain, err := Read(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	withCtx, err := ReadContext(context.Background(), bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("ReadContext: %v", err)
	}
	if !DocumentEqual(plain, withCtx) {
		t.Error("ReadContext with a background context produced a different document than Read")
	}
}

func TestWriteContextCancelled(t *testing.T) {
	doc := heavyDoc(64, 4096)
	err := doc.WriteContext(cancelledCtx(), io.Discard)
	if err == nil {
		t.Fatal("WriteContext with a cancelled context returned no error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error does not unwrap to context.Canceled: %v", err)
	}

	if err := doc.WriteContext(context.Background(), io.Discard); err != nil {
		t.Errorf("WriteContext with a background context failed: %v", err)
	}
}

func TestExtractTextContextCancelled(t *testing.T) {
	doc := heavyDoc(24, 2<<20)

	start := time.Now()
	full := doc.ExtractText()
	baseline := time.Since(start)
	t.Logf("uncancelled ExtractText: %v (%d runes)", baseline, len([]rune(full)))

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	start = time.Now()
	text, err := doc.ExtractTextContext(ctx)
	latency := time.Since(start)
	cancel()
	t.Logf("cancelled ExtractText returned in %v with %d runes", latency, len([]rune(text)))

	if err == nil {
		t.Fatal("a cancelled ExtractTextContext returned a nil error, so its truncated text looks complete")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error does not unwrap to context.Canceled: %v", err)
	}
	if len(text) >= len(full) {
		t.Errorf("cancelled extraction returned %d bytes, not fewer than the full %d", len(text), len(full))
	}

	// The uncancelled variants must be unchanged.
	if got, err := doc.ExtractTextContext(context.Background()); err != nil || got != full {
		t.Errorf("ExtractTextContext(Background) differs from ExtractText (err=%v)", err)
	}
}

func TestExtractImagesContextCancelled(t *testing.T) {
	doc := heavyDoc(2, 1024)
	imgs, err := doc.ExtractImagesContext(cancelledCtx())
	if err == nil {
		t.Fatal("a cancelled ExtractImagesContext returned a nil error, so its short slice looks complete")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error does not unwrap to context.Canceled: %v", err)
	}
	if len(imgs) != 0 {
		t.Errorf("an already-cancelled extraction produced %d images", len(imgs))
	}
	if _, err := doc.ExtractImagesContext(context.Background()); err != nil {
		t.Errorf("ExtractImagesContext(Background) failed: %v", err)
	}
}

// --- backwards compatibility ---

// TestContextlessAPIUnchanged calls every entry point that gained a Context
// variant through its original signature, with no context anywhere, and
// asserts the results match the Context variants driven by a background
// context. The external-module build in the commit message covers the
// signatures; this covers the behaviour.
func TestContextlessAPIUnchanged(t *testing.T) {
	doc := heavyDoc(3, 8192)
	bg := context.Background()

	if a, b := len(ValidatePDFA(doc, PDFA2b)), len(ValidatePDFAContext(bg, doc, PDFA2b)); a != b {
		t.Errorf("ValidatePDFA %d findings vs ValidatePDFAContext %d", a, b)
	}
	if a, b := len(ValidatePDFUA(doc)), len(ValidatePDFUAContext(bg, doc)); a != b {
		t.Errorf("ValidatePDFUA %d findings vs ValidatePDFUAContext %d", a, b)
	}
	if a, b := len(ValidatePDFUA2(doc)), len(ValidatePDFUA2Context(bg, doc)); a != b {
		t.Errorf("ValidatePDFUA2 %d findings vs ValidatePDFUA2Context %d", a, b)
	}
	if a, b := len(ValidatePDFX(doc, PDFX4)), len(ValidatePDFXContext(bg, doc, PDFX4)); a != b {
		t.Errorf("ValidatePDFX %d findings vs ValidatePDFXContext %d", a, b)
	}
	if a, b := len(ValidatePDFVT(doc)), len(ValidatePDFVTContext(bg, doc)); a != b {
		t.Errorf("ValidatePDFVT %d findings vs ValidatePDFVTContext %d", a, b)
	}
	if a, b := len(ValidatePDFVT2(doc)), len(ValidatePDFVT2Context(bg, doc)); a != b {
		t.Errorf("ValidatePDFVT2 %d findings vs ValidatePDFVT2Context %d", a, b)
	}
	if a, b := len(ValidatePDFR(doc)), len(ValidatePDFRContext(bg, doc)); a != b {
		t.Errorf("ValidatePDFR %d findings vs ValidatePDFRContext %d", a, b)
	}
	if a, b := len(ValidateDParts(doc)), len(ValidateDPartsContext(bg, doc)); a != b {
		t.Errorf("ValidateDParts %d findings vs ValidateDPartsContext %d", a, b)
	}
	if a, _ := doc.ExtractTextContext(bg); a != doc.ExtractText() {
		t.Error("ExtractText differs from ExtractTextContext(Background)")
	}
	if a, _ := doc.ExtractImagesContext(bg); len(a) != len(doc.ExtractImages()) {
		t.Error("ExtractImages differs from ExtractImagesContext(Background)")
	}
}

// TestBackgroundContextCostsNothingMeasurable guards the one performance claim
// cancel.go makes: a run that cannot be cancelled pays a comparison per token
// and nothing more. It is a smoke test, not a benchmark — it fails only on a
// regression large enough to be visible past the noise of a single run.
func TestBackgroundContextCostsNothingMeasurable(t *testing.T) {
	if testing.Short() {
		t.Skip("timing-sensitive")
	}
	doc := heavyDoc(8, 2<<20)

	// Warm the caches equally: each call installs its own per-run cache, so
	// there is nothing shared to warm, but the allocator and the CPU caches
	// benefit from a discarded first run.
	ValidatePDFA(doc, PDFA2b)

	start := time.Now()
	ValidatePDFA(doc, PDFA2b)
	plain := time.Since(start)

	start = time.Now()
	ValidatePDFAContext(context.Background(), doc, PDFA2b)
	background := time.Since(start)

	t.Logf("ValidatePDFA %v, ValidatePDFAContext(Background) %v", plain, background)
	if background > plain*2 {
		t.Errorf("a background context more than doubled the run: %v vs %v", background, plain)
	}
}
