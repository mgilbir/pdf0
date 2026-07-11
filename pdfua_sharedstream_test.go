package pdf0

import (
	"testing"
	"time"
)

// buildSharedStreamDoc builds a document whose nPages pages all reference the
// same content stream and the same /Font resource dictionary — the shape of the
// GHOSTSCRIPT-700953 stress file that made font-usage collection and
// real-content analysis quadratic (tokenizing one shared stream once per page).
// The content shows two literal strings with font F1 outside any marked-content
// sequence.
func buildSharedStreamDoc(nPages int) *Document {
	d := &Document{Objects: map[int]*IndirectObject{}, Version: "1.7"}
	put := func(n int, v Object) { d.Objects[n] = &IndirectObject{Number: n, Value: v} }

	font := &Dictionary{}
	font.Set("Type", Name("Font"))
	font.Set("Subtype", Name("Type1"))
	font.Set("BaseFont", Name("Helvetica"))
	put(50, font)

	fontRes := &Dictionary{}
	fontRes.Set("F1", IndirectRef{Number: 50})
	put(45, fontRes)

	content := &Stream{Dict: Dictionary{}, Data: []byte("BT /F1 12 Tf (AB) Tj (CD) Tj ET")}
	put(40, content)

	pagesNode := &Dictionary{}
	pagesNode.Set("Type", Name("Pages"))
	var kids Array
	num := 100
	for i := 0; i < nPages; i++ {
		res := &Dictionary{}
		res.Set("Font", IndirectRef{Number: 45})
		page := &Dictionary{}
		page.Set("Type", Name("Page"))
		page.Set("Parent", IndirectRef{Number: 10})
		page.Set("Contents", IndirectRef{Number: 40})
		page.Set("Resources", res)
		put(num, page)
		kids = append(kids, IndirectRef{Number: num})
		num++
	}
	pagesNode.Set("Kids", kids)
	pagesNode.Set("Count", Integer(nPages))
	put(10, pagesNode)

	cat := &Dictionary{}
	cat.Set("Type", Name("Catalog"))
	cat.Set("Pages", IndirectRef{Number: 10})
	put(1, cat)
	d.Trailer.Set("Root", IndirectRef{Number: 1})
	return d
}

// TestFontUsageSharedStreamDedup verifies that a content stream shared by many
// pages contributes its shown text to the font exactly once (not once per page)
// and that collection stays fast. Before the per-stream memoization this doc
// tokenized the shared stream once per page, making it quadratic.
func TestFontUsageSharedStreamDedup(t *testing.T) {
	const nPages = 20000
	doc := buildSharedStreamDoc(nPages)
	rd := *doc
	rd.valCache = &validationCache{
		pages:   map[int][]pageInfo{},
		content: map[*Stream][]byte{},
	}
	doc = &rd

	done := make(chan map[*Dictionary]*fontTextUsage, 1)
	start := time.Now()
	go func() { done <- collectFontTextUsage(doc) }()
	var usage map[*Dictionary]*fontTextUsage
	select {
	case usage = <-done:
	case <-time.After(30 * time.Second):
		t.Fatalf("collectFontTextUsage did not finish within 30s on a %d-page shared-stream doc", nPages)
	}
	t.Logf("collectFontTextUsage over %d pages took %v", nPages, time.Since(start))

	font := doc.ResolveDict(IndirectRef{Number: 50})
	u := usage[font]
	if u == nil {
		t.Fatal("shared font recorded no usage")
	}
	// The stream shows two strings; deduped across all pages that is exactly two,
	// not two per page.
	if len(u.strings) != 2 {
		t.Errorf("font usage has %d strings, want 2 (dedup across shared pages)", len(u.strings))
	}
	if got := string(u.strings[0]) + string(u.strings[1]); got != "ABCD" {
		t.Errorf("shown strings = %q, want AB+CD", got)
	}
	if !u.modes[0] {
		t.Error("render mode 0 not recorded")
	}
	// The single shared stream should have been tokenized once.
	if n := len(doc.valCache.fontEvents); n != 1 {
		t.Errorf("fontEvents cache holds %d streams, want 1", n)
	}
}

// TestRealContentSharedStreamMemo verifies that the real-content (7.1) check
// analyzes a shared stream once but still reports the violation for every page
// that uses it (each under its own object number).
func TestRealContentSharedStreamMemo(t *testing.T) {
	const nPages = 20000
	doc := buildSharedStreamDoc(nPages)
	rd := *doc
	rd.valCache = &validationCache{
		pages:   map[int][]pageInfo{},
		content: map[*Stream][]byte{},
	}
	doc = &rd
	cat := doc.ResolveDict(doc.Trailer.Get("Root"))

	done := make(chan []UAViolation, 1)
	go func() { done <- doc.checkUARealContent(cat) }()
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
	if n := len(doc.valCache.streamFacts); n != 1 {
		t.Errorf("streamFacts cache holds %d streams, want 1", n)
	}
}
