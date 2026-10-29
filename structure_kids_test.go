package pdf0

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/object"
)

// A structure element's kids — marks, child elements, annotations — in the
// order a reader reads them.

// threeMarks is a page with marked-content spans 0, 1 and 2, and one link.
func threeMarks(t *testing.T, d *Document) (page, link object.IndirectRef) {
	t.Helper()
	var b content.Builder
	for i := range 3 {
		b.BeginTagged("Span", i).Rect(10, float64(10+30*i), 100, 20).Fill().EndMarked()
	}
	page, err := d.AddPage(Page{Width: 200, Height: 200, Content: &b,
		Links: []Link{{Rect: [4]float64{10, 70, 110, 90}, URI: "https://example.com", Contents: "the example site"}}})
	if err != nil {
		t.Fatalf("adding the page: %v", err)
	}
	annots, err := d.PageAnnotations(page)
	if err != nil || len(annots) != 1 {
		t.Fatalf("PageAnnotations = %v, %v; want the one link", annots, err)
	}
	return page, annots[0]
}

// kidsOf is an element's /K as a list.
func kidsOf(d *Document, elem *object.Dictionary) object.Array {
	switch k := d.Resolve(elem.Get("K")).(type) {
	case object.Array:
		return k
	case nil:
		return nil
	default:
		return object.Array{elem.Get("K")}
	}
}

// TestKidsKeepTheirOrder: a paragraph whose middle word is emphasised is the
// marks before the word, the Em, and the marks after, in that order. Content
// before children, as the tree used to be written, reads the word last.
func TestKidsKeepTheirOrder(t *testing.T) {
	d := NewDocument()
	page, _ := threeMarks(t, d)
	if err := d.SetStructureTree([]StructElem{{Tag: "P", Page: &page, Kids: []StructKid{
		MCIDKid(0), ElemKid(StructElem{Tag: "Span", Kids: []StructKid{MCIDKid(1)}}), MCIDKid(2),
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	root := d.ResolveDict(d.ResolveDict(d.Trailer.Get("Root")).Get("StructTreeRoot"))
	p := d.ResolveDict(root.Get("K"))
	k := kidsOf(d, p)
	if len(k) != 3 || k[0] != object.Integer(0) || k[2] != object.Integer(2) {
		t.Fatalf("P's /K = %v, want [0 <Span> 2]", k)
	}
	if span := d.ResolveDict(k[1]); span == nil || span.Get("S") != object.Name("Span") || span.Get("K") != object.Integer(1) {
		t.Errorf("P's second kid = %v, want the Span holding 1", k[1])
	}
}

// TestAnAnnotationKidIsAnObjectReference: a link's annotation is a kid of its
// Link element as an /OBJR, and the annotation's /StructParent leads back to
// the element through the parent tree, keyed after the pages.
func TestAnAnnotationKidIsAnObjectReference(t *testing.T) {
	d := NewDocument()
	page, link := threeMarks(t, d)
	if err := d.SetStructureTree([]StructElem{{Tag: "Link", Page: &page, Kids: []StructKid{MCIDKid(0), AnnotKid(link)}}}, nil); err != nil {
		t.Fatal(err)
	}
	root := d.ResolveDict(d.ResolveDict(d.Trailer.Get("Root")).Get("StructTreeRoot"))
	linkRef := root.Get("K")
	k := kidsOf(d, d.ResolveDict(linkRef))
	if len(k) != 2 {
		t.Fatalf("Link's /K = %v, want [0 <OBJR>]", k)
	}
	objr := d.ResolveDict(k[1])
	if objr == nil || objr.Get("Type") != object.Name("OBJR") || objr.Get("Obj") != link || objr.Get("Pg") != page {
		t.Errorf("Link's second kid = %v, want an /OBJR naming the annotation on its page", objr)
	}
	annot := d.ResolveDict(link)
	if annot.Get("StructParent") != object.Integer(1) {
		t.Fatalf("the annotation's /StructParent = %v, want 1 (the page has 0)", annot.Get("StructParent"))
	}
	if s, _ := d.Resolve(annot.Get("Contents")).(object.String); string(s.Value) != "the example site" {
		t.Errorf("the link's /Contents = %v, want the description", annot.Get("Contents"))
	}
	nums, _ := d.Resolve(d.ResolveDict(root.Get("ParentTree")).Get("Nums")).(object.Array)
	if len(nums) != 4 || nums[2] != object.Integer(1) || nums[3] != linkRef {
		t.Errorf("parent tree /Nums = %v, want [0 <page> 1 <Link>]", nums)
	}
	if d.ResolveDict(page).Get("Tabs") != object.Name("S") {
		t.Errorf("the page's /Tabs = %v, want /S: tabbing follows the tree", d.ResolveDict(page).Get("Tabs"))
	}
	if root.Get("ParentTreeNextKey") != object.Integer(2) {
		t.Errorf("/ParentTreeNextKey = %v, want 2", root.Get("ParentTreeNextKey"))
	}
}

// TestAttributesAndIDs: attributes are one dictionary per owner, a lone one
// written alone; an ID is the element's /ID and an /IDTree entry, which is what
// a cell's Headers names.
func TestAttributesAndIDs(t *testing.T) {
	d := NewDocument()
	page, _ := threeMarks(t, d)
	th := StructElem{Tag: "TH", ID: "h1", Kids: []StructKid{MCIDKid(0)},
		Attributes: []StructAttr{{Owner: "Table", Key: "Scope", Value: object.Name("Column")}}}
	td := StructElem{Tag: "TD", Kids: []StructKid{MCIDKid(1)}, Attributes: []StructAttr{
		{Owner: "Table", Key: "Headers", Value: object.Array{object.String{Value: []byte("h1")}}},
		{Owner: "Layout", Key: "TextAlign", Value: object.Name("Center")},
		{Owner: "Table", Key: "ColSpan", Value: object.Integer(2)},
	}}
	if err := d.SetStructureTree([]StructElem{{Tag: "Table", Page: &page, Kids: []StructKid{
		ElemKid(StructElem{Tag: "TR", Kids: []StructKid{ElemKid(th)}}),
		ElemKid(StructElem{Tag: "TR", Kids: []StructKid{ElemKid(td)}}),
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	root := d.ResolveDict(d.ResolveDict(d.Trailer.Get("Root")).Get("StructTreeRoot"))
	rows := kidsOf(d, d.ResolveDict(root.Get("K")))
	thDict := d.ResolveDict(d.ResolveDict(rows[0]).Get("K"))
	tdDict := d.ResolveDict(d.ResolveDict(rows[1]).Get("K"))
	if a := d.ResolveDict(thDict.Get("A")); a == nil || a.Get("O") != object.Name("Table") || a.Get("Scope") != object.Name("Column") {
		t.Errorf("TH's /A = %v, want << /O /Table /Scope /Column >>", thDict.Get("A"))
	}
	if id, _ := thDict.Get("ID").(object.String); string(id.Value) != "h1" {
		t.Errorf("TH's /ID = %v, want (h1)", thDict.Get("ID"))
	}
	a, _ := d.Resolve(tdDict.Get("A")).(object.Array)
	if len(a) != 2 {
		t.Fatalf("TD's /A = %v, want a Table and a Layout dictionary", tdDict.Get("A"))
	}
	table, layout := d.ResolveDict(a[0]), d.ResolveDict(a[1])
	if table.Get("O") != object.Name("Table") || table.Get("ColSpan") != object.Integer(2) || table.Get("Headers") == nil {
		t.Errorf("TD's Table attributes = %v, want Headers and ColSpan in one dictionary", table)
	}
	if layout.Get("O") != object.Name("Layout") || layout.Get("TextAlign") != object.Name("Center") {
		t.Errorf("TD's Layout attributes = %v", layout)
	}
	names, _ := d.Resolve(d.ResolveDict(root.Get("IDTree")).Get("Names")).(object.Array)
	if len(names) != 2 || d.ResolveDict(names[1]) != thDict {
		t.Errorf("/IDTree /Names = %v, want [(h1) <TH>]", names)
	}
}

// TestStructureKidRefusals: each mistake is refused, and the document is left
// untagged.
func TestStructureKidRefusals(t *testing.T) {
	cases := map[string]func(page, link, other object.IndirectRef) []StructElem{
		"the zero kid": func(page, _, _ object.IndirectRef) []StructElem {
			return []StructElem{{Tag: "P", Page: &page, Kids: []StructKid{{}}}}
		},
		"an annotation with no page": func(_, link, _ object.IndirectRef) []StructElem {
			return []StructElem{{Tag: "Link", Kids: []StructKid{AnnotKid(link)}}}
		},
		"an annotation twice": func(page, link, _ object.IndirectRef) []StructElem {
			return []StructElem{{Tag: "Div", Page: &page, Kids: []StructKid{
				ElemKid(StructElem{Tag: "Link", Kids: []StructKid{AnnotKid(link)}}),
				ElemKid(StructElem{Tag: "Link", Kids: []StructKid{AnnotKid(link)}}),
			}}}
		},
		"an annotation not on the page": func(page, _, other object.IndirectRef) []StructElem {
			return []StructElem{{Tag: "Link", Page: &page, Kids: []StructKid{AnnotKid(other)}}}
		},
		"an ID twice": func(page, _, _ object.IndirectRef) []StructElem {
			return []StructElem{{Tag: "Div", Page: &page, Kids: []StructKid{
				ElemKid(StructElem{Tag: "P", ID: "x"}), ElemKid(StructElem{Tag: "P", ID: "x"}),
			}}}
		},
		"a header that is no element's ID": func(page, _, _ object.IndirectRef) []StructElem {
			return []StructElem{{Tag: "TD", Page: &page, Attributes: []StructAttr{
				{Owner: "Table", Key: "Headers", Value: object.Array{object.String{Value: []byte("nobody")}}}}}}
		},
		"Headers that are not strings": func(page, _, _ object.IndirectRef) []StructElem {
			return []StructElem{{Tag: "TD", Page: &page, Attributes: []StructAttr{
				{Owner: "Table", Key: "Headers", Value: object.Name("h1")}}}}
		},
		"an unknown owner": func(page, _, _ object.IndirectRef) []StructElem {
			return []StructElem{{Tag: "P", Page: &page, Attributes: []StructAttr{{Owner: "CSS-2.00", Key: "Color", Value: object.Name("red")}}}}
		},
		"an attribute with no value": func(page, _, _ object.IndirectRef) []StructElem {
			return []StructElem{{Tag: "TH", Page: &page, Attributes: []StructAttr{{Owner: "Table", Key: "Scope"}}}}
		},
		"the Artifact owner below PDF 2.0": func(page, _, _ object.IndirectRef) []StructElem {
			return []StructElem{{Tag: "P", Page: &page, Attributes: []StructAttr{{Owner: "Artifact", Key: "Type", Value: object.Name("Pagination")}}}}
		},
	}
	for name, tree := range cases {
		t.Run(name, func(t *testing.T) {
			d := NewDocument()
			d.Version = "1.7"
			page, link := threeMarks(t, d)
			other := d.Add(object.NewDictionary(object.Entry{Key: "Type", Value: object.Name("Annot")}, object.Entry{Key: "Subtype", Value: object.Name("Link")}))
			if err := d.SetStructureTree(tree(page, link, other), nil); err == nil {
				t.Error("accepted")
			}
			cat := d.ResolveDict(d.Trailer.Get("Root"))
			if cat.Get("StructTreeRoot") != nil || d.ResolveDict(link).Get("StructParent") != nil {
				t.Error("a refused tree left part of itself behind")
			}
		})
	}
}

// TestAnAccessibleDocumentWithALinkAndATable: a paragraph with an emphasised
// word and a described link in it, and a table whose cell names its header,
// declared PDF/UA-1. ValidatePDFUA has nothing to say about it, and neither
// does veraPDF when PDF0_VERAPDF names it.
func TestAnAccessibleDocumentWithALinkAndATable(t *testing.T) {
	face, err := fonts.NotoSans()
	if err != nil {
		t.Fatal(err)
	}
	d := NewDocument()
	d.Version = "1.7"
	var b content.Builder
	show := func(tag object.Name, mcid int, x, y float64, s string) {
		b.BeginTagged(tag, mcid).BeginText().SetFont("F1", 11).SetRGB(0, 0, 0).MoveText(x, y)
		face.DrawShaped(&b, s, 11)
		b.EndText().EndMarked()
	}
	show("Span", 0, 72, 760, "Revenue grew in")
	show("Span", 1, 152, 760, "every")
	show("Span", 2, 184, 760, "quarter; see")
	show("Span", 3, 252, 760, "the report")
	show("Span", 4, 72, 730, "Quarter")
	show("Span", 5, 72, 710, "Q1")
	page, err := d.AddPage(Page{Width: 595, Height: 842, Content: &b,
		Faces: map[object.Name]*fonts.Face{"F1": face},
		Links: []Link{{Rect: [4]float64{252, 756, 310, 772}, URI: "https://example.com/report", Contents: "the annual report"}}})
	if err != nil {
		t.Fatal(err)
	}
	annots, err := d.PageAnnotations(page)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.SetStructureTree([]StructElem{{Tag: "Document", Page: &page, Kids: []StructKid{
		ElemKid(StructElem{Tag: "P", Kids: []StructKid{
			MCIDKid(0),
			ElemKid(StructElem{Tag: "Span", Kids: []StructKid{MCIDKid(1)}}),
			MCIDKid(2),
			ElemKid(StructElem{Tag: "Link", Kids: []StructKid{MCIDKid(3), AnnotKid(annots[0])}}),
		}}),
		ElemKid(StructElem{Tag: "Table", Kids: []StructKid{
			ElemKid(StructElem{Tag: "TR", Kids: []StructKid{ElemKid(StructElem{Tag: "TH", ID: "q", Kids: []StructKid{MCIDKid(4)},
				Attributes: []StructAttr{{Owner: "Table", Key: "Scope", Value: object.Name("Column")}}})}}),
			ElemKid(StructElem{Tag: "TR", Kids: []StructKid{ElemKid(StructElem{Tag: "TD", Kids: []StructKid{MCIDKid(5)},
				Attributes: []StructAttr{{Owner: "Table", Key: "Headers", Value: object.Array{object.String{Value: []byte("q")}}}}})}}),
		}}),
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	describeAndDeclare(t, d, 1)
	rd := writtenAndRead(t, d)
	if v := uaViolations(ValidatePDFUA(rd)); len(v) != 0 {
		t.Errorf("ValidatePDFUA: %v", v)
	}
	vera := os.Getenv("PDF0_VERAPDF")
	if vera == "" {
		return
	}
	path := filepath.Join(t.TempDir(), "link-and-table.pdf")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Write(f); err != nil {
		t.Fatal(err)
	}
	f.Close()
	veraPDFPasses(t, vera, []string{path}, "--flavour", "ua1")
	if t.Failed() {
		t.Log(strings.Repeat("-", 8), "the file is at", path)
	}
}

// TestPageRefsNameThePages: one reference per page, in PageList's order, each
// resolving to that page's dictionary.
func TestPageRefsNameThePages(t *testing.T) {
	d := NewDocument()
	var want []object.IndirectRef
	for range 3 {
		var b content.Builder
		b.Rect(0, 0, 1, 1).Fill()
		ref, err := d.AddPage(Page{Width: 10, Height: 10, Content: &b})
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, ref)
	}
	got, pages := d.PageRefs(), d.PageList()
	if len(got) != 3 {
		t.Fatalf("PageRefs = %v, want 3", got)
	}
	for i := range got {
		if got[i] != want[i] || d.ResolveDict(got[i]) != pages[i] {
			t.Errorf("page %d: PageRefs gives %v, want %v, the dictionary PageList gives", i, got[i], want[i])
		}
	}
}
