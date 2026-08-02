package pdfua

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"testing"
	"time"
)

// buildSharedStreamDoc builds a document whose nPages pages all reference the
// same content stream and the same /Font resource dictionary — the shape of the
// GHOSTSCRIPT-700953 stress file that made font-usage collection and
// real-content analysis quadratic (tokenizing one shared stream once per page).
// The content shows two literal strings with font F1 outside any marked-content
// sequence.
func buildSharedStreamDoc(nPages int) core.View {
	d := mkViewVersion(map[int]*object.IndirectObject{}, object.Dictionary{}, "1.7")
	put := func(n int, v object.Object) { d.Objects[n] = &object.IndirectObject{Number: n, Value: v} }

	font := &object.Dictionary{}
	font.Set("Type", object.Name("Font"))
	font.Set("Subtype", object.Name("Type1"))
	font.Set("BaseFont", object.Name("Helvetica"))
	put(50, font)

	fontRes := &object.Dictionary{}
	fontRes.Set("F1", object.IndirectRef{Number: 50})
	put(45, fontRes)

	content := &object.Stream{Dict: object.Dictionary{}, Data: []byte("BT /F1 12 Tf (AB) Tj (CD) Tj ET")}
	put(40, content)

	pagesNode := &object.Dictionary{}
	pagesNode.Set("Type", object.Name("Pages"))
	var kids object.Array
	num := 100
	for i := 0; i < nPages; i++ {
		res := &object.Dictionary{}
		res.Set("Font", object.IndirectRef{Number: 45})
		page := &object.Dictionary{}
		page.Set("Type", object.Name("Page"))
		page.Set("Parent", object.IndirectRef{Number: 10})
		page.Set("Contents", object.IndirectRef{Number: 40})
		page.Set("Resources", res)
		put(num, page)
		kids = append(kids, object.IndirectRef{Number: num})
		num++
	}
	pagesNode.Set("Kids", kids)
	pagesNode.Set("Count", object.Integer(nPages))
	put(10, pagesNode)

	cat := &object.Dictionary{}
	cat.Set("Type", object.Name("Catalog"))
	cat.Set("Pages", object.IndirectRef{Number: 10})
	put(1, cat)
	d.Trailer.Set("Root", object.IndirectRef{Number: 1})
	return d
}

// TestFontUsageSharedStreamDedup verifies that a content stream shared by many
// pages contributes its shown text to the font exactly once (not once per page)
// and that collection stays fast. Before the per-stream memoization this doc
// tokenized the shared stream once per page, making it quadratic.
func TestFontUsageSharedStreamDedup(t *testing.T) {
	const nPages = 20000
	doc := buildSharedStreamDoc(nPages)

	done := make(chan map[*object.Dictionary]*core.FontTextUsage, 1)
	start := time.Now()
	go func() { done <- core.CollectFontTextUsage(doc) }()
	var usage map[*object.Dictionary]*core.FontTextUsage
	select {
	case usage = <-done:
	case <-time.After(30 * time.Second):
		t.Fatalf("core.CollectFontTextUsage did not finish within 30s on a %d-page shared-stream doc", nPages)
	}
	t.Logf("core.CollectFontTextUsage over %d pages took %v", nPages, time.Since(start))

	font := doc.ResolveDict(object.IndirectRef{Number: 50})
	u := usage[font]
	if u == nil {
		t.Fatal("shared font recorded no usage")
	}
	// The stream shows two strings; deduped across all pages that is exactly two,
	// not two per page.
	if len(u.Strings) != 2 {
		t.Errorf("font usage has %d strings, want 2 (dedup across shared pages)", len(u.Strings))
	}
	if got := string(u.Strings[0]) + string(u.Strings[1]); got != "ABCD" {
		t.Errorf("shown strings = %q, want AB+CD", got)
	}
	if !u.Modes[0] {
		t.Error("render mode 0 not recorded")
	}
	// The single shared stream should have been tokenized once.
	if n := doc.Run.FontEventsMemoSize(); n != 1 {
		t.Errorf("fontEvents cache holds %d streams, want 1", n)
	}
}

// TestRealContentSharedStreamMemo verifies that the real-content (7.1) check
// analyzes a shared stream once but still reports the violation for every page
// that uses it (each under its own object number).
func TestRealContentSharedStreamMemo(t *testing.T) {
	const nPages = 20000
	doc := buildSharedStreamDoc(nPages)
	cat := doc.ResolveDict(doc.Trailer.Get("Root"))

	done := make(chan []UAViolation, 1)
	go func() { done <- checkUARealContent(doc, cat) }()
	var v []UAViolation
	select {
	case v = <-done:
	case <-time.After(30 * time.Second):
		t.Fatalf("checkUARealContent did not finish within 30s on a %d-page shared-stream doc", nPages)
	}

	// The shown text is outside any marked-content sequence, so every page is a
	// violation — one per page, each carrying that page's object number.
	if len(v) != nPages {
		t.Fatalf("got %d real-content violations, want %d (one per page)", len(v), nPages)
	}
	objs := map[int]bool{}
	for _, x := range v {
		if x.Message != "page contains text that is neither tagged nor marked as an /Artifact" {
			t.Fatalf("unexpected message %q", x.Message)
		}
		objs[x.Object] = true
	}
	if len(objs) != nPages {
		t.Errorf("violations cover %d distinct pages, want %d", len(objs), nPages)
	}
	// The shared stream was analyzed once and cached.
	if n := len(uaMemo(doc).streamFacts); n != 1 {
		t.Errorf("streamFacts cache holds %d streams, want 1", n)
	}
}
