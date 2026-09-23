package pdf0

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/object"
)

// The page-import tests work on documents that went through Read, written as
// PDF syntax rather than assembled from Go values, so that what they exercise is
// the shape a real file has once parsed: nested page trees, attributes on the
// intermediate nodes, pages that point at each other, form fields spread over
// several pages. pdf0's own writer only ever produces a flat tree with every
// attribute on the page, which is the one shape the old copier handled.

// assemblePDF lays out the numbered object bodies as a PDF file with a correct
// cross-reference table. Object 1 must be the catalog.
func assemblePDF(objs map[int]string) []byte {
	var buf bytes.Buffer
	buf.WriteString("%PDF-2.0\n%\x80\x80\x80\x80\n")
	nums := make([]int, 0, len(objs))
	maxNum := 0
	for n := range objs {
		nums = append(nums, n)
		maxNum = max(maxNum, n)
	}
	slices.Sort(nums)
	offsets := map[int]int{}
	for _, n := range nums {
		offsets[n] = buf.Len()
		body := objs[n]
		if strings.HasPrefix(body, "stream:") {
			data := strings.TrimPrefix(body, "stream:")
			dict, content, _ := strings.Cut(data, "|")
			fmt.Fprintf(&buf, "%d 0 obj\n<< %s /Length %d >>\nstream\n%s\nendstream\nendobj\n", n, dict, len(content), content)
			continue
		}
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", n, body)
	}
	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \r\n", maxNum+1)
	for n := 1; n <= maxNum; n++ {
		if off, ok := offsets[n]; ok {
			fmt.Fprintf(&buf, "%010d 00000 n \r\n", off)
		} else {
			buf.WriteString("0000000000 65535 f \r\n")
		}
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", maxNum+1, xref)
	return buf.Bytes()
}

// readAssembled assembles and reads objs.
func readAssembled(t *testing.T, objs map[int]string) *Document {
	t.Helper()
	data := assemblePDF(objs)
	doc, err := Read(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("reading the assembled fixture: %v", err)
	}
	return doc
}

// textContent is a content stream that shows label with font /F1.
func textContent(label string) string {
	return "stream:|BT /F1 12 Tf 10 10 Td (" + label + ") Tj ET"
}

// nestedTreeObjects is the realistic fixture: four pages under two
// intermediate /Pages nodes, with the attributes a producer puts on the tree
// rather than the page.
//
//	2  root   /MediaBox [0 0 200 300] /Rotate 90 /Resources <</Font <</F1 20 0 R>>>>
//	3  mid A  (inherits everything)          → pages 10 (p0), 11 (p1)
//	4  mid B  /CropBox [10 10 190 290] /Rotate 180 /Resources 21 0 R (F1 → 22)
//	                                         → pages 12 (p2), 13 (p3)
//
// Page 11 overrides /MediaBox with its own. The pages point at each other:
//
//	p0 (10): Link /Dest [13 0 R /Fit]                 (explicit, to p3)
//	         Link /Dest (chap1)                        (named, → p1 via /Names)
//	         Link /A GoTo /D [12 0 R /XYZ 0 0 null]    (action, to p2)
//	         Text annotation 44 with /Popup 45, /P 10
//	         Widget 50: kid of radio field 60 (other kid 51 on p2)
//	         Widget/field 52: text field "name" (merged field and widget)
//	         /B [70 0 R] (an article bead)
//	p1 (11): /StructParents 0
//	p2 (12): Widget 51 (second kid of radio field 60)
//	p3 (13): uses optional content group 80 through /Properties
//
// The catalog carries /AcroForm, /StructTreeRoot, /Outlines, /OutputIntents,
// /OCProperties (with 80 OFF by default), /Threads and the named destination.
func nestedTreeObjects() map[int]string {
	return map[int]string{
		1: `<< /Type /Catalog /Pages 2 0 R /Names << /Dests 90 0 R >>
			/AcroForm << /Fields [60 0 R 52 0 R] /DR << /Font << /Helv 23 0 R >> >> /DA (/Helv 0 Tf 0 g) >>
			/StructTreeRoot 95 0 R /MarkInfo << /Marked true >>
			/Outlines 96 0 R /OutputIntents [98 0 R]
			/OCProperties << /OCGs [80 0 R] /D << /OFF [80 0 R] /Order [80 0 R] >> >>
			/Threads [71 0 R] /Lang (en) >>`,
		2: `<< /Type /Pages /Kids [3 0 R 4 0 R] /Count 4 /MediaBox [0 0 200 300] /Rotate 90
			/Resources << /Font << /F1 20 0 R >> >> >>`,
		3: `<< /Type /Pages /Parent 2 0 R /Kids [10 0 R 11 0 R] /Count 2 >>`,
		4: `<< /Type /Pages /Parent 2 0 R /Kids [12 0 R 13 0 R] /Count 2 /CropBox [10 10 190 290] /Rotate 180 /Resources 21 0 R >>`,
		10: `<< /Type /Page /Parent 3 0 R /Contents 30 0 R
			/Annots [40 0 R 41 0 R 42 0 R 44 0 R 45 0 R 50 0 R 52 0 R] /B [70 0 R] >>`,
		11: `<< /Type /Page /Parent 3 0 R /Contents 31 0 R /MediaBox [0 0 400 500] /StructParents 0 >>`,
		12: `<< /Type /Page /Parent 4 0 R /Contents 32 0 R /Annots [51 0 R] >>`,
		13: `<< /Type /Page /Parent 4 0 R /Contents 33 0 R >>`,
		20: `<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>`,
		21: `<< /Font << /F1 22 0 R >> /Properties << /OC1 80 0 R >> >>`,
		22: `<< /Type /Font /Subtype /Type1 /BaseFont /Courier >>`,
		23: `<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>`,
		30: textContent("p0"),
		31: textContent("p1"),
		32: textContent("p2"),
		33: "stream:|/OC /OC1 BDC BT /F1 12 Tf 10 10 Td (p3) Tj ET EMC",
		40: `<< /Type /Annot /Subtype /Link /Rect [0 0 10 10] /P 10 0 R /Dest [13 0 R /Fit] >>`,
		41: `<< /Type /Annot /Subtype /Link /Rect [0 20 10 30] /P 10 0 R /Dest (chap1) >>`,
		42: `<< /Type /Annot /Subtype /Link /Rect [0 40 10 50] /P 10 0 R /A 43 0 R >>`,
		43: `<< /Type /Action /S /GoTo /D [12 0 R /XYZ 0 0 null] >>`,
		44: `<< /Type /Annot /Subtype /Text /Rect [20 0 30 10] /P 10 0 R /Contents (note) /Popup 45 0 R /StructParent 1 >>`,
		45: `<< /Type /Annot /Subtype /Popup /Rect [20 20 80 60] /P 10 0 R /Parent 44 0 R >>`,
		50: `<< /Type /Annot /Subtype /Widget /Rect [40 0 50 10] /P 10 0 R /Parent 60 0 R /AS /Off
			/AP << /N << /On 53 0 R /Off 54 0 R >> >> >>`,
		51: `<< /Type /Annot /Subtype /Widget /Rect [40 0 50 10] /P 12 0 R /Parent 60 0 R /AS /Off
			/AP << /N << /On 53 0 R /Off 54 0 R >> >> >>`,
		52: `<< /Type /Annot /Subtype /Widget /FT /Tx /T (name) /V (Ada) /Rect [60 0 120 10] /P 10 0 R /DA (/Helv 0 Tf 0 g) >>`,
		53: "stream:/Type /XObject /Subtype /Form /BBox [0 0 10 10]|0 0 10 10 re f",
		54: "stream:/Type /XObject /Subtype /Form /BBox [0 0 10 10]|",
		60: `<< /FT /Btn /Ff 49152 /T (choice) /V /Off /Kids [50 0 R 51 0 R] >>`,
		70: `<< /Type /Bead /T 71 0 R /N 70 0 R /V 70 0 R /P 10 0 R /R [0 0 100 100] >>`,
		71: `<< /Type /Thread /F 70 0 R >>`,
		80: `<< /Type /OCG /Name (Layer) >>`,
		90: `<< /Names [(chap1) [11 0 R /Fit]] >>`,
		95: `<< /Type /StructTreeRoot /K 97 0 R /ParentTree << /Nums [0 [97 0 R] 1 97 0 R] >> /ParentTreeNextKey 2 >>`,
		96: `<< /Type /Outlines /First 99 0 R /Last 99 0 R /Count 1 >>`,
		97: `<< /Type /StructElem /S /P /P 95 0 R /Pg 11 0 R /K 0 >>`,
		98: `<< /Type /OutputIntent /S /GTS_PDFA1 /OutputConditionIdentifier (sRGB) >>`,
		99: `<< /Title (Chapter) /Parent 96 0 R /Dest [11 0 R /Fit] >>`,
	}
}

// flatTreeObjects is the ordinary one-level document: n pages, each with every
// attribute of its own.
func flatTreeObjects(n int) map[int]string {
	objs := map[int]string{
		1:  `<< /Type /Catalog /Pages 2 0 R >>`,
		20: `<< /Type /Font /Subtype /Type1 /BaseFont /Times-Roman >>`,
	}
	var kids []string
	for i := 0; i < n; i++ {
		pg, cs := 100+i, 200+i
		objs[pg] = fmt.Sprintf(`<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents %d 0 R /Resources << /Font << /F1 20 0 R >> >> >>`, cs)
		objs[cs] = textContent(fmt.Sprintf("flat%d", i))
		kids = append(kids, fmt.Sprintf("%d 0 R", pg))
	}
	objs[2] = fmt.Sprintf(`<< /Type /Pages /Kids [%s] /Count %d >>`, strings.Join(kids, " "), n)
	return objs
}

// pageTexts returns the text of each page, in page order.
func pageTexts(t *testing.T, d *Document) []string {
	t.Helper()
	var out []string
	for _, pg := range d.PageList() {
		text, err := d.ExtractPageText(pg)
		if err != nil {
			t.Fatalf("ExtractPageText: %v", err)
		}
		out = append(out, strings.TrimSpace(text))
	}
	return out
}

// writeRead writes d and reads it back.
func writeRead(t *testing.T, d *Document) *Document {
	t.Helper()
	var buf bytes.Buffer
	if err := d.Write(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	re, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	return re
}

// rootCount returns the /Count of the catalog's page-tree root.
func rootCount(t *testing.T, d *Document) int {
	t.Helper()
	cat := d.ResolveDict(d.Trailer.Get("Root"))
	pages := d.ResolveDict(cat.Get("Pages"))
	if pages == nil {
		t.Fatal("the document has no page-tree root")
	}
	n, ok := d.Resolve(pages.Get("Count")).(object.Integer)
	if !ok {
		t.Fatalf("the page-tree root /Count is %v", pages.Get("Count"))
	}
	return int(n)
}

// checkCounts asserts that every node of d's page tree has /Count equal to the
// number of pages beneath it, and that every page's /Parent is the node whose
// /Kids lists it.
func checkCounts(t *testing.T, d *Document) {
	t.Helper()
	cat := d.ResolveDict(d.Trailer.Get("Root"))
	var walk func(ref object.Object, parent object.Object, depth int) int
	walk = func(ref object.Object, parent object.Object, depth int) int {
		if depth > 32 {
			t.Fatal("page tree deeper than 32")
		}
		node := d.ResolveDict(ref)
		if node == nil {
			t.Fatalf("page-tree node %v does not resolve", ref)
		}
		if parent != nil {
			if got := node.Get("Parent"); got != parent {
				t.Errorf("node %v has /Parent %v, want %v", ref, got, parent)
			}
		}
		if typ, _ := node.Get("Type").(object.Name); typ == "Page" {
			return 1
		}
		kids, _ := d.Resolve(node.Get("Kids")).(object.Array)
		n := 0
		for _, k := range kids {
			n += walk(k, ref, depth+1)
		}
		if got := object.Int(d.Resolve(node.Get("Count"))); got != n {
			t.Errorf("node %v has /Count %d, and %d pages beneath it", ref, got, n)
		}
		return n
	}
	root := cat.Get("Pages")
	if _, ok := root.(object.IndirectRef); !ok {
		t.Errorf("the catalog's /Pages is %T, want an indirect reference", root)
	}
	if n := walk(root, nil, 0); n != d.PageCount() {
		t.Errorf("the tree holds %d pages, PageCount says %d", n, d.PageCount())
	}
}
