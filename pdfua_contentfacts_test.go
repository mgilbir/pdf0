package pdf0

import "testing"

// TestContentFactsSinglePass verifies that one tokenizeContent pass extracts
// both the real-content (7.1) messages and the Do-operator name sequence the
// form-XObject-painting check needs — the two facts that were previously two
// separate passes over the same bytes.
func TestContentFactsSinglePass(t *testing.T) {
	// /Im1 Do paints an XObject; the two Tj outside any BDC/BMC are untagged
	// real content; the tagged run is fine.
	content := []byte("/Im1 Do (untagged) Tj /P << /MCID 0 >> BDC (ok) Tj EMC (also untagged) Tj")
	f := buildContentFacts(content)

	if len(f.doNames) != 1 || f.doNames[0] != "Im1" {
		t.Errorf("doNames = %v, want [Im1]", f.doNames)
	}
	// The untagged-text message is reported once (deduped), even though two Tj
	// operators are outside marked content.
	if len(f.realMsgs) != 1 ||
		f.realMsgs[0] != "page contains text that is neither tagged nor marked as an /Artifact" {
		t.Errorf("realMsgs = %v, want the single untagged-text message", f.realMsgs)
	}
}

// TestContentFactsCacheShared verifies that a stream examined by both the
// real-content check and the form-MCID check is tokenized once: the cached
// facts are reused, and the Do-count and real-content violations are both
// derived from the same cached entry.
func TestContentFactsCacheShared(t *testing.T) {
	d := &Document{Objects: map[int]*IndirectObject{}, Version: "1.7"}
	put := func(n int, v Object) { d.Objects[n] = &IndirectObject{Number: n, Value: v} }

	// A tagged form XObject painted twice from one page → 7.20 violation.
	form := &Stream{Dict: Dictionary{}, Data: []byte("/P << /MCID 0 >> BDC (x) Tj EMC")}
	form.Dict.Set("Type", Name("XObject"))
	form.Dict.Set("Subtype", Name("Form"))
	put(20, form)

	xobjRes := &Dictionary{}
	xobjRes.Set("Fm0", IndirectRef{Number: 20})
	res := &Dictionary{}
	res.Set("XObject", xobjRes)

	pageContent := &Stream{Dict: Dictionary{}, Data: []byte("/Fm0 Do /Fm0 Do (loose) Tj")}
	put(30, pageContent)

	page := &Dictionary{}
	page.Set("Type", Name("Page"))
	page.Set("Parent", IndirectRef{Number: 10})
	page.Set("Contents", IndirectRef{Number: 30})
	page.Set("Resources", res)
	put(11, page)

	pages := &Dictionary{}
	pages.Set("Type", Name("Pages"))
	pages.Set("Kids", Array{IndirectRef{Number: 11}})
	pages.Set("Count", Integer(1))
	put(10, pages)

	cat := &Dictionary{}
	cat.Set("Type", Name("Catalog"))
	cat.Set("Pages", IndirectRef{Number: 10})
	put(1, cat)
	d.Trailer.Set("Root", IndirectRef{Number: 1})

	rd := *d
	rd.valCache = &validationCache{pages: map[int][]pageInfo{}, content: map[*Stream][]byte{}}
	doc := &rd

	// Both checks run against the same page content stream.
	mcid := doc.checkUAFormXObjectMCID()
	real := doc.checkUARealContent(cat)

	if len(mcid) != 1 || mcid[0].Clause != "7.20" {
		t.Errorf("expected one 7.20 violation for the twice-painted tagged form, got %v", mcid)
	}
	if len(real) != 1 || real[0].Object != 11 {
		t.Errorf("expected one real-content violation on page 11, got %v", real)
	}
	// The page content stream was tokenized once and cached for both checks.
	if _, ok := doc.valCache.streamFacts[pageContent]; !ok {
		t.Error("page content stream facts not cached")
	}
	if n := len(doc.valCache.streamFacts[pageContent].doNames); n != 2 {
		t.Errorf("page doNames = %d, want 2 (two Do operators)", n)
	}
}
