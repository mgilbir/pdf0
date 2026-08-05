package pdf0

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// The logical structure tree.

// taggedPage draws a page whose content carries the marked-content identifiers
// 0 and 1, which is the other half of what a structure tree describes.
func taggedPage(t *testing.T, d *Document) object.IndirectRef {
	t.Helper()
	var b content.Builder
	b.BeginTagged("P", 0).Rect(10, 10, 100, 20).Fill().EndMarked()
	b.BeginTagged("P", 1).Rect(10, 40, 100, 20).Fill().EndMarked()
	ref, err := d.AddPage(Page{Width: 200, Height: 200, Content: &b})
	if err != nil {
		t.Fatalf("adding the page: %v", err)
	}
	return ref
}

// TestBeginTaggedWritesAnInlinePropertyList pins the content-stream half. The
// identifier is written inline rather than through a named resource: every span
// has its own, and a page of a thousand paragraphs would otherwise need a
// thousand named entries used once each.
func TestBeginTaggedWritesAnInlinePropertyList(t *testing.T) {
	var b content.Builder
	b.BeginTagged("P", 7).EndMarked()
	got, err := b.Bytes()
	if err != nil {
		t.Fatalf("building: %v", err)
	}
	if want := "/P <</MCID 7>> BDC\nEMC\n"; string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if len(b.Resources().Properties) != 0 {
		t.Error("an inline property list was recorded as a named resource")
	}

	var bad content.Builder
	bad.BeginTagged("P", -1)
	if _, err := bad.Bytes(); err == nil {
		t.Error("a negative marked-content identifier was accepted")
	}
}

// TestStructureTreeIsWrittenAndMarked pins the shape a reader walks: a marked
// document, a tree root, and elements that name their tag, parent and page.
func TestStructureTreeIsWrittenAndMarked(t *testing.T) {
	doc := NewDocument()
	page := taggedPage(t, doc)
	err := doc.SetStructureTree([]StructElem{{
		Tag:  "Document",
		Page: &page,
		Children: []StructElem{
			{Tag: "H1", Content: []int{0}},
			{Tag: "P", Content: []int{1}},
		},
	}}, nil)
	if err != nil {
		t.Fatalf("setting the tree: %v", err)
	}

	catalog := doc.ResolveDict(doc.Trailer.Get("Root"))
	markInfo := doc.ResolveDict(catalog.Get("MarkInfo"))
	if markInfo == nil || markInfo.Get("Marked") != object.Boolean(true) {
		t.Fatal("the document is not marked as tagged")
	}
	root := doc.ResolveDict(catalog.Get("StructTreeRoot"))
	if root == nil || root.Get("Type") != object.Name("StructTreeRoot") {
		t.Fatal("no structure tree root")
	}

	docElem := doc.ResolveDict(root.Get("K"))
	if docElem == nil || docElem.Get("S") != object.Name("Document") {
		t.Fatalf("the root's child is %v, want a Document element", root.Get("K"))
	}
	kids, _ := doc.Resolve(docElem.Get("K")).(object.Array)
	if len(kids) != 2 {
		t.Fatalf("the Document element has %d children, want 2", len(kids))
	}
	h1 := doc.ResolveDict(kids[0])
	if h1.Get("S") != object.Name("H1") {
		t.Errorf("the first child is %v, want H1", h1.Get("S"))
	}
	// The page is inherited from the parent that stated it.
	if h1.Get("Pg") != page {
		t.Errorf("the heading's page is %v, want the page its marks are on", h1.Get("Pg"))
	}
	if h1.Get("K") != object.Integer(0) {
		t.Errorf("the heading owns %v, want identifier 0", h1.Get("K"))
	}
}

// TestParentTreeTakesAMarkBackToItsElement is the bookkeeping this exists to do.
//
// A reader that has a mark and wants to know what it is looks up the page's
// index in the parent tree and then the identifier in the array it finds. Every
// step of that has to line up, and none of it is visible when it does not:
// getting an index wrong makes a reader attribute content to the wrong element,
// silently.
func TestParentTreeTakesAMarkBackToItsElement(t *testing.T) {
	doc := NewDocument()
	page := taggedPage(t, doc)
	err := doc.SetStructureTree([]StructElem{{
		Tag:  "Document",
		Page: &page,
		Children: []StructElem{
			{Tag: "H1", Content: []int{0}},
			{Tag: "P", Content: []int{1}},
		},
	}}, nil)
	if err != nil {
		t.Fatalf("setting the tree: %v", err)
	}

	// The page carries its index into the tree.
	pageDict := doc.ResolveDict(page)
	index, ok := pageDict.Get("StructParents").(object.Integer)
	if !ok {
		t.Fatal("the page has no /StructParents, so nothing on it can be traced")
	}

	catalog := doc.ResolveDict(doc.Trailer.Get("Root"))
	root := doc.ResolveDict(catalog.Get("StructTreeRoot"))
	parentTree := doc.ResolveDict(root.Get("ParentTree"))
	nums, _ := doc.Resolve(parentTree.Get("Nums")).(object.Array)
	if len(nums) != 2 || nums[0] != index {
		t.Fatalf("the parent tree's keys are %v, want the page's index %v", nums, index)
	}
	elems, _ := doc.Resolve(nums[1]).(object.Array)
	if len(elems) != 2 {
		t.Fatalf("the page's entry has %d slots, want one per identifier", len(elems))
	}
	if got := doc.ResolveDict(elems[0]); got == nil || got.Get("S") != object.Name("H1") {
		t.Errorf("identifier 0 leads to %v, want the heading", elems[0])
	}
	if got := doc.ResolveDict(elems[1]); got == nil || got.Get("S") != object.Name("P") {
		t.Errorf("identifier 1 leads to %v, want the paragraph", elems[1])
	}
	if root.Get("ParentTreeNextKey") != object.Integer(1) {
		t.Errorf("/ParentTreeNextKey = %v, want 1", root.Get("ParentTreeNextKey"))
	}
}

// TestParentTreeEntriesAreDense pins that a gap is a null rather than a shorter
// array. The position in the array *is* the identifier, so an array that skips
// one shifts every element after it onto the wrong content.
func TestParentTreeEntriesAreDense(t *testing.T) {
	doc := NewDocument()
	var b content.Builder
	b.BeginTagged("P", 3).Rect(0, 0, 5, 5).Fill().EndMarked()
	page, err := doc.AddPage(Page{Width: 100, Height: 100, Content: &b})
	if err != nil {
		t.Fatalf("adding the page: %v", err)
	}
	// Only identifier 3 is used; 0, 1 and 2 are not.
	if err := doc.SetStructureTree([]StructElem{{Tag: "P", Page: &page, Content: []int{3}}}, nil); err != nil {
		t.Fatalf("setting the tree: %v", err)
	}

	catalog := doc.ResolveDict(doc.Trailer.Get("Root"))
	root := doc.ResolveDict(catalog.Get("StructTreeRoot"))
	parentTree := doc.ResolveDict(root.Get("ParentTree"))
	nums, _ := doc.Resolve(parentTree.Get("Nums")).(object.Array)
	elems, _ := doc.Resolve(nums[1]).(object.Array)
	if len(elems) != 4 {
		t.Fatalf("the entry has %d slots, want 4 so that index 3 is identifier 3", len(elems))
	}
	for i := 0; i < 3; i++ {
		if _, isNull := elems[i].(object.Null); !isNull {
			t.Errorf("slot %d is %v, want null for an identifier nothing claims", i, elems[i])
		}
	}
	if got := doc.ResolveDict(elems[3]); got == nil || got.Get("S") != object.Name("P") {
		t.Errorf("slot 3 is %v, want the paragraph", elems[3])
	}
}

// TestSeveralPagesEachGetTheirOwnIndex pins that identifiers are per page. The
// same number on two pages is two different pieces of content, and a tree that
// conflated them would send a reader to the wrong one.
func TestSeveralPagesEachGetTheirOwnIndex(t *testing.T) {
	doc := NewDocument()
	first := taggedPage(t, doc)
	second := taggedPage(t, doc)
	err := doc.SetStructureTree([]StructElem{
		{Tag: "Sect", Page: &first, Children: []StructElem{{Tag: "P", Content: []int{0}}}},
		{Tag: "Sect", Page: &second, Children: []StructElem{{Tag: "P", Content: []int{0}}}},
	}, nil)
	if err != nil {
		t.Fatalf("setting the tree: %v", err)
	}
	a := doc.ResolveDict(first).Get("StructParents")
	b := doc.ResolveDict(second).Get("StructParents")
	if a == b {
		t.Errorf("both pages have index %v; each page needs its own", a)
	}
	catalog := doc.ResolveDict(doc.Trailer.Get("Root"))
	root := doc.ResolveDict(catalog.Get("StructTreeRoot"))
	if root.Get("ParentTreeNextKey") != object.Integer(2) {
		t.Errorf("/ParentTreeNextKey = %v, want 2", root.Get("ParentTreeNextKey"))
	}
}

// TestAnIdentifierBelongsToOneElement pins the contradiction that would
// otherwise resolve silently. The parent tree has one slot per identifier, so a
// second claim replaces the first and a reader attributes the marks to the
// wrong element.
func TestAnIdentifierBelongsToOneElement(t *testing.T) {
	doc := NewDocument()
	page := taggedPage(t, doc)
	err := doc.SetStructureTree([]StructElem{{
		Tag:  "Document",
		Page: &page,
		Children: []StructElem{
			{Tag: "H1", Content: []int{0}},
			{Tag: "P", Content: []int{0}}, // the same identifier again
		},
	}}, nil)
	if err == nil {
		t.Fatal("two elements were allowed to own one marked-content identifier")
	}
	if !strings.Contains(err.Error(), "twice") {
		t.Errorf("the error is %q; it should say what the conflict is", err)
	}
}

// TestUnknownTagsNeedARoleMap pins that a tag a reader cannot interpret is
// refused rather than written. An element whose type means nothing is an
// element a screen reader skips, which is worse than no tagging at all because
// the document claims to be tagged.
func TestUnknownTagsNeedARoleMap(t *testing.T) {
	doc := NewDocument()
	page := taggedPage(t, doc)

	if err := doc.SetStructureTree([]StructElem{{Tag: "Paragraph", Page: &page}}, nil); err == nil {
		t.Error("a non-standard tag was accepted with no role map")
	}
	// With a role map saying what it behaves like, it is fine.
	err := doc.SetStructureTree(
		[]StructElem{{Tag: "Paragraph", Page: &page}},
		map[string]string{"Paragraph": "P"},
	)
	if err != nil {
		t.Fatalf("a mapped tag was refused: %v", err)
	}
	catalog := doc.ResolveDict(doc.Trailer.Get("Root"))
	root := doc.ResolveDict(catalog.Get("StructTreeRoot"))
	rm := doc.ResolveDict(root.Get("RoleMap"))
	if rm == nil || rm.Get("Paragraph") != object.Name("P") {
		t.Errorf("the role map is %v, want Paragraph -> P", root.Get("RoleMap"))
	}

	// A role map that points at something equally meaningless is no better.
	err = doc.SetStructureTree(
		[]StructElem{{Tag: "Paragraph", Page: &page}},
		map[string]string{"Paragraph": "AlsoNonsense"},
	)
	if err == nil {
		t.Error("a role map onto a non-standard type was accepted")
	}
}

// TestStructureTreeRefusesWhatCannotBeResolved collects the rest.
func TestStructureTreeRefusesWhatCannotBeResolved(t *testing.T) {
	doc := NewDocument()
	page := taggedPage(t, doc)
	cases := map[string][]StructElem{
		"no tag":                 {{Page: &page}},
		"content without a page": {{Tag: "P", Content: []int{0}}},
		"negative identifier":    {{Tag: "P", Page: &page, Content: []int{-1}}},
		"unknown nested tag":     {{Tag: "Sect", Page: &page, Children: []StructElem{{Tag: "Bogus"}}}},
	}
	for name, elems := range cases {
		if err := doc.SetStructureTree(elems, nil); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}

	// And a tree nested past the bound.
	deep := StructElem{Tag: "Div", Page: &page}
	node := &deep
	for i := 0; i < maxStructureDepth+2; i++ {
		node.Children = []StructElem{{Tag: "Div"}}
		node = &node.Children[0]
	}
	if err := doc.SetStructureTree([]StructElem{deep}, nil); err == nil {
		t.Error("a tree nested past the depth bound was accepted")
	}
}

// TestEmptyTreeRemovesTheTagging pins that clearing the structure also clears
// the claim to have any. A document marked as tagged with no tree is worse than
// an untagged one: a reader trusts the claim.
func TestEmptyTreeRemovesTheTagging(t *testing.T) {
	doc := NewDocument()
	page := taggedPage(t, doc)
	if err := doc.SetStructureTree([]StructElem{{Tag: "P", Page: &page, Content: []int{0}}}, nil); err != nil {
		t.Fatalf("setting: %v", err)
	}
	if err := doc.SetStructureTree(nil, nil); err != nil {
		t.Fatalf("clearing: %v", err)
	}
	catalog := doc.ResolveDict(doc.Trailer.Get("Root"))
	if catalog.Get("StructTreeRoot") != nil {
		t.Error("the tree root survived being cleared")
	}
	if catalog.Get("MarkInfo") != nil {
		t.Error("the document still claims to be tagged with no tree to back it")
	}
}

// TestAlternateTextReachesTheElement pins the field a checker reports first. A
// figure with no alternate text is the most common accessibility failure in a
// generated PDF.
func TestAlternateTextReachesTheElement(t *testing.T) {
	doc := NewDocument()
	page := taggedPage(t, doc)
	err := doc.SetStructureTree([]StructElem{{
		Tag: "Figure", Page: &page, Content: []int{0},
		Alt: "A bar chart of quarterly revenue", Lang: "en-GB",
		ActualText: "chart",
	}}, nil)
	if err != nil {
		t.Fatalf("setting: %v", err)
	}
	catalog := doc.ResolveDict(doc.Trailer.Get("Root"))
	root := doc.ResolveDict(catalog.Get("StructTreeRoot"))
	fig := doc.ResolveDict(root.Get("K"))
	if s, _ := fig.Get("Alt").(object.String); string(s.Value) != "A bar chart of quarterly revenue" {
		t.Errorf("/Alt = %q", s.Value)
	}
	if s, _ := fig.Get("Lang").(object.String); string(s.Value) != "en-GB" {
		t.Errorf("/Lang = %q", s.Value)
	}
	if s, _ := fig.Get("ActualText").(object.String); string(s.Value) != "chart" {
		t.Errorf("/ActualText = %q", s.Value)
	}
}

// TestTaggedDocumentValidatesAtLevelA is the point of the whole exercise: level
// A is the conformance that requires a structure tree, and it was unreachable
// before this existed.
func TestTaggedDocumentValidatesAtLevelA(t *testing.T) {
	for _, level := range []pdfa.Level{pdfa.PDFA1a, pdfa.PDFA2a} {
		t.Run(level.String(), func(t *testing.T) {
			doc := NewPDFADocument(level)
			var b content.Builder
			b.BeginTagged("P", 0).SetRGB(0, 0, 0).Rect(10, 10, 100, 20).Fill().EndMarked()
			page, err := doc.AddPage(Page{Width: 200, Height: 200, Content: &b})
			if err != nil {
				t.Fatalf("adding the page: %v", err)
			}
			err = doc.SetStructureTree([]StructElem{{
				Tag: "Document", Page: &page,
				Children: []StructElem{{Tag: "P", Content: []int{0}}},
			}}, nil)
			if err != nil {
				t.Fatalf("setting the tree: %v", err)
			}

			var buf bytes.Buffer
			if err := doc.Write(&buf); err != nil {
				t.Fatalf("write: %v", err)
			}
			rd, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
			if err != nil {
				t.Fatalf("reparse: %v", err)
			}
			if v := ValidatePDFABytes(rd, level, buf.Bytes()); len(v) != 0 {
				t.Errorf("a tagged document is not %s: %v", level, v)
			}
		})
	}
}
